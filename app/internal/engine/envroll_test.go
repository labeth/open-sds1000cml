package engine

import (
	"math"
	"testing"
	"time"

	"open-sds/app/internal/iface"
)

func TestEnvelopeDecim(t *testing.T) {
	// Spec 04 §5 verification constants: the phase-scatter divisor comes from the
	// formula (EnvPlan), never the nominal table row; the programmed DECIM is that
	// divisor × 5 (CaptureIntervalNs = divisor·10 ns, base tick = 2 ns).
	//
	// The × 5 is a statement about the INHERITED reference frequency, so the DECIM
	// side is evaluated at inheritedFRefHz rather than at the live fRefHz — see
	// timebase_fref_test.go. Measuring f_C2 must stay a one-line edit to fRefHz;
	// it must not drag this golden with it. EnvPlan itself carries no fRefHz
	// dependence (C0.4 scopes that to a later step), so its goldens are absolute.
	cases := []struct {
		tdiv    float64
		winCols int
		divisor uint32
	}{
		{5e-3, 217, 23041},
		{10e-3, 435, 22989},
		{20e-3, 870, 22989},
		{50e-3, 2048, 24414}, // winCols clamped 2174→2048 BEFORE the calc
	}
	for _, c := range cases {
		b, ok := PlanTdiv(c.tdiv)
		if !ok || b.Kind() != KindEnvelope {
			t.Fatalf("PlanTdiv(%g): ok=%v kind=%v", c.tdiv, ok, b.Kind())
		}
		w, d := b.EnvPlan()
		if w != c.winCols || d != c.divisor {
			t.Errorf("EnvPlan(%g) = %d/%d, want %d/%d", c.tdiv, w, d, c.winCols, c.divisor)
		}
		if got, want := b.decimFor(inheritedFRefHz), c.divisor*5; got != want {
			t.Errorf("decimFor(%g) = %d, want %d", c.tdiv, got, want)
		}
	}
}

func TestRollDecim(t *testing.T) {
	for _, tdiv := range []float64{100e-3, 1, 50} {
		b, ok := PlanTdiv(tdiv)
		if !ok || b.Kind() != KindRoll {
			t.Fatalf("PlanTdiv(%g): ok=%v kind=%v", tdiv, ok, b.Kind())
		}
		// Pinned at the inherited reference (see TestEnvelopeDecim): rollDivisor is
		// fRefHz-independent by construction, the × 5 is not.
		if got, want := b.decimFor(inheritedFRefHz), uint32(rollDivisor*5); got != want { // rollDivisor 37000
			t.Errorf("roll decimFor(%g) = %d, want %d", tdiv, got, want)
		}
	}
	if got := RollPaceNs(); got != 370000 {
		t.Errorf("RollPaceNs = %d, want 370000", got)
	}
}

// TestEnvelopeChannelPrimary: when the fabric envelope channel carries records,
// the min/max band is built from ENV_DATA (primary), not the software reducer.
func TestEnvelopeChannelPrimary(t *testing.T) {
	fb := newFakeBus()
	// Flat, off-level drain so the triggered-edge path never fires — the frame
	// takes the min/max band path, which prefers the fabric channel.
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	// The fabric folds envFabricCols columns; script a band at a middle fabric
	// column on channel 0. It must land on that fabric column's stretched display
	// span, NOT a raw 1:1 display index.
	const fcol = 168 // a mid-range fabric column (< envFabricCols)
	fb.setEnvRecords([]iface.EnvelopeRecord{
		{Col: fcol, Min: 40, Max: 210, Ch: 0},
	}, false)
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(5e-3)
	e.band = b
	e.transition(false, false)
	e.envFrame(false)
	f, fresh := e.Consume()
	if !fresh || !f.IsEnv {
		t.Fatalf("env frame: fresh=%v isEnv=%v", fresh, f.IsEnv)
	}
	// Fabric column fcol stretches onto display span [fcol·D/F, (fcol+1)·D/F).
	lo := fcol * envDisplayCols / envFabricCols
	hi := (fcol + 1) * envDisplayCols / envFabricCols
	if hi <= lo {
		t.Fatalf("empty stretch span for fabric col %d", fcol)
	}
	mid := (lo + hi) / 2
	if f.EnvMin[mid] != 40 || f.EnvMax[mid] != 210 {
		t.Fatalf("fabric col %d -> display [%d,%d): band at %d = [%d,%d], want [40,210]",
			fcol, lo, hi, mid, f.EnvMin[mid], f.EnvMax[mid])
	}
	// A display column outside any reported fabric column draws mid-line (128),
	// never a 0-rail bar.
	if f.EnvMin[0] != 128 || f.EnvMax[0] != 128 {
		t.Fatalf("unseen column = [%d,%d], want mid-line [128,128]", f.EnvMin[0], f.EnvMax[0])
	}
}

