package engine

import (
	"math"
	"testing"
)

func TestZoneMaskPublishPolicy(t *testing.T) {
	fb := newFakeBus()
	// Square wave, period 256, plus a controllable feature pulse: code 250
	// over samples 1400..1450 when the flag is up. The flag is read under
	// fb.mu (DrainRead holds it), so flip it only between oneFrame calls.
	feature := false
	fb.wave = func(i int) (uint8, uint8) {
		var c1 uint8 = 56
		if (i/128)%2 == 0 {
			c1 = 200
		}
		if feature && i >= 1400 && i < 1450 {
			c1 = 250
		}
		return c1, 255 - c1
	}
	e, _ := newTestEngine(t, fb)
	e.bringUp()

	// learn the lock geometry from a plain publish
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX < 0 {
		t.Fatalf("no locked baseline frame (fresh=%v)", fresh)
	}
	edgeX, sampleS := f.EdgeX, f.SampleS
	dtLo := (1405 - edgeX) * sampleS
	dtHi := (1445 - edgeX) * sampleS
	e.SetZones([]Zone{{DtLoS: dtLo, DtHiS: dtHi, CodeLo: 230, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)

	// NORM: exactly the feature frames publish.
	for k := 0; k < 20; k++ {
		feature = k%3 == 0
		e.oneFrame(true)
		_, fresh := e.Consume()
		if fresh != feature {
			t.Fatalf("NORM frame %d: published=%v feature=%v", k, fresh, feature)
		}
	}

	// AUTO liveness: feature permanently off -> exactly one unqualified
	// publish per zoneFallback holds.
	feature = false
	e.zoneHeld = 0
	pubs := 0
	for k := 0; k < 2*zoneFallback; k++ {
		e.oneFrame(false)
		if _, fresh := e.Consume(); fresh {
			pubs++
		}
	}
	if pubs != 2 {
		t.Fatalf("AUTO liveness: %d publishes over %d holds, want 2", pubs, 2*zoneFallback)
	}

	// Mask counts every locked frame even while the zone holds, and ring
	// entries carry DISTINCT capture identities.
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range lo {
		lo[j], hi[j] = 0, 255
	}
	// a tight band that the square wave violates immediately
	for j := 0; j < win; j++ {
		hi[j] = 100
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)
	before := e.maskFail.Load()
	for k := 0; k < 6; k++ {
		e.oneFrame(false) // zone still holds (feature off)
	}
	if got := e.maskFail.Load() - before; got != 6 {
		t.Fatalf("mask must test held frames: %d fails over 6 held frames", got)
	}
	ring := e.MaskFails()
	seen := map[uint64]bool{}
	for _, r := range ring {
		if seen[r.Seq] {
			t.Fatalf("ring entries share a capture identity (seq %d twice)", r.Seq)
		}
		seen[r.Seq] = true
	}

	// Stop-on-fail publishes the failing frame even though the zone holds.
	e.SetMaskMode(MaskStopFail)
	e.oneFrame(false)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("stop-on-fail must force-publish past the zone hold")
	}
	if !e.maskStopped.Load() || e.running.Load() {
		t.Fatal("stop-on-fail must latch and stop acquisition")
	}
}

func TestMaskGeometryFuzz(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	win := e.band.WinCols()
	const n = 6016
	const sampleS = 2e-9

	// golden: flat 100 +/- tolerance band
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range lo {
		lo[j], hi[j] = 90, 110
	}

	seed := int64(424242)
	rng := func() float64 {
		seed = (seed*6364136223846793005 + 1442695040888963407) & 0x7fffffffffffffff
		return float64(seed%1_000_000) / 1_000_000
	}

	cases := 0
	for _, posFrac := range []float64{0.1, 0.5, 0.9} {
		for _, edgeX := range []float64{40.7, 900.2, 3000.5, float64(n) - 60.3} {
			for _, liveDepth := range []int{0, int(edgeX) + 150, n} {
				for k := 0; k < 12; k++ {
					// flat frame with one spike at a random raw position
					f := &Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n}
					for i := 0; i < n; i++ {
						f.C1[i] = 100
						f.C2[i] = 100
					}
					spike := int(rng() * float64(n))
					f.C1[spike] = 220

					left := int(math.Round(edgeX - float64(win)*posFrac))
					testableEnd := n
					if liveDepth > 0 && liveDepth < n {
						testableEnd = liveDepth
					}
					col := spike - left
					wantFail := col >= 0 && col < win && spike >= 0 && spike < testableEnd

					e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
					e.SetMaskMode(MaskTest)
					e.ClearMaskFails()
					fail, _ := e.maskEval(f, n, liveDepth, edgeX, sampleS, posFrac)
					cases++
					if fail != wantFail {
						t.Fatalf("posFrac=%.1f edgeX=%.1f live=%d spike=%d (col %d): fail=%v want %v",
							posFrac, edgeX, liveDepth, spike, col, fail, wantFail)
					}
					if fail {
						r := e.MaskFails()
						mf := r[len(r)-1]
						if mf.FailSample != left+mf.FailCol {
							t.Fatalf("geometry: FailSample %d != left %d + FailCol %d", mf.FailSample, left, mf.FailCol)
						}
						if mf.FailSample != spike {
							t.Fatalf("localization: FailSample %d, spike %d", mf.FailSample, spike)
						}
					}
				}
			}
		}
	}
	t.Logf("mask geometry fuzz: %d cases", cases)
}

