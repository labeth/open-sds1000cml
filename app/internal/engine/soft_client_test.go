// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/testenv"
)

// rleWords undoes appendWordsRLE.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func rleWords(t *testing.T, enc []byte) []uint32 {
	t.Helper()
	var out []uint32
	for i := 0; i < len(enc); {
		k := binary.LittleEndian.Uint32(enc[i:])
		i += 4
		if k&(1<<31) != 0 {
			w := binary.LittleEndian.Uint32(enc[i:])
			i += 4
			for range k &^ (1 << 31) {
				out = append(out, w)
			}
			continue
		}
		for range k {
			out = append(out, binary.LittleEndian.Uint32(enc[i:]))
			i += 4
		}
	}
	return out
}

// The ring's coding gives back every word, masked to the lines the page
// decodes, in no more than rleBound bytes, and a quiet line in a few bytes.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestAppendWordsRLE(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for it := 0; it < 300; it++ {
		n := rng.Intn(2000)
		raw := make([]byte, 4*n)
		for i := 0; i < n; {
			// runs of quiet words, of one busy word, and of noise
			w := []uint32{0, 0xFFFFFFFF, 0xFFFF, 0xFFFF0000, rng.Uint32(), rng.Uint32() & 0xFFFF}[rng.Intn(6)]
			for k := 1 + rng.Intn(40); k > 0 && i < n; k-- {
				binary.LittleEndian.PutUint32(raw[4*i:], w)
				i++
			}
		}
		mask := []uint32{0xFFFFFFFF, 0xFFFF, 0xFFFF0000}[it%3]
		// in place (aligned) and a byte at a time (not)
		off := 4 * (it % 2)
		if it%4 == 1 {
			off = 3
		}
		enc := appendWordsRLE(make([]byte, off, 8), raw, mask)[off:]
		if len(enc) > rleBound(n) {
			t.Fatalf("%d words coded in %d bytes, bound %d", n, len(enc), rleBound(n))
		}
		got := rleWords(t, enc)
		if len(got) != n {
			t.Fatalf("%d words back, want %d", len(got), n)
		}
		for i, w := range got {
			if want := binary.LittleEndian.Uint32(raw[4*i:]) & mask; w != want {
				t.Fatalf("word %d: %08x, want %08x", i, w, want)
			}
		}
	}
	quiet := make([]byte, 4*65536)
	if enc := appendWordsRLE(nil, quiet, 0xFFFF); len(enc) != 8 {
		t.Fatalf("a quiet second coded in %d bytes", len(enc))
	}
}

// The ring hands a page its chunks in order and whole; one that fell behind
// by the ring is told how many chunks it lost and goes on from the oldest;
// a read waits for words and returns at once when they come.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestClientTapRing(t *testing.T) {
	var tp clientTap
	tp.begin(ClientStreamInfo{Session: 9, Mask: 0xFFFFFFFF, Active: true}, 160)
	rng := rand.New(rand.NewSource(1))
	index := uint64(160)
	var sent [][]uint32
	for k := 0; k < 400; k++ {
		n := 1 + rng.Intn(16384)
		raw := make([]byte, 4*n)
		for i := range raw {
			raw[i] = byte(rng.Intn(256))
		}
		tp.append(raw, index, false, true)
		index += uint64(16 * n)
		w := make([]uint32, n)
		for i := range w {
			w[i] = binary.LittleEndian.Uint32(raw[4*i:])
		}
		sent = append(sent, w)
	}
	cw, enc := tp.read(0, 1<<30, 0, nil)
	if cw.Dropped == 0 || len(cw.Chunks) == 0 || cw.Chunks[0].Seq != cw.Dropped {
		t.Fatalf("a page behind by the ring: dropped %d, first chunk %+v", cw.Dropped, cw.Chunks[0].Seq)
	}
	total := 0
	for k, c := range cw.Chunks {
		total += len(c.Enc)
		if got, want := rleWords(t, c.Enc), sent[c.Seq]; !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d (seq %d) differs", k, c.Seq)
		}
		if k > 0 && c.Index != cw.Chunks[k-1].Index+uint64(16*cw.Chunks[k-1].Words) {
			t.Fatalf("chunk %d index %d", k, c.Index)
		}
	}
	if total > clientTapBytes || total < clientTapBytes/2 || len(enc) != total {
		t.Fatalf("the ring holds %d bytes", total)
	}
	if cw.Next != 400 {
		t.Fatalf("next %d", cw.Next)
	}
	// a read with nothing new waits, and wakes for the next chunk
	go func() {
		time.Sleep(50 * time.Millisecond)
		tp.append(make([]byte, 64), index, false, true)
	}()
	t0 := time.Now()
	cw, _ = tp.read(400, 1<<20, 2*time.Second, nil)
	if len(cw.Chunks) != 1 || time.Since(t0) > time.Second {
		t.Fatalf("waited %v for %d chunks", time.Since(t0), len(cw.Chunks))
	}
	// frames of another session are refused
	if err := tp.post(8, 0, nil); err == nil {
		t.Fatalf("frames of another session taken")
	}
}