// With every fabric column reported, the stretch must leave NO display column
// blanked to mid-line — the bug where a too-small fabric FIFO left the right of
// the display flat. This directly guards the overflow regression.
func TestEnvelopeStretchNoGaps(t *testing.T) {
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	recs := make([]iface.EnvelopeRecord, 0, envFabricCols)
	for c := 0; c < envFabricCols; c++ {
		recs = append(recs, iface.EnvelopeRecord{Col: uint16(c), Min: 40, Max: 210, Ch: 0})
	}
	fb.setEnvRecords(recs, false)
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(5e-3)
	e.band = b
	e.transition(false, false)
	e.envFrame(false)
	f, fresh := e.Consume()
	if !fresh || !f.IsEnv {
		t.Fatalf("env frame: fresh=%v isEnv=%v", fresh, f.IsEnv)
	}
	for c := 0; c < envDisplayCols; c++ {
		if f.EnvMin[c] == 128 && f.EnvMax[c] == 128 {
			t.Fatalf("display column %d blanked to mid-line despite full fabric coverage (stretch gap)", c)
		}
		if f.EnvMin[c] != 40 || f.EnvMax[c] != 210 {
			t.Fatalf("display column %d = [%d,%d], want [40,210]", c, f.EnvMin[c], f.EnvMax[c])
		}
	}
}

func TestEnvelopeFrame(t *testing.T) {
	fb := newFakeBus()
	// Phase-shift the wave per capture: the real hardware phase-scatters;
	// the ring reduction needs to see both rails at each column.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		ph := fb.armCount * 37
		if ((i+ph)/128)%2 == 0 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(5e-3)
	e.band = b
	e.transition(false, false)

	for i := 0; i < 6; i++ {
		e.envFrame(false)
	}
	if fb.earlyDrain {
		t.Fatal("envelope drained before halt")
	}
	f, fresh := e.Consume()
	if !fresh || !f.IsEnv || f.EnvCols != envDisplayCols {
		t.Fatalf("env frame: fresh=%v isEnv=%v cols=%d", fresh, f.IsEnv, f.EnvCols)
	}
	// Capture carries centring margin (217 display + 2×128); WinCols is the span.
	if f.Valid != 473 || f.WinCols != 217 {
		t.Fatalf("env geometry: valid=%d win=%d, want 473/217", f.Valid, f.WinCols)
	}
	// The square wave (56..200) must appear as a wide band in min/max.
	lo, hi := f.EnvMin[400], f.EnvMax[400]
	if lo > 60 || hi < 195 {
		t.Fatalf("env band at col 400 = [%d,%d], want ≈[56,200]", lo, hi)
	}
	if f.EdgeX != -1 {
		t.Fatalf("env EdgeX = %v, want -1", f.EdgeX)
	}
	// Envelope publishes every frame, both modes.
	if s := e.Snapshot(); s.Published != 6 {
		t.Fatalf("published = %d, want 6", s.Published)
	}
}

func TestRollBand(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(100e-3)
	e.band = b
	fb.clearWrites()
	e.transition(false, false)

	// Bring-up programs the band's own decimation into DECIM_LO/HI. Take it from
	// the band rather than restating rollDivisor × 5: what this test is for is the
	// register write, and the VALUE is pinned by timebase_fref_test.go against the
	// reference it was measured at — so replacing fRefHz stays a one-line edit.
	decim := b.Decim()
	sawDecim := false
	for _, w := range fb.snapWrites() {
		if w.plane == iface.CS1 && w.sel == selDecimLo && w.val == uint16(decim&0xffff) {
			sawDecim = true
		}
	}
	if !sawDecim {
		t.Fatalf("roll bring-up did not program DECIM_LO = %#04x", uint16(decim&0xffff))
	}

	// A roll update captures on the halt engine (owned fabric halts cleanly) and
	// publishes an envelope frame — never a live-buffer (pre-halt) read.
	e.rollUpdate(false)
	if fb.earlyDrain {
		t.Fatal("roll drained the live buffer before halt")
	}
	f, fresh := e.Consume()
	if !fresh || !f.IsEnv || f.Valid != rollWin {
		t.Fatalf("roll frame: fresh=%v isEnv=%v valid=%d", fresh, f.IsEnv, f.Valid)
	}
}

