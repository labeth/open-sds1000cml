// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

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
