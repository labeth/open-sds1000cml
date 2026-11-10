// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package streams

import (
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"github.com/labeth/open-dso-core/decodedlines"
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
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
	// pins are the chunk ranges being written to a page straight from buf:
	// the owner does not overwrite them (it drops its newest words instead,
	// which the page then reads as lost).
	pins    [][2]uint64
	dropped int // chunks the owner dropped for a pin
	// The page's frames, in the scope's decoder index, for the owner.
	frames     []core.ClientFrame
	spans      []core.ClientSpan
	inboxSess  uint32
	decoded    uint64 // the stream index the page decoded through
	lastI0     uint64 // the start of the last frame posted
	havePosted bool
	written    uint64 // the stream index after the last word written
}

// LeaseClientDecode holds the client decode for clientLease.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) LeaseClientDecode() {
	t := &d.ctap
	t.mu.Lock()
	t.lease = time.Now()
	t.mu.Unlock()
}

// clientLeased reports a page holding the client-decode lease.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) leased(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.lease.IsZero() && now.Sub(t.lease) < core.ClientLease
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
		t.buf = make([]byte, core.ClientTapBytes)
	}
	t.info = info
	t.chunks = t.chunks[:0]
	t.frames, t.spans, t.inboxSess = t.frames[:0], t.spans[:0], info.Session
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
	t.frames, t.spans, t.inboxSess = t.frames[:0], t.spans[:0], 0
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
		t.buf = make([]byte, core.ClientTapBytes)
	}
	need := core.RLEBound(n)
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
		at = last.Off + len(last.Enc)
		if at+need > len(t.buf) {
			// the tail is given up: the chunks there are the oldest
			for len(t.chunks) > 0 && t.chunks[0].Off >= at {
				if t.pinned(t.chunks[0].Seq) {
					t.dropped++
					return
				}
				t.chunks = t.chunks[1:]
			}
			at = 0
		}
	}
	for len(t.chunks) > 0 && (len(t.chunks) >= core.ClientTapChunks || t.overlaps(at, need)) {
		if t.pinned(t.chunks[0].Seq) {
			// A page's read of the oldest words is under way: these words
			// go instead (the next chunk marks the loss).
			t.dropped++
			return
		}
		t.chunks = t.chunks[1:]
	}
	enc := core.AppendWordsRLE(t.buf[at:at:at+need], raw, t.info.Mask)
	if t.written != index {
		brk = true
	}
	t.chunks = append(t.chunks, ClientChunk{Seq: t.nextSeq, Index: index, Words: n, Break: brk, Check: check, Enc: enc, Off: at})
	t.nextSeq++
	t.written = index + uint64(16*n)
	t.wake()
}

// overlaps reports the oldest chunk lying in buf[at:at+n].
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) overlaps(at, n int) bool {
	c := t.chunks[0]
	return c.Off < at+n && at < c.Off+len(c.Enc)
}

// read hands out the chunks from seq on, up to about limit bytes, their Enc
// in place in the ring and pinned there until release is called; it waits
// up to wait for one when none is there.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) read(seq uint64, limit int, wait time.Duration, dst []ClientChunk) (out ClientWords, release func()) {
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
	out = ClientWords{Info: t.info, Next: seq, Chunks: dst[:0]}
	if t.written > t.decoded {
		out.Lag = t.written - t.decoded
	}
	release = func() {}
	if !t.info.Active {
		return out, release
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
		k = int(seq - oldest) //nolint:gosec // G115: seq lies within the ring held, a few thousand chunks
	}
	size := 0
	limit = min(limit, ClientReplyMax)
	for ; k < len(t.chunks) && (len(out.Chunks) == 0 || size+len(t.chunks[k].Enc) <= limit); k++ {
		c := t.chunks[k]
		size += len(c.Enc)
		out.Chunks = append(out.Chunks, c)
		seq = c.Seq + 1
	}
	out.Next = seq
	if len(out.Chunks) > 0 {
		pin := [2]uint64{out.Chunks[0].Seq, seq - 1}
		t.pins = append(t.pins, pin)
		release = func() { t.unpin(pin) }
	}
	return out, release
}

// pinned reports a chunk a page's read is writing from the ring.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) pinned(seq uint64) bool {
	for _, p := range t.pins {
		if seq >= p[0] && seq <= p[1] {
			return true
		}
	}
	return false
}

// unpin ends a read's pin.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) unpin(pin [2]uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, p := range t.pins {
		if p == pin {
			t.pins = append(t.pins[:i], t.pins[i+1:]...)
			return
		}
	}
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

