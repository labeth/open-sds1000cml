// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/sramcapture"
)

// A deep raw screen is decimated to a display-sized live record; a screen that
// already fits is left alone.
// TRLC-LINKS: REQ-SDS-010
func TestLiveSRAMPlanBoundsTheScreen(t *testing.T) {
	for _, tdiv := range []float64{200e-6, 500e-6, 10e-3, 1} {
		p := PlanSRAM(tdiv)
		live, ok := liveSRAMPlan(p)
		if !ok {
			t.Fatalf("%g s/div: screen %d not reduced", tdiv, p.Screen)
		}
		if (live.Screen > liveScreenSamples && live.Log < 20) || live.Log < 4 || live.Samples < live.Screen || live.Samples > int(sramcapture.Words) {
			t.Fatalf("%g s/div: bad live plan %+v", tdiv, live)
		}
		if span, want := float64(live.Screen)*live.SampleS, 10*tdiv; span < want*0.99 || span > want*1.01 {
			t.Fatalf("%g s/div: live screen spans %g s, want %g", tdiv, span, want)
		}
	}
	for _, tdiv := range []float64{1e-6, 500e-9} {
		if _, ok := liveSRAMPlan(PlanSRAM(tdiv)); ok {
			t.Fatalf("%g s/div: a short raw screen was decimated", tdiv)
		}
	}
}

// A stopped record's recall and its conditioning come back byte-identical.
// TRLC-LINKS: REQ-SDS-033
func TestHeldRecallRoundTrip(t *testing.T) {
	var h heldRecall
	mk := func(n int, fill byte) *Frame {
		f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Q1: make([]uint16, n), Q2: make([]uint16, n), Valid: n}
		for i := 0; i < n; i++ {
			f.C1[i], f.C2[i], f.Q1[i], f.Q2[i] = fill+byte(i), fill-byte(i), uint16(i)<<4, uint16(i)<<5
		}
		return f
	}
	src := mk(100, 7)
	key := heldKey{offset: 0, words: 50}
	h.store(key, src)
	src.Filter, src.FilterGuard, src.BandwidthHz = "cal", 3, 1e6
	h.storeCond(heldCondKey{held: key}, src)
	dst := mk(100, 0)
	if n := h.restore(dst); n != 100 {
		t.Fatalf("restored %d samples", n)
	}
	h.restoreCond(dst, dst.Q1, dst.Q2)
	for i := range src.C1 {
		if dst.C1[i] != src.C1[i] || dst.C2[i] != src.C2[i] || dst.Q1[i] != src.Q1[i] || dst.Q2[i] != src.Q2[i] {
			t.Fatalf("sample %d differs", i)
		}
	}
	if dst.Filter != "cal" || dst.FilterGuard != 3 || dst.BandwidthHz != 1e6 {
		t.Fatalf("conditioning fields lost: %+v", dst)
	}
}

// Peak stream words decode to (min, max) pairs per channel, ordered to
// continue from the view: a falling edge inside a bucket is one transition.
// TRLC-LINKS: REQ-SDS-035
func TestPeakSamplesOrdered(t *testing.T) {
	e := &Engine{}
	r := &rollView{}
	r.reset(planRollPeak(0.01), 0.01, [2]chScale{})
	// {max2,max1,min2,min1}: high, high->low (edge), low; CH2 constant 100.
	raw := []byte{
		200, 100, 200, 100, // bucket 0: CH1 200..200
		10, 100, 200, 100, // bucket 1: CH1 10..200 (falling edge)
		10, 100, 10, 100, // bucket 2: CH1 10..10
	}
	q1, q2 := e.peakSamples(r, raw)
	want := []uint8{200, 200, 200, 10, 10, 10}
	for i, v := range want {
		if roundQ8(q1[i]) != v || roundQ8(q2[i]) != 100 {
			t.Fatalf("CH1 %v, want %v; CH2 %v", q1, want, q2)
		}
	}
	if p := planRollPeak(0.01); p.screen > rollScreenSamples || p.log < 8 || p.screen%2 != 0 {
		t.Fatalf("peak plan %+v", p)
	}
}

