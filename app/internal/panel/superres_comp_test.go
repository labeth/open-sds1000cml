package panel

import (
	"math"
	"testing"

	"open-sds/app/internal/superres"
)

// The device review path must hand the LCD falloff-COMPENSATED means (web
// parity: srMakeViewFrame applies srCompensate to both channels before any
// view consumes them). Pin the wrapper's gating and per-channel application;
// the compensation math itself is pinned cross-engine in
// internal/superres/comp_jsparity_test.go.
func TestSRCompMeansAppliesFalloffComp(t *testing.T) {
	st := superres.New(256, 16)
	st.SampleS = 2e-9 // raw 500 MSa/s → fine dt 0.125 ns
	dt := st.SampleS / float64(st.K)
	nb := 256 * 16
	tone := func(phase float64) []float32 {
		m := make([]float32, nb)
		att := superres.CompCalH(40e6) // what the analog chain did to a 40 MHz tone
		for i := range m {
			m[i] = float32(128 + 50*att*math.Sin(2*math.Pi*40e6*float64(i)*dt+phase))
		}
		return m
	}
	mean, mean2 := tone(0), tone(1.1)
	for i := 300; i < 340; i++ {
		mean[i] = -1 // a gap band survives the comp untouched
	}
	res := superres.Result{Mean: mean, Mean2: mean2, BitsGained: 2.3}

	cm, cm2 := srCompMeans(st, res)
	if &cm[0] == &mean[0] || &cm2[0] == &mean2[0] {
		t.Fatal("compensation did not run (input slices returned)")
	}
	amp := func(m []float32) float64 {
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, v := range m {
			if v < 0 {
				continue
			}
			lo, hi = math.Min(lo, float64(v)), math.Max(hi, float64(v))
		}
		return (hi - lo) / 2
	}
	// The in-band 40 MHz tone must be boosted back toward its true 50-code
	// amplitude on BOTH channels (chain attenuation ≈ 0.42×).
	if a := amp(cm); a < 40 {
		t.Errorf("align channel not compensated: amplitude %.1f, want ≈ 50", a)
	}
	if a := amp(cm2); a < 40 {
		t.Errorf("other channel not compensated: amplitude %.1f, want ≈ 50", a)
	}
	for i := 300; i < 340; i++ {
		if cm[i] != -1 {
			t.Fatalf("gap sentinel lost at %d: %g", i, cm[i])
		}
	}

	// Gate: no sample interval (SampleS 0) → means pass through untouched.
	st0 := superres.New(256, 16)
	pm, pm2 := srCompMeans(st0, res)
	if &pm[0] != &mean[0] || &pm2[0] != &mean2[0] {
		t.Error("dt=0 stack must return the uncompensated means unchanged")
	}

	// Nil means (stats-only crunch) stay nil.
	nm, nm2 := srCompMeans(st, superres.Result{})
	if nm != nil || nm2 != nil {
		t.Error("nil means must stay nil")
	}
}
