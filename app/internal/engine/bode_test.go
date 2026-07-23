package engine

import (
	"math"
	"testing"
)

// synthSine builds an n-sample record of a sine at freqHz with the given
// amplitude (codes), DC offset (codes), and phase (radians), clamped to 0..255.
func synthSine(n int, freqHz, sampleS, ampCodes, offCodes, phaseRad float64) []uint8 {
	s := make([]uint8, n)
	for i := 0; i < n; i++ {
		v := offCodes + ampCodes*math.Sin(2*math.Pi*freqHz*float64(i)*sampleS+phaseRad)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		s[i] = uint8(math.Round(v))
	}
	return s
}

func TestFundamentalHz(t *testing.T) {
	const n, dt = 4096, 2e-9
	for _, f := range []float64{1e6, 5e6, 12.5e6, 25e6} {
		sig := synthSine(n, f, dt, 90, 128, 0.3)
		got := fundamentalHz(sig, dt)
		if math.Abs(got-f) > f*0.02 {
			t.Errorf("fundamentalHz(%.0f) = %.0f, want within 2%%", f, got)
		}
	}
}

func TestBodePointGainPhase(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n, dt = 4096, 2e-9
	const f = 5e6

	cases := []struct {
		name         string
		amp1, amp2   float64
		ph1, ph2     float64 // radians
		wantGainDB   float64
		wantPhaseDeg float64
		gTol, pTol   float64
	}{
		{"unity 0dB 0deg", 90, 90, 0, 0, 0, 0, 0.3, 1.5},
		{"half amplitude -6dB", 90, 45, 0, 0, -6.02, 0, 0.4, 1.5},
		{"double amplitude +6dB", 45, 90, 0, 0, 6.02, 0, 0.4, 1.5},
		{"+90 deg phase", 90, 90, 0, math.Pi / 2, 0, 90, 0.3, 2},
		{"-45 deg phase", 90, 90, 0, -math.Pi / 4, 0, -45, 0.3, 2},
		{"+6dB and +120deg", 45, 90, 0, 2 * math.Pi / 3, 6.02, 120, 0.5, 2.5},
	}
	for _, c := range cases {
		f1 := &Frame{
			C1:    synthSine(n, f, dt, c.amp1, 128, c.ph1),
			C2:    synthSine(n, f, dt, c.amp2, 128, c.ph2),
			Valid: n, SampleS: dt,
		}
		e.ClearBode()
		e.SetBodeMode(true, 0, 1) // ref C1, DUT C2
		e.bodeEval(f1, n, dt)
		pts := e.BodePoints()
		if len(pts) != 1 {
			t.Fatalf("%s: got %d points, want 1", c.name, len(pts))
		}
		p := pts[0]
		if math.Abs(p.FreqHz-f) > f*0.02 {
			t.Errorf("%s: freq %.0f, want ~%.0f", c.name, p.FreqHz, f)
		}
		if math.Abs(p.GainDB-c.wantGainDB) > c.gTol {
			t.Errorf("%s: gain %.2f dB, want %.2f (±%.2f)", c.name, p.GainDB, c.wantGainDB, c.gTol)
		}
		// phase wraps; compare on the circle
		dPh := math.Mod(p.PhaseDeg-c.wantPhaseDeg+540, 360) - 180
		if math.Abs(dPh) > c.pTol {
			t.Errorf("%s: phase %.2f deg, want %.2f (±%.2f)", c.name, p.PhaseDeg, c.wantPhaseDeg, c.pTol)
		}
	}
}

// A pure time delay τ on the DUT channel must read 0 dB and phase = −360·f·τ
// (mod 360) — the analytic Bode of an ideal delay, and exactly the FPGA
// validation source (C2 = C1 delayed by N samples).
func TestBodePureDelay(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n, dt = 6000, 2e-9
	const tau = 20e-9 // 10 samples at 2 ns
	e.SetBodeMode(true, 0, 1)
	for _, f := range []float64{1e6, 3e6, 7e6, 15e6} {
		e.ClearBode()
		ref := synthSine(n, f, dt, 90, 128, 0)
		dut := synthSine(n, f, dt, 90, 128, -2*math.Pi*f*tau) // delayed by tau
		e.bodeEval(&Frame{C1: ref, C2: dut, Valid: n, SampleS: dt}, n, dt)
		pts := e.BodePoints()
		if len(pts) != 1 {
			t.Fatalf("f=%.0f: got %d points", f, len(pts))
		}
		p := pts[0]
		if math.Abs(p.GainDB) > 0.4 {
			t.Errorf("delay f=%.0f: gain %.2f dB, want ~0", f, p.GainDB)
		}
		wantPhase := math.Mod(-360*f*tau+3600, 360)
		if wantPhase > 180 {
			wantPhase -= 360
		}
		dPh := math.Mod(p.PhaseDeg-wantPhase+540, 360) - 180
		if math.Abs(dPh) > 2.5 {
			t.Errorf("delay f=%.0f: phase %.2f deg, want %.2f", f, p.PhaseDeg, wantPhase)
		}
	}
}

