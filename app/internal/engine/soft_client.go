// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"unsafe"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// Client decode of the digital stream (ADR-STREAM-CLIENT-DECODE): a web page
// that holds the client-decode lease takes the digital stream's words as
// they arrive and decodes them itself, with the same edge stream decoder and
// decoders (webdec.js), and posts the frames back. The scope then only
// copies the words it was handed into a ring the page reads (run-length
// coding its quiet lines) and queues the page's frames as its own, so the
// LCD, the transcript, the web and phone views and a stop on a value work
// as when it decodes; on the device a decode costs about 2 us an edge, which
// a dense bus outruns, and a PC or phone decodes many times faster. Without
// the lease (no page, a page that stopped asking) the scope decodes as
// before.

// clientLease is how long the client decode lasts after the page's last
// request: a page whose tab is hidden or whose Wi-Fi dropped hands the
// decode back to the scope after it.
const clientLease = 3 * time.Second

// clientTapBytes is the ring of coded words the page reads: a second of the
// densest digital stream (2^9, two busy lines), minutes of a quiet one.
const clientTapBytes = 4 << 20

// clientTapChunks bounds the chunks the ring holds (a quiet line's chunk
// is a few bytes).
const clientTapChunks = 8192

// clientBitsBudget is the samples a second the digital stream may carry when
// a page decodes it: every period the stream image has, two lines at 64 ns.
const clientBitsBudget = 32e6

// clientInboxMax bounds the page's frames queued for the owner.
const clientInboxMax = 1 << 16

// ClientStreamInfo is what a page needs to decode the stream as the scope
// would: the protocol and its settings, the digital stream's period and the
// stream decoder's sizes (in samples) and the quiet line's (in words).
type ClientStreamInfo struct {
	Session   uint32       `json:"session"` // the stream's identity; a new one restarts the page's decoder
	Proto     string       `json:"proto"`   // the decode package's name
	Params    SerialParams `json:"params"`
	PeriodS   float64      `json:"periodS"` // a sample (a word's sixteenth)
	Log       int          `json:"log"`
	Lines     int          `json:"lines"`
	CloseN    int          `json:"closeN"`
	MaxN      int          `json:"maxN"`
	LeadN     int          `json:"leadN"`
	PushN     int          `json:"pushN"`
	PushMax   int          `json:"pushMax"`
	KeepWords int          `json:"keepWords"`
	LeadWords int          `json:"leadWords"`
	Subframes bool         `json:"subframes"`
	Mask      uint32       `json:"mask"` // the lines' bits in a word; the others are sent as 0
	Active    bool         `json:"active"`
	Why       string       `json:"why,omitempty"` // why the page does not decode, when it does not
}

// ClientChunk is a piece of the stream as the ring holds it: Seq its place
// in the ring's order, Index the stream sample its first word is (the
// scope's decoder index, which the frames posted back are in), Words its
// words and Enc their coding (appendWordsRLE). Break marks words lost
// before it (a worker fault, the decode handed over): the page's decoder
// starts afresh.
type ClientChunk struct {
	Seq   uint64
	Index uint64
	Words int
	Break bool
	Check bool // the scope would decode what it queued after this chunk (softDrain's push check)
	Enc   []byte
	off   int // Enc's place in the ring
}

// ClientWords is a page's read of the ring: the chunks from its Seq on,
// Dropped those it asked for that the ring no longer holds (the page fell
// behind by the ring), Next the Seq to ask for next.
type ClientWords struct {
	Info    ClientStreamInfo
	Chunks  []ClientChunk
	Dropped uint64
	Next    uint64
	Lag     uint64 // stream samples the scope holds that the page has not posted frames for
}

// clientTap is the ring and the page's frames, shared by the acquisition
// owner (who writes the words and takes the frames) and the web's requests.
type clientTap struct {
	mu      sync.Mutex
	lease   time.Time
	info    ClientStreamInfo
	buf     []byte
	chunks  []ClientChunk // oldest first; Enc slices buf
	nextSeq uint64
	notify  chan struct{}
	// The page's frames, in the scope's decoder index, for the owner.
	inbox      []decode.StreamFrame
	inboxSess  uint32
	decoded    uint64 // the stream index the page decoded through
	lastI0     uint64 // the start of the last frame posted
	havePosted bool
	written    uint64 // the stream index after the last word written
}

// LeaseClientDecode holds the client decode for clientLease.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) LeaseClientDecode() {
	t := &e.ctap
	t.mu.Lock()
	t.lease = time.Now()
	t.mu.Unlock()
}

