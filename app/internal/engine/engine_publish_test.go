// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"

	"open-sds/app/internal/iface"
)

// TRLC-LINKS: REQ-SDS-007, REQ-SDS-009, REQ-SDS-011
func TestDecimatedAutoPublishes(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < 3; i++ {
		e.oneFrame(false)
	}
	if fb.earlyDrain {
		t.Fatal("drain issued before capture-halt (CPU-hang trap)")
	}
	s := e.Snapshot()
	if s.Published != 3 || s.Coherent != 3 {
		t.Fatalf("published=%d coherent=%d, want 3/3", s.Published, s.Coherent)
	}
	f, fresh := e.Consume()
	if !fresh || f.Seq != 3 {
		t.Fatalf("consume: fresh=%v seq=%d, want fresh seq 3", fresh, f.Seq)
	}
	if f.Valid != decimDrain || f.WinCols != 2048 || f.Interp {
		t.Fatalf("frame geom: valid=%d win=%d interp=%v, want %d/2048", f.Valid, f.WinCols, f.Interp, decimDrain)
	}
	if f.EdgeX < 0 {
		t.Fatalf("EdgeX = %v, want a real crossing", f.EdgeX)
	}
	if f.C1[0] != 200 || f.C2[0] != 60 {
		t.Fatalf("drain content: C1[0]=%d C2[0]=%d, want 200/60", f.C1[0], f.C2[0])
	}
	if f.IsEnv || f.EnvCols != 0 {
		t.Fatal("envelope metadata not cleared")
	}
	// Every pop was covered by BURST_REMAIN (no read past the record).
	if fb.popNoRemain {
		t.Fatal("BURST popped beyond what BURST_REMAIN reported")
	}
	if s.ShortDrains != 0 {
		t.Fatalf("short drains = %d, want 0", s.ShortDrains)
	}
}

