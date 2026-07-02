package engine

import (
	"math"
	"testing"
	"time"
)

func TestEnvelopeProgDivisor(t *testing.T) {
	// Spec 04 §5 verification constants: the PROGRAMMED divisor comes from
	// the phase-scatter formula, never the nominal table row.
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
		class, lo, hi := b.Prog()
		if class != 0x80 || uint32(lo) != c.divisor&0xffff || hi != uint16(c.divisor>>16) {
			t.Errorf("Prog(%g) = %#x/%#x/%#x", c.tdiv, class, lo, hi)
		}
	}
}

func TestRollProgDivisor(t *testing.T) {
	for _, tdiv := range []float64{100e-3, 1, 50} {
		b, ok := PlanTdiv(tdiv)
		if !ok || b.Kind() != KindRoll {
			t.Fatalf("PlanTdiv(%g): ok=%v kind=%v", tdiv, ok, b.Kind())
		}
		class, lo, hi := b.Prog()
		if class != 0x80 || lo != 0x1ce8 || hi != 0 {
			t.Errorf("roll Prog(%g) = %#x/%#x/%#x, want 0x80/0x1ce8/0", tdiv, class, lo, hi)
		}
	}
	if got := RollPaceNs(); got != 370000 {
		t.Errorf("RollPaceNs = %d, want 370000", got)
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
	if f.Valid != 217 || f.WinCols != 217 {
		t.Fatalf("env geometry: valid=%d win=%d, want 217", f.Valid, f.WinCols)
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

	// Bring-up: divisor 7400, single reset-head, wrptr pulse, arm ONCE, latch.
	sawDiv, sawGo, sawLatch := false, 0, 0
	for _, w := range fb.snapWrites() {
		if w.plane == 1 && w.sel == selDivLo && w.val == 0x1ce8 {
			sawDiv = true
		}
		if w.plane == 1 && w.sel == selArm && w.val == opGo {
			sawGo++
		}
		if w.plane == 1 && w.sel == selArm && w.val == opLatch {
			sawLatch++
		}
	}
	if !sawDiv || sawGo != 1 || sawLatch != 1 {
		t.Fatalf("roll bring-up: div=%v go=%d latch=%d", sawDiv, sawGo, sawLatch)
	}

	e.rollUpdate(false)
	if fb.rollUnarmed {
		t.Fatal("roll FIFO read while unarmed (WAIT-line wedge)")
	}
	if fb.rollNoLatch {
		t.Fatal("roll FIFO popped without a preceding 0xCB re-latch")
	}
	for _, w := range fb.snapWrites() {
		if w.plane == 1 && w.sel == selArm && w.val == opHalt {
			t.Fatal("0xC8 written on a roll band (freezes the free-run)")
		}
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

	// Leave roll for a decimated band: the FIRST writes must be the double
	// reset-head that drops the latched free-run state.
	b2, _ := PlanTdiv(500e-6)
	e.band = b2
	fb.clearWrites()
	e.transition(false, false)
	w := fb.snapWrites()
	if len(w) < 2 || w[0] != (wr{1, selArm, opResetHead}) || w[1] != (wr{1, selArm, opResetHead}) {
		t.Fatalf("roll→real-time did not start with reset-head ×2: %#v", w[:2])
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
