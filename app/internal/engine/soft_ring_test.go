// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"context"
	"errors"
	"math"
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/sramcapture"
)

// softRingPlanFor is the ring plan beginSoftStream makes for a protocol on
// the digital stream: its stream decimation, the ring's, and whether it has
// one.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func softRingPlanFor(proto, baud int) (streamLog, ringLog int, why string, ok bool) {
	name, _ := softProtoName(proto)
	defer bitsImage.Store(bitsImage.Load())
	bitsImage.Store(true)
	log, bits, gapFree := softGapFreePlan(name, baud, false, softLines(proto))
	if !gapFree || !bits {
		return log, 0, "not on the digital stream", false
	}
	feature, longest, quiet, _ := decode.StreamTiming(name, float64(baud))
	sampleS := decode.StreamBitsPeriod(log)
	pushN := min(int(math.Max(0.2, math.Min(1, 2*quiet))/sampleS), softPushMax)
	n, why, ok := softRingPlan(log, feature, longest, quiet, float64(pushN)*sampleS+0.1)
	return log, n, why, ok
}

// The ring plan: a stream faster than the on-chip slack rides out (30 ms of
// pump gap) has none; the ring covers the host's latency, the frame and the
// margin, at the finest decimation that does, and keeps three ring words in
// the shortest feature.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestSoftRingPlan(t *testing.T) {
	// 2^11: 244k words/s, 25 ms of slack; 2^12: 122k words/s, 50 ms.
	if _, why, ok := softRingPlan(11, 100e-6, 1e-3, 1e-3, 0.2); ok {
		t.Fatal("2^11's word rate planned a ring")
	} else if why == "" {
		t.Fatal("no reason")
	}
	n, _, ok := softRingPlan(12, 100e-6, 1e-3, 1e-3, 0.2)
	// need = 16.8 + 200 + 1 + 50 + 1 + 150 ms = 419 ms: 2^9 holds 537 ms.
	if !ok || n != 9 {
		t.Fatalf("2^12 stream: ring 2^%d %v", n, ok)
	}
	if softRingSpan(n) < 0.419 || softRingSpan(n-1) >= 0.419 {
		t.Fatalf("2^%d spans %.3f s", n, softRingSpan(n))
	}
	// A feature of 2 us wants 0.67 us words: 2^9's 1.02 us are too coarse.
	if _, _, ok := softRingPlan(12, 2e-6, 1e-3, 1e-3, 0.2); ok {
		t.Fatal("a ring coarser than a third of the feature planned")
	}
	// A minute's frame (DCF77) asks a long ring, which its slow bits allow.
	if n, _, ok := softRingPlan(20, 0.1, 60, 2, 1); !ok || softRingSpan(n) < 63 {
		t.Fatalf("DCF77-like: 2^%d %v", n, ok)
	}
	for _, c := range []struct {
		proto, baud int
	}{{serLIN, 19200}, {serDMX, 250000}, {serModbus, 19200}, {serOneWire, 16300}, {serDALI, 1200}, {serCEC, 417},
		{serPS2, 16700}, {serJ1850, 10400}, {serPJDL, 22727}, {serDCF77, 333}, {serPDI, 250000}, {serSDQ, 98425}} {
		sl, rl, why, ok := softRingPlanFor(c.proto, c.baud)
		name, _ := softProtoName(c.proto)
		if ok {
			t.Logf("%-8s %7d Bd: stream 2^%d (%.0fk words/s), ring 2^%d: %.3g s of %.3g us words", name, c.baud, sl,
				500e3/float64(uint64(1)<<sl), rl, softRingSpan(rl), float64(uint64(1)<<rl)*2e-3)
		} else {
			t.Logf("%-8s %7d Bd: stream 2^%d, no ring: %s", name, c.baud, sl, why)
		}
	}
	// The buses this is for: a stop on a value on LIN or DMX512 shows the ring.
	for _, c := range []struct{ proto, baud int }{{serLIN, 19200}, {serDMX, 250000}, {serModbus, 19200}} {
		if _, _, why, ok := softRingPlanFor(c.proto, c.baud); !ok {
			t.Errorf("protocol %d at %d Bd: no ring: %s", c.proto, c.baud, why)
		}
	}
}

// The ring word holding a stream ordinal: the newest word's centre is the
// stamp less the decimator's delay, each word before it 2^n samples
// earlier; a stamp that wrapped past 2^32 raw words still places it; a time
// before the ring's oldest word is not held.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestSoftRingWord(t *testing.T) {
	const n = 8
	m := sramcapture.Metadata{Length: sramcapture.Words}
	for _, newest := range []uint64{3_000_000, 200_000_000, 1<<32 + 123_457, 5<<32 - 77} {
		last := uint32(newest)
		centre := 2*float64(newest) - softRingDelay(n)
		for _, k := range []uint64{0, 1, 17, 1000, uint64(sramcapture.Words) - 1} {
			for _, off := range []float64{-100, 0, 100} { // within half a word (128 samples)
				at := centre - float64(k)*256 + off
				if at < 0 {
					continue // before the stream began
				}
				ord := uint64(at)
				w, ok := softRingWord(m, n, last, ord)
				if !ok || w != m.Length-1-uint32(k) {
					t.Fatalf("newest %d, word %d from the end (+%v): got %d %v", newest, k, off, w, ok)
				}
			}
		}
		if before := centre - float64(m.Length)*256 - 200; before >= 0 {
			if _, ok := softRingWord(m, n, last, uint64(before)); ok {
				t.Fatalf("newest %d: a time before the ring placed", newest)
			}
		}
	}
	// A short ring (stopped before it filled): its own length bounds it.
	short := sramcapture.Metadata{Length: 1000}
	if _, ok := softRingWord(short, n, 1_000_000, 2*1_000_000-uint64(1500*256)); ok {
		t.Fatal("a short ring placed a time before its first word")
	}
	if _, ok := softRingWord(sramcapture.Metadata{}, n, 1, 0); ok {
		t.Fatal("an empty ring placed a time")
	}
}