// Env/roll frames have no edge anchor: the zone trigger and mask cannot run.
// They must still publish (holding would blank slow timebases forever), but
// the bypass is COUNTED — a silently bypassed qualifier wears a clean run's
// signature.
func TestZoneMaskEnvRollCounted(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(5e-3) // KindEnvelope
	if !ok || b.Kind() != KindEnvelope {
		t.Fatalf("5ms/div did not plan an envelope band (kind %v)", b.Kind())
	}
	e.band = b
	e.transition(false, false)
	e.SetZoneMode(ZoneTrigger)
	e.SetZones([]Zone{{DtLoS: 0, DtHiS: 1e-6, CodeLo: 0, CodeHi: 255}})
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range hi {
		hi[j] = 255
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)

	e.envFrame(false)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("env frame must still publish under an armed zone trigger")
	}
	if e.zoneSkip.Load() != 1 {
		t.Fatalf("zoneSkip = %d, want 1", e.zoneSkip.Load())
	}
	if e.maskSkip.Load() != 1 {
		t.Fatalf("maskSkip = %d, want 1", e.maskSkip.Load())
	}
	if e.maskPass.Load() != 0 || e.maskFail.Load() != 0 {
		t.Fatal("an env frame must not count as a mask verdict")
	}

	// roll path counts the same way
	b, ok = PlanTdiv(100e-3)
	if !ok || b.Kind() != KindRoll {
		t.Fatalf("100ms/div did not plan a roll band")
	}
	e.band = b
	e.transition(false, false)
	e.rollUpdate(false)
	if e.zoneSkip.Load() < 2 || e.maskSkip.Load() < 2 {
		t.Fatalf("roll publish not counted: zoneSkip=%d maskSkip=%d", e.zoneSkip.Load(), e.maskSkip.Load())
	}
}

// A frame whose whole window falls off the record / into the dead tail has
// ZERO testable columns — that is a skip, not a pass (found by the geometry
// fuzz review: a zero-column "pass" wears a clean run's signature).
func TestMaskZeroTestableColumnsSkips(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	win := e.band.WinCols()
	lo := make([]uint8, win)
	hi := make([]uint8, win)
	for j := range hi {
		hi[j] = 255
	}
	e.SetMask(&Mask{Lo: lo, Hi: hi, WinCols: win, Ch: 0})
	e.SetMaskMode(MaskTest)
	const n = 6016
	f := zmFrame(n, 100, 0, 0)
	// liveDepth far left of the window: every column is dead
	edgeX := float64(n) - 10
	skip0 := e.maskSkip.Load()
	fail, _ := e.maskEval(f, n, 100, edgeX, 2e-9, 0.5)
	if fail {
		t.Fatal("zero-testable-column frame must not fail")
	}
	if e.maskPass.Load() != 0 {
		t.Fatal("zero-testable-column frame must not count as a pass")
	}
	if e.maskSkip.Load() != skip0+1 {
		t.Fatalf("zero-testable-column frame must count a skip (got %d)", e.maskSkip.Load()-skip0)
	}
}

// NaN trigger position would silently un-map every mask column (left = NaN
// -> all columns "off-record" -> eternal skips at best).
func TestTrigPosFracNaNGuard(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	e.SetTrigPosFrac(math.NaN())
	if got := e.Snapshot().TrigPosFrac; got != 0.5 {
		t.Fatalf("NaN posFrac stored as %v, want the 0.5 default", got)
	}
}

// SINGLE + zone trigger compose: the latch must wait for a QUALIFYING frame,
// not the first triggered one — that is the flagship "arm single, catch the
// anomaly" workflow.
func TestSingleShotWaitsForZoneQualify(t *testing.T) {
	fb := newFakeBus()
	// The feature must be PERIOD-RELATIVE: a SINGLE arms the full deep drain
	// (effDrainCols), which moves edgeX — an absolute-index feature would slide
	// out of the zone window. Put it at +32..56 samples after EVERY rising
	// edge of the square instead.
	feature := false
	fb.wave = func(i int) (uint8, uint8) {
		var c1 uint8 = 56
		if (i/128)%2 == 0 {
			c1 = 200
		}
		if feature && i%256 >= 32 && i%256 < 56 {
			c1 = 250
		}
		return c1, 255 - c1
	}
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX < 0 {
		t.Fatal("no baseline lock")
	}
	e.SetZones([]Zone{{DtLoS: 36 * f.SampleS, DtHiS: 52 * f.SampleS, CodeLo: 230, CodeHi: 255}})
	e.SetZoneMode(ZoneTrigger)
	e.SetSingle()
	for k := 0; k < 8; k++ { // unqualified frames must not satisfy the single
		e.oneFrame(true)
		if !e.singleArmed.Load() {
			t.Fatalf("single latched on an unqualified frame (k=%d)", k)
		}
		if _, fresh := e.Consume(); fresh {
			t.Fatalf("unqualified frame published under single+zone (k=%d)", k)
		}
	}
	feature = true
	e.oneFrame(true)
	if _, fresh := e.Consume(); !fresh {
		t.Fatal("qualifying frame did not publish")
	}
	if e.singleArmed.Load() || e.running.Load() {
		t.Fatal("single must latch and stop on the qualifying frame")
	}
}