// TRLC-LINKS: REQ-SDS-009
func TestShortRecordIsNotCoherent(t *testing.T) {
	// The fabric finalized fewer words than the frame asked for: the drain
	// pops only what BURST_REMAIN holds, pads the slot with the last real word,
	// counts the shortfall, and the frame is not coherent.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	fb.mu.Lock()
	fb.post = 1000 // record = 3072 + 1000 < decimDrain
	fb.mu.Unlock()
	e.oneFrame(false)
	s := e.Snapshot()
	if s.ShortDrains != 1 || s.Coherent != 0 {
		t.Fatalf("short record: short_drains=%d coherent=%d, want 1/0", s.ShortDrains, s.Coherent)
	}
	if fb.popNoRemain {
		t.Fatal("popped past the record")
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedNormHoldsWithoutDone(t *testing.T) {
	fb := newFakeBus()
	fb.doneOnGo = false // comparator never fires
	e, _ := newTestEngine(t, fb)
	e.SetNorm(true)
	e.bringUp()
	e.oneFrame(true)
	s := e.Snapshot()
	if s.Published != 0 || s.Held != 1 {
		t.Fatalf("published=%d held=%d, want 0/1", s.Published, s.Held)
	}
	// The engine must not have halted a half-empty record.
	for _, w := range fb.snapWrites() {
		if w.plane == 1 && w.sel == iface.SelOpcode && w.val == iface.OpHalt {
			t.Fatal("capture-halt issued on an unanchored decimated frame")
		}
	}
	if _, fresh := e.Consume(); fresh {
		t.Fatal("held frame reached the arena")
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedAutoPublishesOnFreeRun(t *testing.T) {
	// AUTO with NO trigger at all (neither DONE nor VALID, and 0x46 the
	// post-trigger counter would stay low): the frame must still PUBLISH the
	// free-running buffer snapshot, or an untriggered AUTO display starves.
	fb := newFakeBus()
	fb.doneOnGo = false
	fb.trigOnGo = false
	fb.validOnGo = false
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false) // AUTO
	if s := e.Snapshot(); s.Published != 1 || s.Held != 0 {
		t.Fatalf("AUTO free-run: published=%d held=%d, want 1/0", s.Published, s.Held)
	}

	// NORM with the same signals must still HOLD — no trigger, no frame.
	fb2 := newFakeBus()
	fb2.doneOnGo = false
	fb2.trigOnGo = false
	e2, _ := newTestEngine(t, fb2)
	e2.SetNorm(true)
	e2.bringUp()
	e2.oneFrame(true)
	if s := e2.Snapshot(); s.Published != 0 || s.Held != 1 {
		t.Fatalf("NORM untriggered: published=%d held=%d, want 0/1", s.Published, s.Held)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedAutoHoldsWrongSlopeFrame(t *testing.T) {
	// Sub-period AUTO backstop: a free-run frame whose only edge is the WRONG
	// slope (falling when we want rising → edgeX=-1) but which is NOT flat
	// (ptp≥40) must HOLD, not flash an uncentred frame. A correct-slope frame
	// publishes and centres. This is what stabilises 50 µs AUTO.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { // single FALLING edge on C1: no rising crossing
		if i < 900 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false) // AUTO
	if s := e.Snapshot(); s.Published != 0 || s.Held != 1 {
		t.Fatalf("wrong-slope AUTO frame: published=%d held=%d, want 0/1 (hold)", s.Published, s.Held)
	}
	// A rising-edge frame publishes.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if i < 900 {
			return 56, 190
		}
		return 200, 60
	}
	fb.mu.Unlock()
	e.oneFrame(false)
	if s := e.Snapshot(); s.Published != 1 {
		t.Fatalf("rising-edge AUTO frame did not publish: published=%d, want 1", s.Published)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedAutoWrongSlopeLiveness(t *testing.T) {
	// AUTO LIVENESS on a persistently un-lockable signal (fuzz-found, HW-verified):
	// a live signal whose record NEVER contains the requested slope (e.g. a fast
	// stream aliased by a slow band — on the bench, 2 Mbps Manchester at 50 µs/div
	// on falling-edge froze AUTO forever) must still refresh the display with an
	// honest unlocked frame every nativeFlatFallbck holds. NORM keeps holding.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { // single FALLING edge: no rising crossing, ever
		if i < 900 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < nativeFlatFallbck+2; i++ {
		e.oneFrame(false) // AUTO
	}
	s := e.Snapshot()
	if s.Published == 0 {
		t.Fatalf("AUTO froze: %d holds, 0 liveness publishes (want >=1 per %d holds)", s.Held, nativeFlatFallbck)
	}
	f, _ := e.Consume()
	if f.EdgeX >= 0 || f.Trigd {
		t.Fatalf("liveness frame must be honest/unlocked: EdgeX=%v Trigd=%v", f.EdgeX, f.Trigd)
	}
	// NORM: strictly held, no liveness.
	fb2 := newFakeBus()
	fb2.mu.Lock()
	fb2.wave = func(i int) (uint8, uint8) {
		if i < 900 {
			return 200, 60
		}
		return 56, 190
	}
	fb2.mu.Unlock()
	e2, _ := newTestEngine(t, fb2)
	e2.bringUp()
	e2.SetNorm(true)
	e2.serviceCommands()
	e2.transition(true, false)
	for i := 0; i < nativeFlatFallbck+2; i++ {
		e2.oneFrame(true)
	}
	if s2 := e2.Snapshot(); s2.Published != 0 {
		t.Fatalf("NORM published %d liveness frames; NORM must hold strictly", s2.Published)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedAutoLivenessTimeBound(t *testing.T) {
	// The AUTO liveness fallback must be bounded by WALL CLOCK, not only by the
	// 60-frame count: at slow bands one hold cycle costs the full wait budget, so
	// counting alone stretches the refresh to 5-8 s (fuzz-found @ 500 µs/div).
	// After autoLivenessMaxWait without a publish, a single held frame refreshes.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { // falling edge only: never locks on rising
		if i < 900 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.oneFrame(false) // hold #1 (flatHeld=1, no lastPubAt yet -> count path only)
	if s := e.Snapshot(); s.Published != 0 {
		t.Fatalf("first wrong-slope hold published (%d); want hold", s.Published)
	}
	// Simulate a stale last publish: the engine published long ago.
	e.lastPubAt = e.clk.Now().Add(-2 * autoLivenessMaxWait)
	e.oneFrame(false)
	if s := e.Snapshot(); s.Published != 1 {
		t.Fatalf("time-bound liveness did not fire: published=%d, want 1", s.Published)
	}
	f, _ := e.Consume()
	if f.EdgeX >= 0 || f.Trigd {
		t.Fatalf("liveness frame must be honest/unlocked: EdgeX=%v Trigd=%v", f.EdgeX, f.Trigd)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestSingleShotNotConsumedByFlatFallback(t *testing.T) {
	// SINGLE on a QUIET screen must never fire (a real scope's single-shot waits
	// forever without a trigger). On native-fast NORM the flat fallback publishes
	// one honest coherent frame every nativeFlatFallbck holds — the single latch
	// used to gate on `coherent` and consumed exactly that refresh, stopping the
	// engine with a "captured" flat screen. It now gates on `lock`.
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { return 128, 128 } // flat rail: no lock possible
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(1e-6)
	if !ok {
		t.Fatal("1 µs not in ladder")
	}
	e.band = b
	e.SetSingle()
	e.bringUp()
	for i := 0; i < nativeFlatFallbck+5; i++ {
		e.oneFrame(true) // single forces NORM
	}
	s := e.Snapshot()
	if s.Published == 0 {
		t.Fatal("flat fallback never refreshed (test premise broken)")
	}
	if !s.Running || !s.Single {
		t.Fatalf("flat refresh consumed the single-shot: running=%v single=%v", s.Running, s.Single)
	}
	// A real edge then fires the single and stops.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if (i/512)%2 == 0 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	fb.trigOnGo = true
	e.oneFrame(true)
	if s := e.Snapshot(); !s.Running && !s.Single {
		return // stopped on the genuine trigger — pass
	}
	// allow one extra frame for the comparator path on this harness
	e.oneFrame(true)
	if s := e.Snapshot(); s.Running || s.Single {
		t.Fatalf("single did not stop on a genuine trigger: running=%v single=%v", s.Running, s.Single)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestEdgeLevelOffSignalDoesNotLock(t *testing.T) {
	// A trigger level set OFF the signal band cannot be crossed, so no trigger is
	// possible. Regression: the EDGE path used to fall back to the signal's own
	// mid-level crossing and fabricate a lock — the scope kept "triggering" with
	// the level parked above the wave.
	//
	// Default fake signal is a 56..200 square. SetTrigLevelCode(27000) maps to
	// display code 255 (off the top). AUTO must FREE-RUN unlocked (published,
	// EdgeX = -1, Trigd = false); NORM must HOLD.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetTrigLevelCode(27000) // dc = 255, above the 56..200 signal
	e.bringUp()
	e.oneFrame(false) // AUTO
	if s := e.Snapshot(); s.Published != 1 {
		t.Fatalf("AUTO off-signal: published=%d, want 1 (free-run, not a hold)", s.Published)
	}
	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("AUTO off-signal frame never reached the arena")
	}
	if f.EdgeX >= 0 {
		t.Fatalf("AUTO off-signal EdgeX=%v, want -1 (no lock — the level is off the wave)", f.EdgeX)
	}
	if f.Trigd {
		t.Fatal("AUTO off-signal frame marked Trigd; an off-wave level must not claim a trigger")
	}

	// NORM with the same off-signal level: no trigger can come → HOLD.
	fb2 := newFakeBus()
	e2, _ := newTestEngine(t, fb2)
	e2.SetNorm(true)
	e2.SetTrigLevelCode(27000)
	e2.bringUp()
	e2.oneFrame(true)
	if s := e2.Snapshot(); s.Published != 0 || s.Held != 1 {
		t.Fatalf("NORM off-signal: published=%d held=%d, want 0/1 (hold)", s.Published, s.Held)
	}

	// Control: an ON-signal level (centre, dc=128) still locks and centres.
	fb3 := newFakeBus()
	e3, _ := newTestEngine(t, fb3)
	e3.SetTrigLevelCode(31434) // dc = 128, mid of 56..200
	e3.bringUp()
	e3.oneFrame(false)
	f3, fresh3 := e3.Consume()
	if !fresh3 || f3.EdgeX < 0 {
		t.Fatalf("ON-signal AUTO: fresh=%v EdgeX=%v, want a real crossing (lock)", fresh3, f3.EdgeX)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestDecimatedFlatFallback(t *testing.T) {
	// A genuinely flat/DC decimated screen (ptp < threshold) has no lock to be had. AUTO
	// HOLDs (re-presenting the last edge) and publishes ONE honest flat capture (EdgeX=-1)
	// every nativeFlatFallbck frames for liveness — it does NOT free-run every frame (that
	// would flicker on a sub-period band that catches edges only intermittently).
	fb := newFakeBus()
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 } // flat DC
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < nativeFlatFallbck-1; i++ {
		e.oneFrame(false) // held: not yet at the flat fallback
	}
	if s := e.Snapshot(); s.Published != 0 {
		t.Fatalf("flat DC published before the fallback: published=%d, want 0", s.Published)
	}
	e.oneFrame(false) // the nativeFlatFallbck-th held frame → one honest flat capture
	if s := e.Snapshot(); s.Published != 1 {
		t.Fatalf("flat DC fallback did not publish: published=%d, want 1", s.Published)
	}
	f, fresh := e.Consume()
	if !fresh || f.EdgeX != -1 {
		t.Fatalf("DC fallback frame: fresh=%v EdgeX=%v, want fresh EdgeX=-1", fresh, f.EdgeX)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestLevelAnchoredCentering(t *testing.T) {
	// With a trigger level set, the display should anchor on the crossing of
	// THAT level, not the mid-level.
	fb := newFakeBus()
	// A single rising ramp 0→255 across the whole drained record: the crossing
	// of level L is at a monotonically increasing index, so a higher level
	// anchors strictly LATER than the mid-level. Scaled to decimDrain so the
	// ramp stays monotonic across the full (margin-carrying) drain.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) { v := uint8(i * 255 / decimDrain); return v, v }
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.SetChannelVdiv(0, 1, 0, 0) // 1 V/div: display code = 128 + volts·32
	e.bringUp()

	// No level set (boot): anchors at the mid-level (~128) crossing near centre.
	e.oneFrame(false) // AUTO publishes regardless of slope validation
	f, _ := e.Consume()
	midEdge := f.EdgeX

	// Level display code ≈ 200 (volts 2.25 @ 1 V/div): crosses strictly later.
	e.SetTrigLevelCode(uint16(31434 - 2110))
	e.serviceCommands()
	e.oneFrame(false)
	f2, _ := e.Consume()
	if f2.EdgeX <= midEdge+200 {
		t.Fatalf("level-anchored EdgeX %v not well past mid-level %v (level ignored)", f2.EdgeX, midEdge)
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestNativeFastAutoFreeRunUntriggered(t *testing.T) {
	// Native-fast "free run + trigger hold" (spec 04 §11): when the HW comparator does NOT fire
	// within the budget (untriggered), AUTO FREE-RUNS a live refresh frame (EdgeX=-1, record
	// centre) rather than holding — this is the different technique the ≤200 ns bands need to
	// stay live. NORM trigger-HOLDs an untriggered screen.
	b, ok := PlanTdiv(200e-9)
	if !ok {
		t.Fatal("200ns not in ladder")
	}

	// AUTO, comparator silent (trigOnGo=false) → free-run a flat refresh.
	fb := newFakeBus()
	fb.trigOnGo, fb.doneOnGo = false, false
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.band = b
	e.bringUp()
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.EdgeX != -1 {
		t.Fatalf("native-fast AUTO untriggered: fresh=%v EdgeX=%v, want fresh EdgeX=-1 (free-run)", fresh, f.EdgeX)
	}

	// NORM, comparator silent → HOLD (no publish).
	fb2 := newFakeBus()
	fb2.trigOnGo, fb2.doneOnGo = false, false
	fb2.mu.Lock()
	fb2.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb2.mu.Unlock()
	e2, _ := newTestEngine(t, fb2)
	e2.band = b
	e2.SetNorm(true)
	e2.bringUp()
	e2.oneFrame(true)
	if _, fresh := e2.Consume(); fresh {
		t.Fatal("native-fast NORM untriggered published — should trigger-hold")
	}
}

// TRLC-LINKS: REQ-SDS-011
func TestNativeFastContentGate(t *testing.T) {
	fb := newFakeBus()
	fb.trigOnGo = true // HW comparator fires → native-fast waits for it and catches the edge
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(1e-6)
	if !ok {
		t.Fatal("1 µs not in ladder")
	}
	e.band = b
	e.bringUp()

	// Edge-rich wave + comparator (bit1) → the record captures the edge; publish it centred
	// from the full deep record (spec 04 §11 trigger-hold path).
	e.oneFrame(false)
	f, fresh := e.Consume()
	if !fresh || f.Valid != maxRecordCols || !f.Interp {
		t.Fatalf("native-fast edge frame: fresh=%v valid=%d interp=%v", fresh, f.Valid, f.Interp)
	}
	if f.EdgeX < 0 {
		t.Fatalf("native-fast edge frame not centred: EdgeX=%v", f.EdgeX)
	}

	// Comparator fires but the content is a flat rail (the common DC-input case: a mid-scale
	// level inside the noise fires the comparator every record): no lock, so AUTO native-fast
	// FREE-RUNS an honest unlocked refresh (EdgeX = -1) every frame — never a 60-frame hold
	// (HW-verified 0 fps with the calibrated default image before this rule).
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	for i := 0; i < 3; i++ {
		e.oneFrame(false)
		f, fresh = e.Consume()
		if !fresh || f.EdgeX != -1 || f.Trigd {
			t.Fatalf("flat AUTO native-fast frame %d: fresh=%v EdgeX=%v trigd=%v, want a fresh unlocked refresh", i, fresh, f.EdgeX, f.Trigd)
		}
	}
	// NORM keeps the trigger-hold with the honest 60-frame flat fallback.
	for i := 0; i < nativeFlatFallbck-1; i++ {
		e.oneFrame(true)
	}
	if _, fresh := e.Consume(); fresh {
		t.Fatal("NORM flat frame published before the fallback threshold")
	}
	e.oneFrame(true)
	f, fresh = e.Consume()
	if !fresh || f.EdgeX != -1 {
		t.Fatalf("NORM flat fallback: fresh=%v EdgeX=%v, want fresh EdgeX=-1", fresh, f.EdgeX)
	}
}