// The stop's state: a frozen ring holding the frame is the record shown,
// its trigger on the frame on the session's clock (sessBase: the decoder's
// indices run on past a lost stream); a freeze that failed, or a ring that
// no longer holds the frame, arms the symbol trigger's record instead; and
// the record shown is adopted with its host trigger.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestSoftRingStopState(t *testing.T) {
	const n = 9
	mk := func() *softStream {
		s := &softStream{ring: true, ringLog: n, ordStep: 16, sessBase: 1000, gapFree: true, bits: true, value: 0x55,
			ringCfg: sramcapture.Config{DecimationLog2: n}, id: sramcapture.RecordIdentity{Epoch: softEpochBase | 1, Record: 1}}
		s.stopFirst = 1000*16 + 4_000_000 // the frame, 4 M samples into this session
		s.stopLast = s.stopFirst + 5000
		return s
	}
	frozen := sramcapture.Metadata{Ready: true, Frozen: true, Length: sramcapture.Words, TriggerIndex: sramcapture.Words, SampleRateHz: 500e6 / 512, SamplesPerWord: 1, FractionBits: 8}
	// The ring stopped 100 ms (25 M raw words) after the frame.
	last := uint32(4_000_000/2 + 25_000_000)
	s := mk()
	s.softRingDone(frozen, last, nil)
	if s.phase != softDone || !s.recOK || !s.recMeta.HostTrigger || !s.recMeta.Triggered || s.recCfg.DecimationLog2 != n {
		t.Fatalf("held frame: phase %d ok %v meta %+v", s.phase, s.recOK, s.recMeta)
	}
	k := math.Round((2*float64(last) - softRingDelay(n) - 4_000_000) / 512)
	if s.recMeta.TriggerIndex != frozen.Length-1-uint32(k) {
		t.Fatalf("trigger word %d, want %d", s.recMeta.TriggerIndex, frozen.Length-1-uint32(k))
	}
	// 100 ms at 2^9 is 98k words back: well inside 512K.
	if s.recMeta.TriggerIndex < frozen.Length-200_000 {
		t.Fatalf("trigger word %d too far back", s.recMeta.TriggerIndex)
	}
	s = mk()
	s.softRingDone(frozen, uint32(4_000_000/2+400_000_000), nil) // 1.6 s on: past a 1.07 s ring
	if s.phase != softRecord || s.recOK || s.ringErr == nil {
		t.Fatalf("frame past the ring: phase %d ok %v err %v", s.phase, s.recOK, s.ringErr)
	}
	s = mk()
	s.softRingDone(sramcapture.Metadata{}, 0, errors.New("did not freeze"))
	if s.phase != softRecord || s.recOK {
		t.Fatalf("failed freeze: phase %d ok %v", s.phase, s.recOK)
	}
	// Adopted as the stopped record, with the host's trigger.
	e, _ := newTestEngine(t, newFakeBus())
	s = mk()
	s.softRingDone(frozen, last, nil)
	e.soft = s
	meta, adopted, err := e.softFreeze(context.Background(), s.id)
	if err != nil || !adopted || !meta.Triggered || meta.SampleFirst != s.stopFirst {
		t.Fatalf("freeze: %+v %v %v", meta, adopted, err)
	}
	a := e.takeAdopted()
	if a == nil || !a.m.HostTrigger || a.m.TriggerIndex != s.recMeta.TriggerIndex || a.cfg.DecimationLog2 != n {
		t.Fatalf("adopted %+v", a)
	}
}

// A ring that has not lapped spans the time since the stream's start, less
// its arm lag: the numbers of the bits image's top-level bench pass (2^8:
// 10226 words, stamp 1310200; 2^9: 5846 words, stamp 1500126); a ring with
// half or twice the words for its time, or a stamp that wrapped past it, is
// refused and the record falls back; a lapped ring is not checked.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
func TestSoftRingPace(t *testing.T) {
	for _, c := range []struct {
		n      int
		length uint32
		last   uint32
		ok     bool
	}{
		{8, 10226, 1310200, true}, {9, 5846, 1500126, true},
		{9, 177972, 177972 * 256 * 2, false}, {9, 177972, 177972 * 128, false},
		{9, 177972, 177972*256 + 3000, true}, {9, sramcapture.Words, 7, true},
	} {
		err := softRingPace(sramcapture.Metadata{Length: c.length}, c.n, c.last)
		if (err == nil) != c.ok {
			t.Errorf("2^%d, %d words, stamp %d: %v", c.n, c.length, c.last, err)
		}
	}
	s := &softStream{ring: true, ringLog: 9, ordStep: 16, ringCfg: sramcapture.Config{DecimationLog2: 9}}
	s.softRingDone(sramcapture.Metadata{Ready: true, Frozen: true, Length: 177972}, 177972*256*2, nil)
	if s.phase != softRecord || s.recOK {
		t.Fatalf("a ring off its pace was shown: phase %d", s.phase)
	}
}
