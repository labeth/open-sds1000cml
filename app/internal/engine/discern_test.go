package engine

import (
	"math"
	"testing"
)

// square builds n samples of a square wave: low then high each halfPeriod.
func square(n, halfPeriod int, lo, hi uint8) []uint8 {
	out := make([]uint8, n)
	for i := range out {
		if (i/halfPeriod)%2 == 0 {
			out[i] = lo
		} else {
			out[i] = hi
		}
	}
	return out
}

func TestMidLevel(t *testing.T) {
	if got := midLevel(nil); got != 128 {
		t.Errorf("midLevel(nil) = %d, want 128", got)
	}
	if got := midLevel([]uint8{10, 200}); got != 105 {
		t.Errorf("midLevel = %d, want 105", got)
	}
}

func TestCenterCrossNearestCentre(t *testing.T) {
	sig := square(1000, 100, 50, 200)
	lvl := midLevel(sig)

	xc := centerCross(sig, lvl, true)
	if xc < 0 {
		t.Fatal("no rising crossing found")
	}
	// Rising transitions (low→high) happen at odd multiples of 100; the one
	// nearest index 500 is 500 itself (sig[499]=50 < lvl, sig[500]=200 ≥ lvl).
	if math.Abs(xc-499.5) > 1 {
		t.Errorf("rising crossing at %v, want ≈ 499.5", xc)
	}

	xf := centerCross(sig, lvl, false)
	if xf < 0 {
		t.Fatal("no falling crossing found")
	}
	// Falling transitions at even multiples of 100; nearest to 500 is 400 or 600.
	if math.Abs(xf-399.5) > 1 && math.Abs(xf-599.5) > 1 {
		t.Errorf("falling crossing at %v, want ≈ 399.5 or 599.5", xf)
	}
}

func TestCenterCrossFlat(t *testing.T) {
	flat := make([]uint8, 500)
	for i := range flat {
		flat[i] = 128
	}
	if xc := centerCross(flat, 128, true); xc != -1 {
		t.Errorf("flat rail produced crossing %v, want -1", xc)
	}
}

func TestCenterCrossSubSample(t *testing.T) {
	// Ramp crossing lvl=100 between samples: sig[4]=90, sig[5]=110 → frac 0.5.
	sig := []uint8{90, 90, 90, 90, 90, 110, 110, 110, 110, 110}
	xc := centerCross(sig, 100, true)
	if math.Abs(xc-4.5) > 1e-9 {
		t.Errorf("sub-sample crossing = %v, want 4.5", xc)
	}
}

func TestWindowSlopeMatches(t *testing.T) {
	sig := square(1000, 100, 50, 200)
	lvl := midLevel(sig)
	xr := centerCross(sig, lvl, true)
	if !windowSlopeMatches(sig, xr, 200, true) {
		t.Error("valid rising crossing rejected")
	}
	// The plateau adjacent to a rising crossing: left low, right high — a
	// falling request at that spot must be rejected.
	if windowSlopeMatches(sig, xr, 200, false) {
		t.Error("falling slope accepted at a rising crossing")
	}
	// Two-period window (the spec's trap case): the plateaus at the OUTER
	// window edges are opposite, so an outer-eighth comparison would reject
	// every centred edge; the adjacent-plateau logic must pass it.
	if !windowSlopeMatches(sig, xr, 400, true) {
		t.Error("two-period window false-rejected a centred edge")
	}
}

func TestWindowSlopeSmallWindowNeverVetoes(t *testing.T) {
	sig := square(1000, 100, 50, 200)
	if !windowSlopeMatches(sig, 500, 4, true) || !windowSlopeMatches(sig, 500, 4, false) {
		t.Error("window < 8 must never veto")
	}
	if !windowSlopeMatches(sig[:6], 3, 100, true) {
		t.Error("record < 8 samples must never veto")
	}
}

func TestPtp(t *testing.T) {
	lo, hi, p := ptp([]uint8{100, 50, 220, 128})
	if lo != 50 || hi != 220 || p != 170 {
		t.Errorf("ptp = %d/%d/%d, want 50/220/170", lo, hi, p)
	}
	if _, _, p := ptp(nil); p != 0 {
		t.Errorf("ptp(nil) = %d, want 0", p)
	}
}
