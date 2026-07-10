package superres

import (
	"math"
	"testing"
)

// Node-free sanity for the compensation port (the cross-engine truth lives in
// comp_jsparity_test.go): the curve's anchor points, the auto sizing's caps,
// and Compensate's contract (gap preservation, DC preservation, in-band
// restoration, and the return-input-unchanged gates).
func TestCompCurveBasics(t *testing.T) {
	if h := CompCalH(0); h != 1 {
		t.Errorf("CalH(0) = %g, want 1", h)
	}
	if h := CompCalH(16e6); h > 0.75 || h < 0.65 { // ≈ −3 dB at the measured corner
		t.Errorf("CalH(16 MHz) = %g, want ≈ 0.707", h)
	}
	if h := CompCalH(1e9); h < 1e-4-1e-12 {
		t.Errorf("CalH tail floor broken: %g", h)
	}
	o := CompOpts{} // defaults: fbw 70e6, order 3, eps 0.06, gmax 6
	for f := 0.0; f <= 250e6; f += 1e6 {
		if g := CompGain(f, o); g > 6+1e-12 {
			t.Fatalf("gain %g at %g Hz exceeds gmax", g, f)
		}
	}
	info := CompFigures(o)
	if info.RecoveredF3 < 45e6 || info.RecoveredF3 > 80e6 {
		t.Errorf("default recovered −3 dB %g Hz, want ~61 MHz", info.RecoveredF3)
	}
	if info.PeakBoostDb < 5 || info.PeakBoostDb > 10 {
		t.Errorf("default peak boost %g dB, want ~7.6 dB", info.PeakBoostDb)
	}
}

func TestCompAutoCaps(t *testing.T) {
	// More bits → monotonically higher RECOVERED −3 dB (fbw itself saturates at
	// the 200 MHz cal-trust cap; the shrinking eps keeps buying bandwidth).
	prev := 0.0
	for _, bits := range []float64{1.5, 2.3, 3.3, 5} {
		o := CompAuto(bits, 250e6, 0.8)
		info := CompFigures(o)
		if info.RecoveredF3 <= prev {
			t.Errorf("auto recovered −3 dB not monotone: bits=%g f3=%g (prev %g)", bits, info.RecoveredF3, prev)
		}
		if info.PeakBoostDb > o.BudgetDb+0.01 {
			t.Errorf("bits=%g: peak boost %g dB exceeds budget %g dB", bits, info.PeakBoostDb, o.BudgetDb)
		}
		prev = info.RecoveredF3
	}
	// The raw-Nyquist ceiling: a 62.5 MHz Nyquist caps fbw at 50 MHz.
	if o := CompAuto(7, 62.5e6, 0.8); o.Fbw > 0.8*62.5e6+1 {
		t.Errorf("fbw %g exceeds the 0.8·Nyquist cap", o.Fbw)
	}
	// The 40 MHz floor.
	if o := CompAuto(0.1, 250e6, 0.8); o.Fbw < 40e6-1 {
		t.Errorf("fbw %g under the 40 MHz floor", o.Fbw)
	}
}

func TestCompensateContract(t *testing.T) {
	const m = 512
	dt := 1e-9 / 16
	// A 40 MHz tone attenuated by the measured chain (CalH ≈ 0.42), riding a
	// DC offset, with a gap band.
	att := CompCalH(40e6)
	mean := make([]float32, m)
	for i := range mean {
		mean[i] = float32(128 + 60*att*math.Sin(2*math.Pi*40e6*float64(i)*dt))
	}
	for i := 200; i < 230; i++ {
		mean[i] = -1
	}
	comp := Compensate(mean, dt, CompOpts{})
	if len(comp) != m {
		t.Fatalf("length changed: %d", len(comp))
	}
	amp := func(a []float32) float64 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for i, v := range a {
			if v < 0 || i >= 200 && i < 230 {
				continue
			}
			lo = math.Min(lo, float64(v))
			hi = math.Max(hi, float64(v))
		}
		return (hi - lo) / 2
	}
	// In-band restoration: the 0.42× tone must come back toward unity (>0.8×).
	if got := amp(comp); got < 0.8*60 || got > 1.2*60 {
		t.Errorf("40 MHz tone amplitude after comp = %.1f codes, want ≈ 60", got)
	}
	// Gap pattern preserved exactly.
	for i := range comp {
		if (mean[i] < 0) != (comp[i] < 0) {
			t.Fatalf("gap pattern broken at %d", i)
		}
	}
	// DC/offset preserved: a flat record (no AC content) must come back at the
	// same level — the DC bin is held at unity and G corrects magnitude only.
	flat := make([]float32, 256)
	for i := range flat {
		flat[i] = 173.25
	}
	for i := 60; i < 80; i++ {
		flat[i] = -1
	}
	fc := Compensate(flat, dt, CompOpts{})
	for i, v := range fc {
		if flat[i] < 0 {
			continue
		}
		if math.Abs(float64(v)-173.25) > 0.05 {
			t.Fatalf("flat record moved at %d: %g, want 173.25", i, v)
		}
	}

	// The unchanged gates return the input slice itself.
	short := []float32{1, 2, 3}
	if got := Compensate(short, dt, CompOpts{}); &got[0] != &short[0] {
		t.Error("short input should be returned unchanged")
	}
	if got := Compensate(mean, 0, CompOpts{}); &got[0] != &mean[0] {
		t.Error("dt=0 input should be returned unchanged")
	}
}