func TestRollToRealTimeTransition(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(100e-3)
	e.band = b
	e.transition(false, false)
	e.rollUpdate(false)

	// Leave roll for a decimated band: the FIRST writes must idle the capture
	// FSM (OPCODE = OP_RESET ×2) so the next armed capture starts clean.
	b2, _ := PlanTdiv(500e-6)
	e.band = b2
	fb.clearWrites()
	e.transition(false, false)
	w := fb.snapWrites()
	if len(w) < 2 || w[0] != (wr{iface.CS1, selOpcode, opReset}) || w[1] != (wr{iface.CS1, selOpcode, opReset}) {
		t.Fatalf("roll→real-time did not start with OP_RESET ×2: %#v", w[:2])
	}

	// The next real-time frame must clear the envelope metadata.
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.IsEnv || f.EnvCols != 0 {
		t.Fatalf("stale envelope metadata after roll→real-time: isEnv=%v cols=%d", f.IsEnv, f.EnvCols)
	}
}

func TestETSFrame(t *testing.T) {
	fb := newFakeBus()
	// Sine with a FRACTIONAL per-capture phase shift: the sub-sample frac of
	// the mid-level crossing is what ETS bins, and on any linear ramp the
	// mid-level sits exactly half-grid (frac locked at 0.5) — a sine's
	// varying local slope lets the frac follow the phase. armCount/2 because
	// each sub-acquisition arms twice (arm + re-arm).
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		phase := float64(fb.armCount/2) * 0.37
		v := uint8(128 + 100*math.Sin(2*math.Pi*(float64(i)+phase)/16))
		return v, v
	}
	fb.mu.Unlock()

	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(50e-9) // factor 50, nCols 829
	e.band = b
	e.SetETS(true)
	e.transition(false, true)
	if !e.etsOn {
		t.Fatal("ETS not enabled after transition")
	}

	e.etsFrame(false)
	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("ETS frame not published")
	}
	if f.Valid != 829 || f.WinCols != 829 || !f.Interp {
		t.Fatalf("ETS geometry: valid=%d win=%d interp=%v, want 829/829/true", f.Valid, f.WinCols, f.Interp)
	}
	if f.IsEnv || f.EnvCols != 0 {
		t.Fatal("ETS frame carries envelope metadata")
	}
	// 32 distinct phase bins ≥ factor/4 (13): the reconstruction path runs.
	if f.EdgeX < 0 {
		t.Fatalf("ETS reconstruction expected (EdgeX=%v)", f.EdgeX)
	}
	_, _, p := ptp(f.C1[:f.Valid])
	if p < etsEdgeMinPtp {
		t.Fatalf("ETS reconstruction ptp = %d, want ≥ %d", p, etsEdgeMinPtp)
	}
}

func TestETSFallbackOnFlat(t *testing.T) {
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(50e-9)
	e.band = b
	e.SetETS(true)
	e.transition(false, true)
	e.etsFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX != -1 {
		t.Fatalf("flat ETS: fresh=%v edge=%v, want honest flat fallback", fresh, f.EdgeX)
	}
	if f.Valid != 829 {
		t.Fatalf("flat fallback valid=%d", f.Valid)
	}
}

func TestEnvelopeBailsOnBandChange(t *testing.T) {
	fb := newFakeBus()
	fb.fillAdvance = true
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(5e-3)
	e.band = b
	e.transition(false, false)

	// Stage a band change, then run a frame: it must bail unpublished
	// (the fill target is high enough that the wait loop checks interrupted).
	e.SetTdiv(1e-3)
	pub0 := e.Snapshot().Published
	e.envFrame(false)
	if got := e.Snapshot().Published; got != pub0 {
		t.Fatalf("envelope frame published despite staged band change")
	}
	_ = time.Millisecond
}