// clientCase is one stream decoded by the scope and by the page.
type clientCase struct {
	name  string
	proto int
	p     SerialParams
	raw   []byte
}

// clientTestStream is a gap-free digital stream of proto as the engine makes
// it, its pushes every pushN samples.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func clientTestStream(t *testing.T, proto int, p SerialParams, pushN int) *softStream {
	t.Helper()
	name, _ := softProtoName(proto)
	log, bits, ok := softGapFreePlan(name, p.Baud, false, softLines(proto))
	if !ok || !bits {
		log = decode.StreamGapFreeMinLog
	}
	_, longest, quiet, _ := decode.StreamTiming(name, float64(p.Baud))
	d, _ := serialDecoder(proto)
	s := &softStream{p: p, proto: name, decoder: d, mask: unitMask(p), gapFree: true, quiet: quiet, log: uint8(log), bits: true,
		trig: StreamManual, id: sramcapture.RecordIdentity{Epoch: softEpochBase | 7}}
	s.sampleS, s.ordStep = decode.StreamBitsPeriod(log), float64(uint64(1)<<(log-4))
	s.newDecoder(int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
	lead := int(decode.StreamLead(name, float64(p.Baud)) / s.sampleS)
	s.setLead(lead)
	s.leadWords = lead/16 + 2
	s.keepWords = int(quiet/s.sampleS)/16 + s.leadWords + 2
	s.pushN = pushN
	return s
}

// clientPieces are the pieces the stream arrives in (words) and whether the
// scope checks its pushes after each.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func clientPieces(n int, seed int64) (cuts []int, checks []bool) {
	rng := rand.New(rand.NewSource(seed))
	for at := 0; at < n; {
		k := min(n-at, 1+rng.Intn(3000))
		at += k
		cuts = append(cuts, at)
		checks = append(checks, rng.Intn(3) > 0)
	}
	return cuts, checks
}

// clientWordsReply is a /api/stream/words.bin reply as the web serves it.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func clientWordsReply(cw ClientWords, enc []byte) []byte {
	hj, _ := json.Marshal(struct {
		ClientStreamInfo
		Next   uint64 `json:"next"`
		Chunks int    `json:"chunks"`
	}{cw.Info, cw.Next, len(cw.Chunks)})
	for len(hj)%4 != 0 {
		hj = append(hj, ' ')
	}
	out := append([]byte("SDW1"), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(hj)))
	out = append(out, hj...)
	out = AppendClientChunkHeads(out, cw.Chunks)
	return append(out, enc...)
}