// clientLeased reports a page holding the client-decode lease.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) leased(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.lease.IsZero() && now.Sub(t.lease) < clientLease
}

// waitCh is the channel closed when the ring next changes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) waitCh() chan struct{} {
	if t.notify == nil {
		t.notify = make(chan struct{})
	}
	return t.notify
}

// wake tells the waiting readers the ring changed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) wake() {
	if t.notify != nil {
		close(t.notify)
		t.notify = nil
	}
}

// begin starts a session in the ring: the chunks of an earlier one go.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) begin(info ClientStreamInfo, index uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf == nil {
		t.buf = make([]byte, clientTapBytes)
	}
	t.info = info
	t.chunks = t.chunks[:0]
	t.inbox, t.inboxSess = t.inbox[:0], info.Session
	t.decoded, t.written = index, index
	t.havePosted = false
	t.wake()
}

// end marks the session over: the page stops decoding and its later frames
// are refused.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) end(why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.info.Active, t.info.Why = false, why
	t.inbox, t.inboxSess = t.inbox[:0], 0
	t.wake()
}

// append codes raw (whole words) as the stream's chunk starting at stream
// sample index, dropping the oldest chunks for room.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) append(raw []byte, index uint64, brk, check bool) {
	n := len(raw) / 4
	if n == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf == nil {
		t.buf = make([]byte, clientTapBytes)
	}
	need := rleBound(n)
	if need > len(t.buf) {
		// More than the ring in one piece: in halves, so each fits.
		t.mu.Unlock()
		h := 4 * (n / 2)
		t.append(raw[:h], index, brk, false)
		t.append(raw[h:], index+uint64(16*(n/2)), false, check)
		t.mu.Lock()
		return
	}
	// The chunks lie in buf in order, wrapping to its start: the next one
	// goes after the newest, or at the start when it does not fit there. The
	// chunks ahead of it in buf are the oldest, so room is made from the front.
	at := 0
	if k := len(t.chunks); k > 0 {
		last := t.chunks[k-1]
		at = last.off + len(last.Enc)
		if at+need > len(t.buf) {
			// the tail is given up: the chunks there are the oldest
			for len(t.chunks) > 0 && t.chunks[0].off >= at {
				t.chunks = t.chunks[1:]
			}
			at = 0
		}
	}
	for len(t.chunks) > 0 && (len(t.chunks) >= clientTapChunks || t.overlaps(at, need)) {
		t.chunks = t.chunks[1:]
	}
	enc := appendWordsRLE(t.buf[at:at:at+need], raw, t.info.Mask)
	if t.written != index {
		brk = true
	}
	t.chunks = append(t.chunks, ClientChunk{Seq: t.nextSeq, Index: index, Words: n, Break: brk, Check: check, Enc: enc, off: at})
	t.nextSeq++
	t.written = index + uint64(16*n)
	t.wake()
}

// overlaps reports the oldest chunk lying in buf[at:at+n].
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) overlaps(at, n int) bool {
	c := t.chunks[0]
	return c.off < at+n && at < c.off+len(c.Enc)
}

// read copies the chunks from seq on, up to about max bytes, into dst's
// chunks (their Enc appended to one buffer); it waits up to wait for one
// when none is there.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) read(seq uint64, max int, wait time.Duration, dst []byte) (ClientWords, []byte) {
	deadline := time.Now().Add(wait)
	t.mu.Lock()
	for !t.ready(seq) {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		ch := t.waitCh()
		t.mu.Unlock()
		select {
		case <-ch:
		case <-time.After(left):
		}
		t.mu.Lock()
	}
	defer t.mu.Unlock()
	out := ClientWords{Info: t.info, Next: seq}
	if t.written > t.decoded {
		out.Lag = t.written - t.decoded
	}
	if !t.info.Active {
		return out, dst
	}
	k := 0
	if len(t.chunks) > 0 {
		oldest := t.chunks[0].Seq
		if seq < oldest || seq > t.nextSeq {
			if seq < oldest {
				out.Dropped = oldest - seq
			}
			seq = oldest
		}
		k = int(seq - oldest)
	}
	for ; k < len(t.chunks) && (len(out.Chunks) == 0 || len(dst)+len(t.chunks[k].Enc) <= max); k++ {
		c := t.chunks[k]
		at := len(dst)
		dst = append(dst, c.Enc...)
		c.Enc = dst[at:len(dst):len(dst)]
		out.Chunks = append(out.Chunks, c)
		seq = c.Seq + 1
	}
	out.Next = seq
	return out, dst
}

