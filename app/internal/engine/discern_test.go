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

// lcg is a tiny deterministic PRNG so the noise tests are reproducible.
type lcg uint64

func (r *lcg) next() float64 { *r = *r*6364136223846793005 + 1442695040888963407; return float64(*r>>11) / float64(1<<53) }
func (r *lcg) gauss() float64 { // Box-Muller-ish; good enough for noise shaping
	u1, u2 := r.next()+1e-12, r.next()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

// noisyTriangle builds n samples of a triangle (peak-to-peak amp, given
// samples-per-period) centred at 128, plus Gaussian noise of the given sigma.
func noisyTriangle(n, period int, amp, sigma float64, seed uint64) []uint8 {
	r := lcg(seed)
	out := make([]uint8, n)
	for i := range out {
		ph := float64(i%period) / float64(period)
		var tri float64
		if ph < 0.5 {
			tri = 2 * ph
		} else {
			tri = 2 * (1 - ph)
		}
		v := 128 + amp*(tri-0.5) + sigma*r.gauss()
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		out[i] = uint8(v)
	}
	return out
}

func noisyFlat(n int, sigma float64, seed uint64) []uint8 {
	r := lcg(seed)
	out := make([]uint8, n)
	for i := range out {
		v := 128 + sigma*r.gauss()
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		out[i] = uint8(v)
	}
	return out
}

// TestSignalPresentSmallSignal — the small-signal lock gate must ADMIT a real
// small on-screen signal (ptp well below the old 40-code floor) and REJECT a
// noisy flat rail whose raw ptp can EXCEED that signal's. Covers the sub-1.6-div
// case that used to freeze NORM (edge found but ptp<40 → never locked).
func TestSignalPresentSmallSignal(t *testing.T) {
	const k = 8.0
	// Real signals: a 2.4 Vpp cal signal is ptp ≈ 8/13/32 at 10/5/2 V/div. Test
	// well-sampled (240 samples/period) AND aliased (20 samples/period).
	for _, amp := range []float64{8, 13, 32} {
		for _, period := range []int{240, 20} {
			sig := noisyTriangle(6000, period, amp, 0.5, 42)
			if !signalPresent(sig, k) {
				_, _, p := ptp(sig)
				t.Errorf("real signal amp=%.0f period=%d (ptp=%d) not present, want present", amp, period, p)
			}
		}
	}
	// Flat rails across a wide noise range must be rejected — even where the
	// rail's raw ptp exceeds a small signal's.
	for _, sigma := range []float64{0.5, 1.0, 1.5, 2.0, 2.5, 3.0} {
		for seed := uint64(1); seed <= 8; seed++ {
			flat := noisyFlat(6000, sigma, seed*131+7)
			if signalPresent(flat, k) {
				_, _, p := ptp(flat)
				t.Errorf("flat rail sigma=%.1f seed=%d (ptp=%d) reported present, want rejected", sigma, seed, p)
			}
		}
	}
}

// TestNoiseFloorPeriodIndependent — noiseFloor must estimate the per-sample
// noise, not the signal, regardless of how many periods sit in the record (the
// 2nd difference cancels the linear ramp). A clean well-sampled ramp reads ~the
// noise floor; a pure flat rail reads a similar per-sample noise.
func TestNoiseFloorPeriodIndependent(t *testing.T) {
	clean := noisyTriangle(6000, 300, 60, 0.4, 3)
	nf := noiseFloor(clean)
	if nf > 3 {
		t.Errorf("noiseFloor of a clean ramp = %.1f, want small (≈ per-sample noise)", nf)
	}
	// Even with many periods (few samples/period), a well-sampled ramp's floor
	// stays low — the discriminator does not wash out.
	dense := noisyTriangle(6000, 40, 60, 0.4, 4)
	if nfd := noiseFloor(dense); nfd > 4 {
		t.Errorf("noiseFloor of a dense ramp = %.1f, want still small", nfd)
	}
}