// A page that decodes the stream (webdec.js, as the web worker runs it)
// gives the scope the very events its own decode gives: for every protocol
// on the digital stream, one line and two, thresholds off the middle, line
// B's level set or learned, the words in pieces of any size coded in the
// scope's ring, read in replies of any size, the frames posted back in the
// page's binary form and queued by the scope; LIN's real frames among them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestClientDecodeMatchesScope(t *testing.T) {
	testenv.NeedNode(t)
	withBitsImage(t, func() {
		rng := rand.New(rand.NewSource(11))
		var cases []clientCase
		protos := make([]int, 0, len(softProtoNames))
		for proto := range softProtoNames {
			protos = append(protos, proto)
		}
		sort.Ints(protos)
		for _, proto := range protos {
			name := softProtoNames[proto]
			if !decode.StreamBitsLine(name) {
				continue
			}
			for k, thr := range []float64{90, 128, 200} {
				p := SerialParams{Proto: proto, Threshold: thr, HaveThr: true, ChA: k & 1}
				if k == 1 {
					p.ThresholdB, p.HaveThrB = 160, true
				}
				feature, _, _, _ := decode.StreamTiming(name, 0)
				log, ok := decode.StreamBitsLog(feature)
				if !ok {
					log = decode.StreamGapFreeMinLog
				}
				run := max(2, int(feature/decode.StreamBitsPeriod(log)))
				la := burstLevels(rng, 1<<18, 1, run)
				lb := burstLevels(rng, 1<<18, byte(k&1), run)
				cases = append(cases, clientCase{fmt.Sprintf("%s-%v", name, thr), proto, p, linesWords(la, lb)})
			}
		}
		const baud = 19200
		log, _, _ := softGapFreePlan("lin", baud, false, 1)
		var frames [][]int
		for i := 0; i < 30; i++ {
			frames = append(frames, []int{0x80 | i, i, 0x55, 0})
		}
		cases = append(cases, clientCase{"lin-frames", serLIN, SerialParams{Proto: serLIN, Baud: baud, Threshold: 90, HaveThr: true},
			bitsWords(linLevels(13/decode.StreamBitsPeriod(log), baud, frames), 13)})

		dir := t.TempDir()
		type nodeCase struct {
			Name    string   `json:"name"`
			Replies []string `json:"replies"`
			Out     string   `json:"out"`
		}
		var ncs []nodeCase
		want := map[string][]sramcapture.DecodedEvent{}
		streams := map[string]*softStream{}
		engines := map[string]*Engine{}
		for ci, c := range cases {
			pushN := 1 << (10 + ci%9)
			cuts, checks := clientPieces(len(c.raw)/4, int64(ci))
			// the scope decodes
			ref := clientTestStream(t, c.proto, c.p, pushN)
			e := &Engine{}
			from := 0
			for k, to := range cuts {
				e.feedBitsQuiet(ref, c.raw[4*from:4*to])
				if checks[k] && ref.pending() >= ref.pushN {
					e.softPush(ref)
				}
				from = to
			}
			e.softPush(ref)
			ref.emit(ref.dec.Flush(), ref.ord)
			want[c.name] = ref.events
			// the page decodes
			s := clientTestStream(t, c.proto, c.p, pushN)
			ec := &Engine{}
			s.client = true
			ec.ctap.begin(s.softClientInfo(), s.dec.Next())
			nc := nodeCase{Name: c.name, Out: filepath.Join(dir, fmt.Sprintf("%d.frames", ci))}
			seq := uint64(0)
			from = 0
			for k, to := range cuts {
				ec.softClientWords(s, c.raw[4*from:4*to], checks[k])
				from = to
				if k%7 == 6 || k == len(cuts)-1 {
					for {
						cw, enc := ec.ctap.read(seq, 1+rng.Intn(20000), 0, nil)
						if cw.Dropped != 0 {
							t.Fatalf("%s: the ring dropped %d chunks", c.name, cw.Dropped)
						}
						if len(cw.Chunks) == 0 {
							break
						}
						f := filepath.Join(dir, fmt.Sprintf("%d-%d.bin", ci, len(nc.Replies)))
						if err := os.WriteFile(f, clientWordsReply(cw, enc), 0o644); err != nil {
							t.Fatal(err)
						}
						nc.Replies = append(nc.Replies, f)
						seq = cw.Next
					}
				}
			}
			ncs = append(ncs, nc)
			streams[c.name], engines[c.name] = s, ec
		}
		cj := filepath.Join(dir, "cases.json")
		blob, _ := json.Marshal(ncs)
		if err := os.WriteFile(cj, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("node", "../web/webdec_check.mjs", "session", cj).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "WEBDEC OK") {
			t.Fatalf("webdec_check.mjs: %v\n%s", err, out)
		}
		n := 0
		for _, nc := range ncs {
			body, err := os.ReadFile(nc.Out)
			if err != nil {
				t.Fatal(err)
			}
			session, through, frames, err := ParseClientFrames(body)
			if err != nil {
				t.Fatalf("%s: %v", nc.Name, err)
			}
			s, ec := streams[nc.Name], engines[nc.Name]
			if err := ec.PostClientFrames(session, through, frames); err != nil {
				t.Fatalf("%s: %v", nc.Name, err)
			}
			ec.softClientFrames(s)
			if through != s.dec.Next() {
				t.Errorf("%s: the page decoded through %d, the scope's stream is at %d", nc.Name, through, s.dec.Next())
			}
			if !reflect.DeepEqual(s.events, want[nc.Name]) {
				t.Errorf("%s: %d events from the page, %d from the scope", nc.Name, len(s.events), len(want[nc.Name]))
				continue
			}
			n += len(s.events)
		}
		t.Logf("%d streams, %d events alike", len(ncs), n)
		if n < 1000 {
			t.Fatalf("only %d events", n)
		}
	})
}