func TestBodeRejectsFloorAndSubCycle(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n, dt = 4096, 2e-9
	e.SetBodeMode(true, 0, 1)

	// flat reference (no signal) → invalid, no point
	e.ClearBode()
	flat := make([]uint8, n)
	for i := range flat {
		flat[i] = 128
	}
	e.bodeEval(&Frame{C1: flat, C2: synthSine(n, 5e6, dt, 90, 128, 0), Valid: n, SampleS: dt}, n, dt)
	if len(e.BodePoints()) != 0 || e.Snapshot().BodeValid {
		t.Error("flat reference must not produce a Bode point")
	}

	// sub-cycle: only ~1 cycle in the record → rejected (needs >= bodeMinCycles)
	e.ClearBode()
	lowF := 1.0 / (float64(n) * dt) // exactly 1 cycle
	e.bodeEval(&Frame{C1: synthSine(n, lowF, dt, 90, 128, 0), C2: synthSine(n, lowF, dt, 90, 128, 0), Valid: n, SampleS: dt}, n, dt)
	if len(e.BodePoints()) != 0 {
		t.Error("a sub-bodeMinCycles record must be rejected")
	}
}

func TestBodeAccumulationBinsByFrequency(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	const n, dt = 4096, 2e-9
	e.ClearBode()
	e.SetBodeMode(true, 0, 1)
	// sweep several distinct frequencies → distinct bins accumulate
	for _, f := range []float64{1e6, 2e6, 5e6, 10e6, 20e6} {
		s1 := synthSine(n, f, dt, 90, 128, 0)
		s2 := synthSine(n, f, dt, 45, 128, 0) // -6 dB DUT
		e.bodeEval(&Frame{C1: s1, C2: s2, Valid: n, SampleS: dt}, n, dt)
	}
	pts := e.BodePoints()
	if len(pts) != 5 {
		t.Fatalf("swept 5 frequencies, got %d accumulated points", len(pts))
	}
	// sorted ascending, all ~ -6 dB
	for i := 1; i < len(pts); i++ {
		if pts[i].FreqHz <= pts[i-1].FreqHz {
			t.Error("BodePoints not sorted ascending by frequency")
		}
	}
	for _, p := range pts {
		if math.Abs(p.GainDB-(-6.02)) > 0.5 {
			t.Errorf("f=%.0f gain %.2f dB, want ~-6", p.FreqHz, p.GainDB)
		}
	}
	// re-visiting a frequency updates its bin (no duplicate)
	e.bodeEval(&Frame{C1: synthSine(n, 5e6, dt, 90, 128, 0), C2: synthSine(n, 5e6, dt, 90, 128, 0), Valid: n, SampleS: dt}, n, dt)
	if len(e.BodePoints()) != 5 {
		t.Errorf("re-visiting 5 MHz should update, not add a bin (now %d)", len(e.BodePoints()))
	}
}

// singleBinDFT's incremental rotation must match a direct-trig reference over a
// deep record — the periodic renormalization keeps the recurrence from drifting.
func TestSingleBinDFTMatchesDirect(t *testing.T) {
	const n = 20480
	const sampleS = 1e-8 // 100 MS/s
	const f0 = 1e6
	sig := make([]uint8, n)
	for i := range sig { // a real tone + offset, 8-bit
		sig[i] = uint8(128 + 60*math.Sin(2*math.Pi*f0*float64(i)*sampleS))
	}
	re, im := singleBinDFT(sig, f0, sampleS)

	// direct reference: a fresh trig call per sample (no accumulation)
	var sum float64
	for _, v := range sig {
		sum += float64(v)
	}
	mean := sum / n
	var reRef, imRef float64
	for i := 0; i < n; i++ {
		ang := 2 * math.Pi * f0 * float64(i) * sampleS
		v := float64(sig[i]) - mean
		reRef += v * math.Cos(ang)
		imRef -= v * math.Sin(ang)
	}
	magRef := math.Hypot(reRef, imRef)
	if magRef == 0 {
		t.Fatal("reference magnitude is zero")
	}
	relErr := math.Hypot(re-reRef, im-imRef) / magRef
	if relErr > 1e-6 {
		t.Fatalf("incremental DFT drifted from direct: rel err %g (re=%g/%g im=%g/%g)", relErr, re, reRef, im, imRef)
	}
}