// ready reports chunks for a read from seq: the ring holds seq or later,
// or seq is not in it (a page behind by the ring, or of another session).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) ready(seq uint64) bool {
	if !t.info.Active || len(t.chunks) == 0 {
		return false
	}
	return t.chunks[len(t.chunks)-1].Seq >= seq || seq < t.chunks[0].Seq || seq > t.nextSeq
}

// post queues a page's frames for the owner, refused for another session.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) post(session uint32, through uint64, frames []decode.StreamFrame) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.info.Active || session != t.inboxSess {
		return fmt.Errorf("no client decode for session %d", session)
	}
	if len(t.inbox)+len(frames) > clientInboxMax {
		return fmt.Errorf("client frames queue full")
	}
	for _, f := range frames {
		if t.havePosted && f.I0 <= t.lastI0 {
			continue // posted before (the page sent it again)
		}
		t.inbox = append(t.inbox, f)
		t.lastI0, t.havePosted = f.I0, true
	}
	t.decoded = max(t.decoded, through)
	return nil
}

// take hands the owner the page's queued frames.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) take(dst []decode.StreamFrame) []decode.StreamFrame {
	t.mu.Lock()
	defer t.mu.Unlock()
	dst = append(dst[:0], t.inbox...)
	clear(t.inbox)
	t.inbox = t.inbox[:0]
	return dst
}

// ReadClientWords is a page's read of the client-decode ring (it holds the
// lease): the chunks from seq, about max bytes, waiting up to wait.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) ReadClientWords(seq uint64, max int, wait time.Duration, dst []byte) (ClientWords, []byte) {
	e.LeaseClientDecode()
	return e.ctap.read(seq, max, wait, dst)
}

// PostClientFrames queues a page's decoded frames (their indices the
// stream's samples, through the index it decoded up to).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) PostClientFrames(session uint32, through uint64, frames []decode.StreamFrame) error {
	e.LeaseClientDecode()
	return e.ctap.post(session, through, frames)
}

// softBudget is the samples a second the digital stream's plan may carry:
// the scope's own decode's, or all of them when a page decodes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softBudget() float64 {
	if e.ctap.leased(time.Now()) {
		return clientBitsBudget
	}
	return softBitsBudget
}

// softClientInfo is the stream as the page decodes it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) softClientInfo() ClientStreamInfo {
	lines := softLines(s.p.Proto)
	mask := uint32(0xFFFF) << (16 * uint(s.p.ChA&1))
	if lines == 2 {
		mask = 0xFFFFFFFF
	}
	return ClientStreamInfo{Session: s.id.Epoch, Proto: s.proto, Params: s.p, PeriodS: s.sampleS, Log: int(s.log), Lines: lines,
		CloseN: s.closeN, MaxN: s.maxN, LeadN: s.leadN, PushN: s.pushN, PushMax: softPushMax,
		KeepWords: s.keepWords, LeadWords: s.leadWords, Subframes: s.proto == "spdif", Mask: mask, Active: true}
}

// softClientMode hands the stream's decode to the page that holds the lease
// or back to the scope, and queues the page's frames as the scope's own.
// It reports a stop on the value (the stream then ends as softPush ends it).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softClientMode(s *softStream) bool {
	want := s.bits && e.ctap.leased(time.Now())
	switch {
	case want && !s.client:
		// What the scope queued is decoded and its whole frames handed out;
		// a frame still open is the page's to decode from its start.
		e.softPush(s)
		if s.phase != softStreaming {
			return true
		}
		s.dec.Skip(16 * s.skipped)
		s.skipped, s.idleWords, s.haveLvl = 0, 0, false
		s.dec.Skip(0)
		s.client = true
		e.ctap.begin(s.softClientInfo(), s.dec.Next())
		e.logf("engine: %s stream: a web page decodes it", s.decoder.Name)
	case !want && s.client:
		e.softClientFrames(s)
		s.client = false
		e.ctap.end("the page stopped asking")
		// The words the page took but did not post frames for are lost.
		s.dec.Skip(0)
		s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventLoss, Sample: s.ord(s.dec.Next()), Count: 1})
		if s.overBudget {
			// A plan only a page keeps up with: decoded here it would fall a
			// ring behind again and again with the CPU the display needs. The
			// stream ends and says why; started again it streams as captures.
			e.softStreamStop(s)
			s.err = fmt.Errorf("the web page decoding this stream went away; start the stream again to decode it on the scope")
			s.phase = softDone
			e.logf("engine: %s stream: %v", s.decoder.Name, s.err)
			return true
		}
		e.logf("engine: %s stream: the web page stopped; the scope decodes it", s.decoder.Name)
		return false
	}
	if s.client {
		return e.softClientFrames(s)
	}
	return false
}