// A page that takes the lease takes the decode from the scope at the next
// step: the scope decodes what it queued, then only rings the words, and
// the page's frames are its transcript, a stop on the value included; a
// page that stops asking hands the decode back with a loss mark. The lease
// also lets a stream's plan run the digital stream past the scope's budget.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestClientDecodeHandover(t *testing.T) {
	withBitsImage(t, func() {
		if _, bits, _ := softGapFreePlanBudget("ws281x", 0, false, 1, softBitsBudget); bits {
			t.Fatalf("WS281x streams digital words within the scope's budget")
		}
		if _, bits, ok := softGapFreePlanBudget("ws281x", 0, false, 1, clientBitsBudget); !bits || !ok {
			t.Fatalf("WS281x does not stream gap-free when a page decodes")
		}
		e := &Engine{logf: t.Logf}
		if e.softBudget() != softBitsBudget {
			t.Fatalf("a budget for a page that never asked")
		}
		p := SerialParams{Proto: serLIN, Baud: 19200, Threshold: 90, HaveThr: true}
		s := clientTestStream(t, serLIN, p, 1<<12)
		s.trig, s.value = StreamOnValue, 0x55
		quiet := make([]byte, 4*4096)
		for i := range quiet {
			quiet[i] = 0xFF
		}
		e.softIn(s, quiet)
		if e.softClientMode(s) || s.client {
			t.Fatalf("the page decodes without the lease")
		}
		e.LeaseClientDecode()
		if e.softBudget() != clientBitsBudget {
			t.Fatalf("no budget for a page that holds the lease")
		}
		if e.softClientMode(s); !s.client {
			t.Fatalf("the page does not take the decode")
		}
		at := s.dec.Next()
		e.softIn(s, quiet)
		cw, _ := e.ReadClientWords(0, 1<<20, 0, nil)
		if !cw.Info.Active || cw.Info.Proto != "lin" || len(cw.Chunks) != 1 || cw.Chunks[0].Index != at || cw.Chunks[0].Words != 4096 {
			t.Fatalf("the page's read: %+v", cw)
		}
		if s.dec.Next() != at+16*4096 || s.pending() != 0 {
			t.Fatalf("the scope's decoder at %d (%d queued), want %d", s.dec.Next(), s.pending(), at+16*4096)
		}
		// the page's frames: a byte, then the value
		f := []decode.StreamFrame{
			{I0: at + 10, I1: at + 20, Spans: []decode.StreamSpan{{I0: at + 10, Kind: "sof", Text: "BRK"}, {I0: at + 12, Kind: "data", Val: 0x12}}},
			{I0: at + 30, I1: at + 40, Spans: []decode.StreamSpan{{I0: at + 31, Kind: "loss"}}},
			{I0: at + 50, I1: at + 60, Spans: []decode.StreamSpan{{I0: at + 50, Kind: "sof", Text: "BRK"}, {I0: at + 52, Kind: "data", Val: 0x55}}},
		}
		if err := e.PostClientFrames(s.id.Epoch, at+16*4096, f); err != nil {
			t.Fatal(err)
		}
		if !e.softClientMode(s) || s.phase != softRecord {
			t.Fatalf("the value in the page's frames does not stop the stream")
		}
		var kinds []uint8
		for _, ev := range s.events {
			kinds = append(kinds, ev.Kind)
		}
		want := []uint8{sramcapture.EventStart, sramcapture.EventData, sramcapture.EventEnd, sramcapture.EventLoss,
			sramcapture.EventStart, sramcapture.EventData, sramcapture.EventEnd, sramcapture.EventTrigger}
		if !reflect.DeepEqual(kinds, want) {
			t.Fatalf("events %v, want %v", kinds, want)
		}
		if cw, _ := e.ReadClientWords(cw.Next, 1<<20, 0, nil); cw.Info.Active {
			t.Fatalf("the page still decodes a stream that stopped")
		}
		// a page that stops asking hands the decode back
		s2 := clientTestStream(t, serLIN, p, 1<<12)
		s2.id.Epoch++
		e.softClientMode(s2)
		if !s2.client {
			t.Fatalf("the page does not take the next stream")
		}
		e.ctap.mu.Lock()
		e.ctap.lease = time.Now().Add(-2 * clientLease)
		e.ctap.mu.Unlock()
		e.softClientMode(s2)
		if s2.client || len(s2.events) != 1 || s2.events[0].Kind != sramcapture.EventLoss {
			t.Fatalf("the scope does not take the decode back: client %v, events %+v", s2.client, s2.events)
		}
		if err := e.PostClientFrames(s2.id.Epoch, 0, nil); err == nil {
			t.Fatalf("frames taken after the page lost the decode")
		}
		// a plan only a page keeps up with ends when the page goes away
		s3 := clientTestStream(t, serLIN, p, 1<<12)
		s3.id.Epoch += 2
		s3.overBudget = true
		e.LeaseClientDecode()
		e.softClientMode(s3)
		e.ctap.mu.Lock()
		e.ctap.lease = time.Now().Add(-2 * clientLease)
		e.ctap.mu.Unlock()
		if !e.softClientMode(s3) || s3.phase != softDone || s3.err == nil {
			t.Fatalf("an over-budget stream goes on without its page: phase %v err %v", s3.phase, s3.err)
		}
	})
}