// Every roll timebase keeps the view within rollScreenSamples, grouping in
// software beyond the largest decimation (50 s/div was 476k samples, and a
// knob step re-sized and re-drew them for seconds).
// TRLC-LINKS: REQ-SDS-010
func TestRollPlansBounded(t *testing.T) {
	for _, tdiv := range []float64{0.01, 0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50} {
		p := planRoll(tdiv)
		if p.screen > rollScreenSamples || p.group < 1 || p.chunk < 8 {
			t.Fatalf("roll plan at %g s/div: %+v", tdiv, p)
		}
		if span := float64(p.screen) * p.sampleS; math.Abs(span-10*tdiv) > p.sampleS {
			t.Fatalf("roll plan at %g s/div spans %g s", tdiv, span)
		}
		q := planRollPeak(tdiv)
		if q.screen > rollScreenSamples || q.group < 1 || q.screen%2 != 0 {
			t.Fatalf("peak plan at %g s/div: %+v", tdiv, q)
		}
		if span := float64(q.screen) * q.sampleS; math.Abs(span-10*tdiv) > 2*q.sampleS {
			t.Fatalf("peak plan at %g s/div spans %g s", tdiv, span)
		}
	}
	if planRoll(50).group == 1 || planRollPeak(50).group == 1 {
		t.Fatal("50 s/div should group in software")
	}
}

// Software groups average (chunked) or merge extremes (stream) and carry a
// partial group to the next update.
// TRLC-LINKS: REQ-SDS-010
func TestRollGroupCarries(t *testing.T) {
	r := &rollView{}
	r.reset(rollPlan{group: 4, screen: 100}, 1, [2]chScale{})
	o1, _ := r.groupAvg(0, []uint16{100, 200, 300}, []uint16{1, 1, 1})
	if len(o1) != 0 {
		t.Fatalf("partial group published %v", o1)
	}
	o1, o2 := r.groupAvg(2, []uint16{500, 700, 900, 1100, 1300}, []uint16{1, 1, 1, 1, 1})
	// 100 200 300 | 500 ; held 300 300 is placed before the new samples:
	// groups: {100,200,300,300} {300,500,700,900} ; carry {1100,1300}
	if len(o1) != 2 || o1[0] != 225 || o1[1] != 600 || o2[0] != 1 {
		t.Fatalf("groups %v %v", o1, o2)
	}
	r.reset(rollPlan{group: 3, screen: 100}, 1, [2]chScale{})
	// bytes: min1 min2 max1 max2
	if out := r.groupPeak([]byte{50, 60, 70, 80, 40, 61, 75, 79}); len(out) != 0 {
		t.Fatalf("partial peak group published %v", out)
	}
	out := r.groupPeak([]byte{55, 59, 90, 81, 1, 2, 3, 4})
	if len(out) != 4 || out[0] != 40 || out[1] != 59 || out[2] != 90 || out[3] != 81 || len(r.carry) != 4 {
		t.Fatalf("peak group %v carry %v", out, r.carry)
	}
}

// A V/div round trip keeps the roll history: codes clip on the screen at the
// sensitive scale, but the history is volts (bench 2026-10-10: 1 -> 0.5 -> 1
// V/div left a 3.3 V square topped at 2.7 V).
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
func TestRollRescaleKeepsHistory(t *testing.T) {
	r := &rollView{}
	one := [2]chScale{{vdiv: 1, off: 0}, {vdiv: 1, off: 0}}
	r.reset(rollPlan{group: 1, screen: 6}, 1, one)
	in := []uint16{128 << 8, 140 << 8, 215 << 8, 41 << 8, 250 << 8, 5 << 8} // -0..+4.9 V
	r.push(0, in, in)
	for i, v := range in {
		if r.q1[i] != v {
			t.Fatalf("code %d: %d, want %d (volts round trip)", i, r.q1[i], v)
		}
	}
	half := [2]chScale{{vdiv: 0.5, off: 0}, {vdiv: 0.5, off: 0}}
	r.rescale(half, time.Now())
	if r.c1[4] != 255 || r.c1[5] != 0 {
		t.Fatalf("at 0.5 V/div the extremes should clip on screen: %v", r.c1)
	}
	r.rescale(one, time.Now())
	for i, v := range in {
		if r.q1[i] != v {
			t.Fatalf("after 1 -> 0.5 -> 1 V/div code %d is %d, want %d", i, r.q1[i], v)
		}
	}
}

// A peak view holds a gap with its last (min, max) pair, not one value.
// TRLC-LINKS: REQ-SDS-010
func TestRollPeakHoldPairs(t *testing.T) {
	r := &rollView{}
	one := [2]chScale{{vdiv: 1}, {vdiv: 1}}
	r.reset(rollPlan{group: 1, screen: 8, peak: true}, 1, one)
	lo, hi := uint16(100<<8), uint16(200<<8)
	r.push(0, []uint16{lo, hi, lo, hi}, []uint16{lo, hi, lo, hi})
	r.push(4, nil, nil)
	for i, want := range []uint16{lo, hi, lo, hi, lo, hi, lo, hi} {
		if r.q1[i] != want {
			t.Fatalf("held view %v", r.q1)
		}
	}
}