// post queues a page's frames (a /api/stream/frames.bin body,
// webdec.js webdecFramesBody) for the owner, read straight into the queue:
// "SDF1", the session, the stream index the page decoded through (two
// words, low first) and the frames' count, then each frame - its first and
// last sample (two words each), its spans' count (16 bits), its start
// text's length (8 bits, then a byte) and text - and its spans: the first
// sample (two words), the value (32 bits) and the kind (8 bits,
// clientSpanKinds). All little-endian. A body that does not read whole
// leaves the queue as it was; one of another session is refused.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) post(b []byte) error {
	if len(b) < 20 || [4]byte(b[:4]) != core.ClientFramesMagic {
		return core.ErrNotFrames
	}
	session := binary.LittleEndian.Uint32(b[4:])
	through := binary.LittleEndian.Uint64(b[8:])
	n := int(binary.LittleEndian.Uint32(b[16:]))
	if n > len(b)/20 {
		return core.ErrNotFrames
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.info.Active || session != t.inboxSess {
		return core.ErrNoClientSession
	}
	nf, ns, lastI0, have := len(t.frames), len(t.spans), t.lastI0, t.havePosted
	undo := func(err error) error {
		t.frames, t.spans, t.lastI0, t.havePosted = t.frames[:nf], t.spans[:ns], lastI0, have
		return err
	}
	at := 20
	for range n {
		if at+20 > len(b) {
			return undo(core.ErrShortFrames)
		}
		f := core.ClientFrame{I0: binary.LittleEndian.Uint64(b[at:]), I1: binary.LittleEndian.Uint64(b[at+8:])}
		cnt, tl := int(binary.LittleEndian.Uint16(b[at+16:])), int(b[at+18])
		at += 20
		if at+tl+13*cnt > len(b) {
			return undo(core.ErrShortFrames)
		}
		if f.I1 < f.I0 {
			return undo(core.ErrNotFrames)
		}
		text := b[at : at+tl]
		at += tl
		f.From = int32(len(t.spans)) //nolint:gosec // G115: a frame list holds far fewer than 2^31 spans
		for k := range cnt {
			kind := b[at+12]
			if int(kind) >= len(core.ClientSpanKinds) {
				return undo(core.ErrNotFrames)
			}
			switch {
			case kind <= 3:
				t.spans = append(t.spans, core.ClientSpan{I0: binary.LittleEndian.Uint64(b[at:]), Val: int32(binary.LittleEndian.Uint32(b[at+8:])), Kind: kind}) //nolint:gosec // G115: the two's-complement value the page sent
			case k == 0 && kind == 4:
				f.Start = decodedlines.StartNoteCodeBytes(text)
			case k == 0 && kind == 6:
				f.Loss = true
			}
			at += 13
		}
		f.To = int32(len(t.spans)) //nolint:gosec // G115: a frame list holds far fewer than 2^31 spans
		if t.havePosted && f.I0 <= t.lastI0 {
			t.spans = t.spans[:f.From] // posted before (the page sent it again)
			continue
		}
		if len(t.frames) >= core.ClientInboxMax {
			return undo(core.ErrClientQueueFull)
		}
		t.frames = append(t.frames, f)
		t.lastI0, t.havePosted = f.I0, true
	}
	t.decoded = max(t.decoded, through)
	return nil
}

// take hands the owner the page's queued frames, swapping its emptied
// queue in for them: no copy, nothing made.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (t *clientTap) take(frames []core.ClientFrame, spans []core.ClientSpan) ([]core.ClientFrame, []core.ClientSpan) {
	t.mu.Lock()
	defer t.mu.Unlock()
	f, sp := t.frames, t.spans
	t.frames, t.spans = frames[:0], spans[:0]
	return f, sp
}

// ReadClientWords is a page's read of the client-decode ring (it holds the
// lease): the chunks from seq, about limit bytes, waiting up to wait. Their
// Enc lie in the ring, pinned until release is called (after they are
// written to the page).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) ReadClientWords(seq uint64, limit int, wait time.Duration, dst []ClientChunk) (ClientWords, func()) {
	d.LeaseClientDecode()
	return d.ctap.read(seq, limit, wait, dst)
}

// PostClientFrames queues a page's decoded frames, its
// /api/stream/frames.bin body (clientTap.post); b is not kept.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) PostClientFrames(b []byte) error {
	d.LeaseClientDecode()
	return d.ctap.post(b)
}

// softBudget is the samples a second the digital stream's plan may carry:
// the scope's own decode's, or all of them when a page decodes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) softBudget() float64 {
	if d.ctap.leased(time.Now()) {
		return core.ClientBitsBudget
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
	return ClientStreamInfo{
		Session: s.id.Epoch, Proto: s.proto, Params: s.p, PeriodS: s.sampleS, Log: int(s.log), Lines: lines,
		CloseN: s.closeN, MaxN: s.maxN, LeadN: s.leadN, PushN: s.pushN, PushMax: softPushMax,
		KeepWords: s.keepWords, LeadWords: s.leadWords, Subframes: s.subframes, Mask: mask, Active: true,
	}
}