// softClientFrames queues the page's frames; on the stop value the
// transcript ends there and the record is armed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softClientFrames(s *softStream) bool {
	s.clientFrames = e.ctap.take(s.clientFrames)
	for k := range s.clientFrames {
		f := s.clientFrames[k : k+1]
		if len(f[0].Spans) == 1 && f[0].Spans[0].Kind == "loss" {
			// The page lost words (it fell behind by the ring).
			s.losses++
			s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventLoss, Sample: s.ord(f[0].I0), Count: 1})
			continue
		}
		if s.emit(f, s.ord) {
			e.softStreamStop(s)
			s.phase = softRecord
			e.ctap.end("stopped on the value")
			return true
		}
	}
	return false
}

// softClientWords hands the page raw's words: the scope's decoder moves on
// by them without decoding them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (e *Engine) softClientWords(s *softStream, raw []byte, check bool) {
	n := len(raw) / 4
	if n == 0 {
		return
	}
	e.ctap.append(raw[:4*n], s.dec.Next(), s.clientBreak, check)
	s.clientBreak = false
	s.dec.Skip(16 * n)
}

// rleRun is the shortest run of a quiet word the ring codes as a run.
const rleRun = 3

// rleBound is the most bytes appendWordsRLE writes for n words.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func rleBound(n int) int { return 4*n + 4*(n/rleRun+1) + 8 }

// appendWordsRLE appends raw's words (each masked) as tokens: a little-endian
// word k < 2^31 then k words as they are, or 2^31 | k then one word that
// comes k times. Only a quiet word - each line at 0 or 0xFFFF - is coded as
// a run: a bus at rest costs eight bytes, a busy one its words.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func appendWordsRLE(dst, raw []byte, mask uint32) []byte {
	n := len(raw) / 4
	at := len(dst)
	dst = slices.Grow(dst, rleBound(n))[:at+rleBound(n)]
	if n == 0 {
		return dst[:at]
	}
	src, out := words32(raw[:4*n]), words32(dst[at:])
	if src == nil || out == nil {
		return rleBytes(dst, at, raw[:4*n], mask)
	}
	// The words in place, as the device's ring holds them (aligned, the
	// device little-endian like the page): a word costs a load, a mask and
	// two compares, where the bytes' path costs it as much again.
	o := 0
	lit := -1 // the open literal's token
	for i := 0; i < n; {
		w := src[i] & mask
		if uint16(w+1) <= 1 && uint16(w>>16+1) <= 1 {
			j := i + 1
			for j < n && src[j]&mask == w {
				j++
			}
			if j-i >= rleRun {
				if lit >= 0 {
					out[lit] = uint32(o - lit - 1)
					lit = -1
				}
				out[o], out[o+1] = 1<<31|uint32(j-i), w
				o += 2
				i = j
				continue
			}
		}
		if lit < 0 {
			lit = o
			o++
		}
		out[o] = w
		o++
		i++
	}
	if lit >= 0 {
		out[lit] = uint32(o - lit - 1)
	}
	return dst[:at+4*o]
}

// words32 is b's words in place, nil where they are not aligned or the
// machine is not little-endian.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func words32(b []byte) []uint32 {
	if len(b) < 4 || uintptr(unsafe.Pointer(unsafe.SliceData(b)))%4 != 0 || !littleEndian {
		return nil
	}
	return unsafe.Slice((*uint32)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/4)
}

// littleEndian is the machine's byte order (the device's and the PC's).
var littleEndian = func() bool {
	x := uint16(1)
	return *(*byte)(unsafe.Pointer(&x)) == 1
}()

// rleBytes is appendWordsRLE a byte at a time (dst grown, its tokens from
// at), for words that are not aligned.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func rleBytes(dst []byte, at int, raw []byte, mask uint32) []byte {
	n := len(raw) / 4
	o := at
	lit := -1
	litN := uint32(0)
	for i := 0; i < n; {
		w := binary.LittleEndian.Uint32(raw[4*i:]) & mask
		if uint16(w+1) <= 1 && uint16(w>>16+1) <= 1 {
			j := i + 1
			for j < n && binary.LittleEndian.Uint32(raw[4*j:])&mask == w {
				j++
			}
			if j-i >= rleRun {
				if lit >= 0 {
					binary.LittleEndian.PutUint32(dst[lit:], litN)
					lit = -1
				}
				binary.LittleEndian.PutUint32(dst[o:], 1<<31|uint32(j-i))
				binary.LittleEndian.PutUint32(dst[o+4:], w)
				o += 8
				i = j
				continue
			}
		}
		if lit < 0 {
			lit, litN = o, 0
			o += 4
		}
		binary.LittleEndian.PutUint32(dst[o:], w)
		o += 4
		litN++
		i++
	}
	if lit >= 0 {
		binary.LittleEndian.PutUint32(dst[lit:], litN)
	}
	return dst[:o]
}