// clientBenchWords is a worker packet (64 KB) of busy two-line words, or of
// a quiet line.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func clientBenchWords(quiet bool) []byte {
	raw := make([]byte, softReadBytes)
	rng := rand.New(rand.NewSource(1))
	for i := range raw {
		raw[i] = byte(rng.Intn(256))
		if quiet {
			raw[i] = 0xFF
		}
	}
	return raw
}

// BenchmarkClientTapAppend is the scope's cost of handing a page the words
// (the owner's softClientWords: code into the ring), per worker packet of
// 16384 words; ns/op / 16384 is a word's.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkClientTapAppend(b *testing.B) {
	for _, quiet := range []bool{false, true} {
		b.Run(map[bool]string{false: "busy", true: "quiet"}[quiet], func(b *testing.B) {
			raw := clientBenchWords(quiet)
			var tp clientTap
			tp.begin(ClientStreamInfo{Session: 1, Mask: 0xFFFFFFFF, Active: true}, 0)
			b.SetBytes(int64(len(raw)))
			idx := uint64(0)
			for b.Loop() {
				tp.append(raw, idx, false, true)
				idx += uint64(4 * len(raw))
			}
		})
	}
}

// BenchmarkClientTapRead is the cost of a page's read (the web request's
// copy out of the ring), a megabyte a reply.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkClientTapRead(b *testing.B) {
	raw := clientBenchWords(false)
	var tp clientTap
	tp.begin(ClientStreamInfo{Session: 1, Mask: 0xFFFFFFFF, Active: true}, 0)
	for k := 0; k < 48; k++ {
		tp.append(raw, uint64(k*len(raw)*4), false, true)
	}
	dst := make([]byte, 0, 2<<20)
	b.SetBytes(1 << 20)
	for b.Loop() {
		cw, d := tp.read(tp.chunks[0].Seq, 1<<20, 0, dst[:0])
		if len(cw.Chunks) == 0 {
			b.Fatal("no chunks")
		}
		dst = d
	}
}

// BenchmarkSoftFeedEdges is, for comparison, the scope's own cost of the
// same packet decoded (SWD, the densest edge bus): feedBitsQuiet and the
// decode, per packet of busy words.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func BenchmarkSoftFeedEdges(b *testing.B) {
	withBitsImage(&testing.T{}, func() {
		raw := clientBenchWords(false)
		p := SerialParams{Proto: serSWD, Threshold: 128, HaveThr: true}
		t := &testing.T{}
		s := clientTestStream(t, serSWD, p, 1<<18)
		e := &Engine{logf: func(string, ...any) {}}
		b.SetBytes(int64(len(raw)))
		for b.Loop() {
			e.feedBitsQuiet(s, raw)
			if s.pending() >= s.pushN {
				e.softPush(s)
			}
			s.events, s.evHead = s.events[:0], 0
		}
	})
}