// softClientMode hands the stream's decode to the page that holds the lease
// or back to the scope, and queues the page's frames as the scope's own.
// It reports a stop on the value (the stream then ends as softPush ends it).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) softClientMode(s *softStream) bool {
	want := s.bits && d.ctap.leased(time.Now())
	switch {
	case want && !s.client:
		// What the scope queued is decoded and its whole frames handed out;
		// a frame still open is the page's to decode from its start.
		d.softPush(s)
		if s.phase != softStreaming {
			return true
		}
		s.dec.Skip(16 * s.skipped)
		s.skipped, s.idleWords, s.haveLvl = 0, 0, false
		s.dec.Skip(0)
		s.client = true
		d.ctap.begin(s.softClientInfo(), s.dec.Next())
		d.h.logf("engine: %s stream: a web page decodes it", s.decoder.Name)
	case !want && s.client:
		d.softClientFrames(s)
		s.client = false
		d.ctap.end("the page stopped asking")
		// The words the page took but did not post frames for are lost,
		// and the scope's decoding takes up the line where it is.
		s.dec.Skip(0)
		s.dec.CutStart(s.dec.Next())
		s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventLoss, Sample: s.ord(s.dec.Next()), Count: 1})
		if s.overBudget {
			// A plan only a page keeps up with: decoded here it would fall a
			// ring behind again and again with the CPU the display needs. The
			// stream ends and says why; started again it streams as captures.
			d.softStreamStop(s)
			s.err = errors.New("the web page decoding this stream went away; start the stream again to decode it on the scope")
			s.phase = softDone
			d.h.logf("engine: %s stream: %v", s.decoder.Name, s.err)
			return true
		}
		d.h.logf("engine: %s stream: the web page stopped; the scope decodes it", s.decoder.Name)
		return false
	}
	if s.client {
		return d.softClientFrames(s)
	}
	return false
}

// softClientFrames queues the page's frames; on the stop value the
// transcript ends there and the stop is the scope's own (softStopOnValue).
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) softClientFrames(s *softStream) bool {
	s.cliFrames, s.cliSpans = d.ctap.take(s.cliFrames, s.cliSpans)
	for _, f := range s.cliFrames {
		if f.Loss {
			// The page lost words (it fell behind by the ring).
			s.losses++
			s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventLoss, Sample: s.ord(f.I0), Count: 1})
			continue
		}
		if s.emitClient(f, s.cliSpans[f.From:f.To]) {
			// As when the scope decodes: the history ring frozen around the
			// frame, or the symbol trigger's record.
			d.softStopOnValue(s)
			d.ctap.end("stopped on the value")
			return true
		}
	}
	return false
}

// emitClient is emit of one of the page's frames (its start of frame's
// code and the spans the transcript keeps), event for event.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (s *softStream) emitClient(f core.ClientFrame, spans []core.ClientSpan) bool {
	s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventStart, Sample: s.ord(f.I0), Value: f.Start})
	hit, unit := false, 0
	for _, sp := range spans {
		if sp.Kind != 0 {
			s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventError, Sample: s.ord(sp.I0)})
			continue
		}
		v := int(sp.Val) & s.mask
		s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventData, Sample: s.ord(sp.I0), Value: uint32(v), Valid: true}) //nolint:gosec // G115: v is masked to the unit width
		if !hit && s.trig == StreamOnValue && v == s.value {
			s.valueRead = true
			if !s.noStop {
				hit, s.stopUnit = true, unit
			}
		}
		unit++
	}
	s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventEnd, Sample: s.ord(f.I1)})
	if !hit {
		return false
	}
	// The frame's line starts at its named start or, unnamed, at its first
	// unit (emit).
	s.stopFirst, s.stopLast = s.ord(f.I0), s.ord(f.I1)
	if f.Start == 0 && len(spans) > 0 {
		s.stopFirst = s.ord(spans[0].I0)
	}
	s.addEvent(sramcapture.DecodedEvent{Kind: sramcapture.EventTrigger, Sample: s.ord(f.I0)})
	return true
}

// softClientWords hands the page raw's words: the scope's decoder moves on
// by them without decoding them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func (d *DecodedStreams) softClientWords(s *softStream, raw []byte, check bool) {
	n := len(raw) / 4
	if n == 0 {
		return
	}
	d.ctap.append(raw[:4*n], s.dec.Next(), s.clientBreak, check)
	s.clientBreak = false
	s.dec.Skip(16 * n)
}
