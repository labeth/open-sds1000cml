package engine

import (
	"math"
	"testing"
)

// The owned time axis hangs from exactly one named constant, fRefHz (bands.go).
// These tests exist to make replacing it a ONE-line edit — campaign step 0.8 /
// fpga-specs/takeover/18-clocks-and-timebase.md C0.4 — and so they are written
// to survive that edit:
//
//   - the golden ladder is pinned at inheritedFRefHz, the reference the table
//     was generated at, via decimFor(). Editing fRefHz to the measured f_C2
//     therefore does NOT break this file; the golden stays a true statement
//     about the value it was taken at.
//   - the rescaling law is asserted at candidate references instead, so what is
//     tested is "the arithmetic follows the constant", not "the constant is 2 ns".
//
// SCOPE (C0.4's own warning): the ten rows at tdiv >= 5 ms do not program the
// table divisor at all — the engine programs the envelope phase-scatter divisor
// (EnvPlan) or the fixed rollDivisor. Those constants carry their own, separate
// fRefHz dependence, deliberately NOT re-derived here; they are held
// byte-identical and their capture intervals appear below as goldens only.

// inheritedFRefHz is the reference frequency the golden table below was
// evaluated at: the value that reproduces the 2.0 ns base tick this engine
// inherited from the vendor's 500 MSa/s ladder. It is a TEST pin, not a claim
// about the hardware — see the UNMEASURED banner on fRefHz.
const inheritedFRefHz = 500e6

// TestBaseTickDerivesFromFRef: the base tick is computed from fRefHz, not
// written down beside it, and the const and runtime forms agree.
func TestBaseTickDerivesFromFRef(t *testing.T) {
	if got, want := float64(baseTickNs), 1e9/float64(fRefHz); got != want {
		t.Errorf("baseTickNs = %v, want 1e9/fRefHz = %v", got, want)
	}
	if got, want := baseTickNsFor(fRefHz), float64(baseTickNs); got != want {
		t.Errorf("baseTickNsFor(fRefHz) = %v, want baseTickNs = %v", got, want)
	}
	// The inherited behaviour, stated once: 500 MHz <=> a 2.0 ns tick.
	if got := baseTickNsFor(inheritedFRefHz); got != 2.0 {
		t.Errorf("baseTickNsFor(%v) = %v, want exactly 2.0 (the inherited tick)", inheritedFRefHz, got)
	}
	// ETS must not carry a second copy of the assumption (bands.go, ets.go).
	if float64(etsTs) != float64(baseTickNs) {
		t.Errorf("etsTs = %v, want baseTickNs = %v — a second hard-coded base tick has come back", float64(etsTs), float64(baseTickNs))
	}
}

// ladderGolden is the DECIM / capture-interval column of the 33-detent master
// band table, transcribed from fpga-specs/25-timebase-decimation-and-bands.md
// §6.1 (our ladder's 25 ns row where the vendor's says 20 ns, §8.1 item 3), in
// ladder order. It is the byte-identity check C0.4 asks for.
var ladderGolden = []struct {
	tdiv      float64
	decim     uint32
	captureNs float64
}{
	{1e-9, 1, 2}, {2e-9, 1, 2}, {5e-9, 1, 2}, {10e-9, 1, 2},
	{25e-9, 1, 2}, {50e-9, 1, 2}, {100e-9, 1, 2}, {200e-9, 1, 2},
	{500e-9, 2, 4}, {1e-6, 2, 4},
	{2e-6, 5, 10}, {5e-6, 5, 10}, {10e-6, 5, 10},
	{20e-6, 20, 40},
	{50e-6, 40, 80}, {100e-6, 100, 200}, {200e-6, 200, 400},
	{500e-6, 400, 800}, {1e-3, 1000, 2000}, {2e-3, 2000, 4000},
	{5e-3, 115205, 230410}, {10e-3, 114945, 229890},
	{20e-3, 114945, 229890}, {50e-3, 122070, 244140},
	{100e-3, 185000, 370000}, {200e-3, 185000, 370000},
	{500e-3, 185000, 370000}, {1, 185000, 370000},
	{2, 185000, 370000}, {5, 185000, 370000},
	{10, 185000, 370000}, {20, 185000, 370000},
	{50, 185000, 370000},
}