// ClientChunkHead is a chunk's head in a /api/stream/words.bin reply: its
// first word's stream index (two words, low first), its words, its flags
// (1: words lost before it, 2: the scope checks its pushes after it) and its
// coded bytes, each a little-endian word.
const ClientChunkHead = 20

// AppendClientChunkHeads appends the chunks' heads; their coded words follow
// them in a reply, in the same order.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func AppendClientChunkHeads(dst []byte, chunks []ClientChunk) []byte {
	for _, c := range chunks {
		flags := uint32(0)
		if c.Break {
			flags |= 1
		}
		if c.Check {
			flags |= 2
		}
		dst = binary.LittleEndian.AppendUint32(dst, uint32(c.Index))
		dst = binary.LittleEndian.AppendUint32(dst, uint32(c.Index>>32))
		dst = binary.LittleEndian.AppendUint32(dst, uint32(c.Words))
		dst = binary.LittleEndian.AppendUint32(dst, flags)
		dst = binary.LittleEndian.AppendUint32(dst, uint32(len(c.Enc)))
	}
	return dst
}

// clientFramesMagic opens a page's frames (webdec.js webdecFramesBody).
var clientFramesMagic = [4]byte{'S', 'D', 'F', '1'}

// clientSpanKinds are the span kinds a page's frames carry, by their code:
// the ones the transcript keeps (emit); any other kind as a frame's first
// span, which decides its line; a loss as a frame's only span.
var clientSpanKinds = []string{"data", "frame-error", "parity-error", "gap", "sof", "other", "loss"}

// errShortFrames is a page's frames cut short.
var errShortFrames = errors.New("short client frames")

// ParseClientFrames reads a page's frames: "SDF1", the session, the stream
// index it decoded through (two words, low first) and the frames' count,
// then each frame - its first and last sample (two words each), its spans'
// count (16 bits), its start text's length (8 bits, then a byte) and text -
// and its spans: the first sample (two words), the value (32 bits) and the
// kind (8 bits, clientSpanKinds). All little-endian.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func ParseClientFrames(b []byte) (session uint32, through uint64, frames []decode.StreamFrame, err error) {
	if len(b) < 20 || [4]byte(b[:4]) != clientFramesMagic {
		return 0, 0, nil, fmt.Errorf("not a client frames body")
	}
	at := 4
	u32 := func() uint32 {
		v := binary.LittleEndian.Uint32(b[at:])
		at += 4
		return v
	}
	u64 := func() uint64 {
		lo := u32()
		return uint64(lo) | uint64(u32())<<32
	}
	session, through = u32(), u64()
	n := int(u32())
	if n > len(b)/20 {
		return 0, 0, nil, fmt.Errorf("%d frames in %d bytes", n, len(b))
	}
	frames = make([]decode.StreamFrame, 0, n)
	var spans []decode.StreamSpan
	for range n {
		if at+20 > len(b) {
			return 0, 0, nil, errShortFrames
		}
		f := decode.StreamFrame{I0: u64(), I1: u64()}
		ns, tl := int(binary.LittleEndian.Uint16(b[at:])), int(b[at+2])
		at += 4
		if at+tl+13*ns > len(b) {
			return 0, 0, nil, errShortFrames
		}
		if f.I1 < f.I0 {
			return 0, 0, nil, fmt.Errorf("a frame ending before it starts")
		}
		text := string(b[at : at+tl])
		at += tl
		from := len(spans)
		for k := range ns {
			sp := decode.StreamSpan{I0: u64(), Val: int(int32(u32()))}
			kind := int(b[at])
			at++
			if kind >= len(clientSpanKinds) {
				return 0, 0, nil, fmt.Errorf("span kind %d", kind)
			}
			sp.Kind, sp.I1 = clientSpanKinds[kind], sp.I0
			if k == 0 && sp.Kind == "sof" {
				sp.Text = text
			}
			spans = append(spans, sp)
		}
		f.Spans = spans[from:len(spans):len(spans)]
		frames = append(frames, f)
	}
	return session, through, frames, nil
}
