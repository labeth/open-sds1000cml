// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"math/rand"
	"reflect"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// linesWords packs two lines of levels (0 or 1, a period each) into
// digital words: a on CH1, b on CH2.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func linesWords(a, b []byte) []byte {
	n := min(len(a), len(b)) / 16
	raw := make([]byte, 4*n)
	for i := 0; i < n; i++ {
		var wa, wb uint16
		for k := 0; k < 16; k++ {
			wa |= uint16(a[16*i+k]&1) << k
			wb |= uint16(b[16*i+k]&1) << k
		}
		raw[4*i], raw[4*i+1], raw[4*i+2], raw[4*i+3] = byte(wa), byte(wa>>8), byte(wb), byte(wb>>8)
	}
	return raw
}

// burstLevels is n periods of a line that rests at idle and now and then
// carries a burst of edges a few periods apart: what a decoder makes of it
// is beside the point, only that both paths make the same.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func burstLevels(rng *rand.Rand, n int, idle byte, run int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		for k := rng.Intn(40 * run); k > 0 && len(out) < n; k-- {
			out = append(out, idle)
		}
		lvl := idle
		for e := rng.Intn(200); e > 0 && len(out) < n; e-- {
			lvl ^= 1
			for k := run/2 + rng.Intn(run+1); k > 0 && len(out) < n; k-- {
				out = append(out, lvl)
			}
		}
	}
	return out
}

// softEdgeRun streams raw through a soft stream of proto as feedBitsQuiet
// takes it, in pieces of any size, over the edges or the samples, and
// returns its events.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func softEdgeRun(t *testing.T, proto int, p SerialParams, raw []byte, edges bool) []sramcapture.DecodedEvent {
	t.Helper()
	name, _ := softProtoName(proto)
	log, bits, ok := softGapFreePlan(name, p.Baud, false, softLines(proto))
	if !ok || !bits {
		// a bus too fast for the plan's budget: the stream at the finest period
		log = decode.StreamGapFreeMinLog
	}
	_, longest, quiet, _ := decode.StreamTiming(name, float64(p.Baud))
	d, _ := serialDecoder(proto)
	if !edges {
		d.Edges = nil
	}
	s := &softStream{p: p, proto: name, decoder: d, mask: 0xff, gapFree: true, quiet: quiet, log: uint8(log), bits: true,
		trig: StreamManual, id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1}}
	s.sampleS, s.ordStep = decode.StreamBitsPeriod(log), float64(uint64(1)<<(log-4))
	s.newDecoder(int(quiet/s.sampleS), int((longest+quiet)/s.sampleS))
	if s.edges != edges {
		t.Fatalf("%s: edge path %v, want %v", name, s.edges, edges)
	}
	lead := int(decode.StreamLead(name, float64(p.Baud)) / s.sampleS)
	s.dec.SetLead(lead)
	s.leadWords = lead/16 + 2
	s.keepWords = int(quiet/s.sampleS)/16 + s.leadWords + 2
	e := &Engine{}
	rng := rand.New(rand.NewSource(int64(len(raw))))
	for at := 0; at < len(raw); {
		n := min(len(raw)-at, 4*(1+rng.Intn(3000)))
		e.feedBitsQuiet(s, raw[at:at+n])
		at += n
		if s.pending() >= 1<<16 || rng.Intn(4) == 0 {
			e.softPush(s)
		}
	}
	e.softPush(s)
	s.emit(s.dec.Flush(), s.ord)
	return s.events
}

// The digital stream decoded over its edges gives the very events the
// samples give, for every protocol with an edge decoder: one line and two,
// a threshold off the middle, line B's level set or learned; LIN's frames
// from a real line among them.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamEdgesMatchSamples(t *testing.T) {
	withBitsImage(t, func() {
		rng := rand.New(rand.NewSource(5))
		n := 0
		for proto, name := range softProtoNames {
			d, _ := serialDecoder(proto)
			if !decode.StreamBitsLine(name) {
				if d.Edges != nil {
					t.Errorf("%s: an edge decoder for a line the digital stream does not carry", name)
				}
				continue
			}
			if d.Edges == nil {
				t.Errorf("%s: no edge decoder", name)
				continue
			}
			n++
			for k, thr := range []float64{90, 128, 200} {
				p := SerialParams{Proto: proto, Baud: 0, Threshold: thr, HaveThr: true}
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
				raw := linesWords(la, lb)
				want := softEdgeRun(t, proto, p, raw, false)
				got := softEdgeRun(t, proto, p, raw, true)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s thr %v: %d events over edges, %d over samples", name, thr, len(got), len(want))
				}
				t.Logf("%s thr %v: %d events alike", name, thr, len(want))
			}
		}
		if n < 30 {
			t.Fatalf("%d protocols with edge decoders", n)
		}
		// LIN's real frames
		const baud = 19200
		log, _, _ := softGapFreePlan("lin", baud, false, 1)
		period := decode.StreamBitsPeriod(log)
		var frames [][]int
		for i := 0; i < 30; i++ {
			frames = append(frames, []int{0x80 | i, i, 0x55, 0})
		}
		raw := bitsWords(linLevels(13/period, baud, frames), 13)
		p := SerialParams{Proto: serLIN, Baud: baud, Threshold: 90, HaveThr: true}
		want := softEdgeRun(t, serLIN, p, raw, false)
		got := softEdgeRun(t, serLIN, p, raw, true)
		if len(want) < 30 || !reflect.DeepEqual(got, want) {
			t.Fatalf("LIN: %d events over edges, %d over samples", len(got), len(want))
		}
	})
}

// ord is the float ordinal at every step and sample: a power of two from
// zero shifted, any other step or base computed.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-240
func TestSoftStreamOrd(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for it := 0; it < 200000; it++ {
		s := &softStream{ordStep: math.Ldexp(1, rng.Intn(16)-3)}
		switch rng.Intn(4) {
		case 0:
			s.ordStep *= 1 + rng.Float64()
		case 1:
			s.ordBase = float64(rng.Int63n(1 << 40))
		}
		i := uint64(rng.Int63()) >> uint(rng.Intn(64))
		if got, want := s.ord(i), uint64(s.ordBase+float64(i)*s.ordStep); got != want {
			t.Fatalf("step %v base %v sample %d: %d, want %d", s.ordStep, s.ordBase, i, got, want)
		}
	}
}
