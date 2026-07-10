package superres

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"open-sds/app/internal/testenv"
)

// jsOpts is a PARTIAL options object for the JS side: omitempty drops zero
// fields so Object.assign({}, SRCOMP_DEFAULT, opts) fills them — mirroring
// CompOpts' zero-means-default semantics exactly.
type jsOpts struct {
	Fbw   float64 `json:"fbw,omitempty"`
	Eps   float64 `json:"eps,omitempty"`
	Gmax  float64 `json:"gmax,omitempty"`
	Order int     `json:"order,omitempty"`
}

func (o *jsOpts) goOpts() CompOpts {
	if o == nil {
		return CompOpts{}
	}
	return CompOpts{Fbw: o.Fbw, Eps: o.Eps, Gmax: o.Gmax, Order: o.Order}
}

type autoCase struct {
	BitsGained float64 `json:"bitsGained"`
	RawNyqHz   float64 `json:"rawNyqHz"`
	Spend      float64 `json:"spend"`
}

type compCase struct {
	Mean   []float32 `json:"mean"`
	DtFine float64   `json:"dtFine"`
	Opts   *jsOpts   `json:"opts,omitempty"`
	Auto   *autoCase `json:"auto,omitempty"`
}

// TestCompParityJS pins the Go falloff-compensation port to the JS reference
// (../web/superres_comp.js) run under node: the gain/cal/target curves on a
// dense frequency grid, the filter figures, the auto-sizing (bandwidth-cap /
// floor / bisection) results, and the full srCompensate pipeline on synthetic
// stacks with gaps. Curve agreement is required to 1e-9 (relative); the
// end-to-end compensated arrays to one float32 ULP (both engines round the
// same ~1e-10-agreeing float64 stream to float32 independently). Skips when
// node is unavailable (a hard failure under CI_REQUIRE_BROWSER=1).
func TestCompParityJS(t *testing.T) {
	testenv.NeedNode(t)
	node, _ := exec.LookPath("node")

	// Frequency grid: the whole cal + extrapolation band at 0.25 MHz, the cal
	// table boundary (92 MHz) at ±ULP-ish offsets, negatives (even symmetry),
	// and off-grid irrational-ish points.
	var freqs []float64
	for i := 0; i <= 1040; i++ {
		freqs = append(freqs, float64(i)*0.25e6)
	}
	for i := 1; i <= 26; i++ {
		freqs = append(freqs, -float64(i)*9.7e6)
	}
	freqs = append(freqs, 91.999999e6, 92e6, 92.000001e6, 3.999999e6, 1.2345678e6, 259.9e6, 400e6)

	type curveCase struct {
		Opts *jsOpts `json:"opts,omitempty"`
	}
	curves := []curveCase{
		{nil},                // pure defaults
		{&jsOpts{Fbw: 40e6}}, // fixed-fbw path (the web's non-auto mode)
		{&jsOpts{Fbw: 100e6}},
		{&jsOpts{Fbw: 70e6, Eps: 0.03, Gmax: 12, Order: 2}},
	}
	autos := []autoCase{
		{0, 250e6, 0.8},   // no measured bits → the 4 dB budget floor
		{1.5, 250e6, 0.8}, // the falloff-plan table rows
		{2.3, 250e6, 0.8},
		{3.3, 250e6, 0.8},
		{5.0, 250e6, 0.8},
		{7.0, 250e6, 0.9},  // high budget → the 200 MHz cal-trust cap
		{3.3, 62.5e6, 0.8}, // low raw Nyquist → the 0.8·Nyq bandwidth cap
		{3.3, 40e6, 0.8},   // cap below the 40 MHz floor → floor wins
		{2.3, 0, 0},        // rawNyq/spend fallbacks (250e6 / 0.8)
		{5.0, 500e6, 0.65},
	}

	// srCompensate cases: gaps (leading/mid/trailing), a non-pow2 length that
	// rounds the FFT DOWN (M=520 → N=512), a clean pow2 record, and the
	// return-input-unchanged gates (all-gap, short, dt=0).
	mkTone := func(m int, dt float64, gaps [][2]int) []float32 {
		out := make([]float32, m)
		for i := 0; i < m; i++ {
			ti := float64(i) * dt
			v := 128 + 70*math.Sin(2*math.Pi*21e6*ti) + 25*math.Sin(2*math.Pi*55e6*ti+0.7)
			if i > m/2 {
				v += 40 // a step, so the endpoint detrend path is exercised
			}
			out[i] = float32(v)
		}
		for _, g := range gaps {
			for i := g[0]; i < g[1] && i < m; i++ {
				out[i] = -1
			}
		}
		return out
	}
	allGap := make([]float32, 32)
	for i := range allGap {
		allGap[i] = -1
	}
	fine16 := 1e-9 / 16
	comps := []compCase{
		{Mean: mkTone(300, fine16, [][2]int{{0, 3}, {40, 55}, {297, 300}}), DtFine: fine16,
			Auto: &autoCase{2.3, 250e6, 0.8}},
		{Mean: mkTone(256, 2e-9/32, nil), DtFine: 2e-9 / 32, Opts: &jsOpts{Fbw: 70e6}},
		{Mean: mkTone(520, fine16, [][2]int{{100, 104}}), DtFine: fine16, Opts: &jsOpts{Fbw: 60e6}},
		{Mean: allGap, DtFine: fine16},                 // all-gap → input unchanged
		{Mean: mkTone(7, fine16, nil), DtFine: fine16}, // <8 bins → input unchanged
		{Mean: mkTone(64, fine16, nil), DtFine: 0},     // dt gate → input unchanged
	}

	payload, err := json.Marshal(map[string]any{
		"freqs": freqs, "curves": curves, "autos": autos, "comps": comps,
	})
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "comp.json")
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "comp_parity_driver.cjs", tmp).CombinedOutput()
	if err != nil {
		t.Fatalf("node driver failed: %v\n%s", err, out)
	}
	var js struct {
		Curves []struct {
			Gain        []float64 `json:"gain"`
			Cal         []float64 `json:"cal"`
			Target      []float64 `json:"target"`
			PeakBoostDb float64   `json:"peakBoostDb"`
			RecoveredF3 float64   `json:"recoveredF3"`
		} `json:"curves"`
		Autos []struct {
			Fbw         float64 `json:"fbw"`
			Eps         float64 `json:"eps"`
			Gmax        float64 `json:"gmax"`
			BudgetDb    float64 `json:"budgetDb"`
			PeakBoostDb float64 `json:"peakBoostDb"`
			RecoveredF3 float64 `json:"recoveredF3"`
		} `json:"autos"`
		Comps []struct {
			Comp []float64 `json:"comp"`
		} `json:"comps"`
	}
	if err := json.Unmarshal(out, &js); err != nil {
		t.Fatalf("bad driver output: %v\n%s", err, out)
	}

	// close asserts |a-b| within 1e-9 relative (1e-9 absolute below 1).
	close := func(what string, a, b float64) {
		t.Helper()
		tol := 1e-9 * math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
		if math.Abs(a-b) > tol || math.IsNaN(a) != math.IsNaN(b) {
			t.Errorf("%s: go=%.15g js=%.15g (Δ=%.3g)", what, a, b, a-b)
		}
	}

	if len(js.Curves) != len(curves) {
		t.Fatalf("curve count %d vs %d", len(js.Curves), len(curves))
	}
	for ci, c := range curves {
		o := c.Opts.goOpts()
		jc := js.Curves[ci]
		od := o.withDefaults()
		for fi, f := range freqs {
			close("gain", CompGain(f, o), jc.Gain[fi])
			close("calH", CompCalH(f), jc.Cal[fi])
			close("targetH", CompTargetH(f, od.Fbw, od.Order), jc.Target[fi])
			if t.Failed() {
				t.Fatalf("curve %d diverged first at f=%g", ci, f)
			}
		}
		info := CompFigures(o)
		close("peakBoostDb", info.PeakBoostDb, jc.PeakBoostDb)
		close("recoveredF3", info.RecoveredF3, jc.RecoveredF3)
	}

	if len(js.Autos) != len(autos) {
		t.Fatalf("auto count %d vs %d", len(js.Autos), len(autos))
	}
	for ai, a := range autos {
		o := CompAuto(a.BitsGained, a.RawNyqHz, a.Spend)
		ja := js.Autos[ai]
		close("auto.fbw", o.Fbw, ja.Fbw)
		close("auto.eps", o.Eps, ja.Eps)
		close("auto.gmax", o.Gmax, ja.Gmax)
		close("auto.budgetDb", o.BudgetDb, ja.BudgetDb)
		info := CompFigures(o)
		close("auto.peakBoostDb", info.PeakBoostDb, ja.PeakBoostDb)
		close("auto.recoveredF3", info.RecoveredF3, ja.RecoveredF3)
		if t.Failed() {
			t.Fatalf("auto case %d (bits=%g nyq=%g spend=%g) diverged", ai, a.BitsGained, a.RawNyqHz, a.Spend)
		}
	}

	if len(js.Comps) != len(comps) {
		t.Fatalf("comp count %d vs %d", len(js.Comps), len(comps))
	}
	for ci, cc := range comps {
		var o CompOpts
		if cc.Auto != nil {
			o = CompAuto(cc.Auto.BitsGained, cc.Auto.RawNyqHz, cc.Auto.Spend)
		} else {
			o = cc.Opts.goOpts()
		}
		got := Compensate(cc.Mean, cc.DtFine, o)
		want := js.Comps[ci].Comp
		if len(got) != len(want) {
			t.Fatalf("comp %d length %d vs %d", ci, len(got), len(want))
		}
		var maxD float64
		for i := range got {
			if (cc.Mean[i] < 0) != (want[i] == -1) || (got[i] == -1) != (want[i] == -1) {
				t.Fatalf("comp %d gap pattern mismatch at %d: go=%g js=%g", ci, i, got[i], want[i])
			}
			d := math.Abs(float64(got[i]) - want[i])
			if d > maxD {
				maxD = d
			}
			// One float32 ULP at full code scale: both engines round a float64
			// stream agreeing to ~1e-10 into float32; only boundary values can
			// land one quantum apart.
			if d > 1e-4 {
				t.Fatalf("comp %d value mismatch at %d: go=%g js=%g (Δ=%.3g)", ci, i, got[i], want[i], d)
			}
		}
		t.Logf("comp case %d: max |Δ| = %.3g (n=%d)", ci, maxD, len(got))
	}
}