// TestDecimLadderRegeneratesFromFRef: every one of the 33 rows regenerates
// byte-identically from the named constant at the reference it was taken at,
// and at that reference the DELIVERED interval (DECIM x base tick) equals the
// band's target interval exactly — which is precisely why naming the constant
// changed no programmed value.
func TestDecimLadderRegeneratesFromFRef(t *testing.T) {
	td := SupportedTdivs()
	if len(td) != len(ladderGolden) {
		t.Fatalf("ladder has %d rows, golden has %d", len(td), len(ladderGolden))
	}
	tick := baseTickNsFor(inheritedFRefHz)
	for i, g := range ladderGolden {
		if td[i] != g.tdiv {
			t.Fatalf("row %d: ladder tdiv %g, golden %g (order changed)", i, td[i], g.tdiv)
		}
		b, ok := PlanTdiv(g.tdiv)
		if !ok {
			t.Fatalf("PlanTdiv(%g) not found", g.tdiv)
		}
		if got := b.CaptureIntervalNs(); got != g.captureNs {
			t.Errorf("tdiv %g: CaptureIntervalNs = %g, want %g (spec 25 §6.1)", g.tdiv, got, g.captureNs)
		}
		if got := b.decimFor(inheritedFRefHz); got != g.decim {
			t.Errorf("tdiv %g: decimFor(%v) = %d, want %d (spec 25 §6.1)", g.tdiv, inheritedFRefHz, got, g.decim)
		}
		if got, want := float64(g.decim)*tick, g.captureNs; got != want {
			t.Errorf("tdiv %g: delivered %g ns != target %g ns at the inherited reference", g.tdiv, got, want)
		}
	}
}

// TestDecimUsesFRefHz wires the ladder to the constant: Decim() is decimFor at
// fRefHz and nothing else. If someone reintroduces a literal in Decim(), this
// still passes at 500e6 — so it is paired with TestBaseTickDerivesFromFRef,
// which pins the derivation itself.
func TestDecimUsesFRefHz(t *testing.T) {
	for _, tdiv := range SupportedTdivs() {
		b, _ := PlanTdiv(tdiv)
		if got, want := b.Decim(), b.decimFor(fRefHz); got != want {
			t.Fatalf("tdiv %g: Decim() = %d, decimFor(fRefHz) = %d", tdiv, got, want)
		}
	}
}

// TestDecimRescalesWithFRef is the property that makes the future one-line edit
// correct: at ANY reference frequency the programmed DECIM must put the
// delivered interval (DECIM / fRef) within half a base tick of the band's target
// interval — or, where the target is finer than one tick, clamp to DECIM = 1 and
// deliver the floor. The candidate references are the corpus's live hypotheses
// for f_C2 (fpga-specs/takeover/18 CLK-3): 80 / 83.33 / 100 MHz, plus the
// inherited 500 MHz. None of them is a measurement — step 1.4 supplies that.
func TestDecimRescalesWithFRef(t *testing.T) {
	for _, fRef := range []float64{80e6, 83.3333333e6, 100e6, inheritedFRefHz} {
		tick := baseTickNsFor(fRef)
		for _, tdiv := range SupportedTdivs() {
			b, _ := PlanTdiv(tdiv)
			target := b.CaptureIntervalNs()
			d := b.decimFor(fRef)
			if d < 1 {
				t.Fatalf("fRef %g tdiv %g: DECIM %d below the floor", fRef, tdiv, d)
			}
			delivered := float64(d) * tick
			if target < tick { // below the fabric's floor: DECIM clamps to 1
				if d != 1 {
					t.Errorf("fRef %g tdiv %g: target %g ns < tick %g ns, want DECIM 1, got %d",
						fRef, tdiv, target, tick, d)
				}
				continue
			}
			if err := math.Abs(delivered - target); err > tick/2*(1+1e-9) {
				t.Errorf("fRef %g tdiv %g: delivered %g ns vs target %g ns, error %g > half a tick %g",
					fRef, tdiv, delivered, target, err, tick/2)
			}
		}
	}
}

// TestDecimForRejectsUnusableReference: a zero, negative or NaN reference must
// not become an out-of-range uint32 conversion in the DECIM register write.
func TestDecimForRejectsUnusableReference(t *testing.T) {
	b, _ := PlanTdiv(1e-3)
	for _, f := range []float64{0, -1, math.NaN()} {
		if got := b.decimFor(f); got != 1 {
			t.Errorf("decimFor(%v) = %d, want the DECIM=1 floor", f, got)
		}
	}
}
