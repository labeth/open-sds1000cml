package engine

import (
	"sync"
	"testing"
	"time"
)

// fakeClock advances instantly on Sleep so FSM waits are deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) clock() Clock {
	return Clock{
		Now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.t
		},
		Sleep: func(d time.Duration) {
			c.mu.Lock()
			c.t = c.t.Add(d)
			c.mu.Unlock()
		},
	}
}

type wr struct {
	plane uint8
	sel   uint16
	val   uint16
}

// fakeBus mimics the FPGA well enough to drive the FSM: status/fill respond
// to arm state, drains only make sense after a halt, and every write is
// recorded for order assertions.
type fakeBus struct {
	mu          sync.Mutex
	writes      []wr
	doneOnGo    bool // 0x39 bit2 asserts while armed
	trigOnGo    bool // 0x39 bit1 asserts while armed
	validOnGo   bool // 0x39 bit0 asserts while armed (AUTO free-run timeout)
	fillAdvance bool
	confDone    uint16 // CS3 0x07 value; bit7 = CONF_DONE
	wave        func(i int) (c1, c2 uint8)

	armed       bool
	halted      bool
	fill        uint16
	drainN      int
	drainSels   []uint16
	earlyDrain  bool // a drain before halt = the CPU-hang trap
	armCount    int  // 0xC3 writes; lets waves shift phase per capture
	rollN       int
	rollUnarmed bool // roll FIFO read while unarmed = the WAIT-line wedge
	rollDwell   int  // reads since the last 0xCB latch
	rollNoLatch bool // a pop without a preceding re-latch
}

func newFakeBus() *fakeBus {
	return &fakeBus{
		doneOnGo:    true,
		fillAdvance: true,
		confDone:    0x80,
		// Square wave, period 256: edges everywhere, ptp = 144.
		wave: func(i int) (uint8, uint8) {
			if (i/128)%2 == 0 {
				return 200, 60
			}
			return 56, 190
		},
	}
}

func (f *fakeBus) Read(plane uint8, sel uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if plane == 3 {
		if sel == 0x07 {
			return f.confDone, nil
		}
		return 0, nil
	}
	switch sel {
	case 0x12:
		return 0x0052, nil
	case 0x39:
		var s uint16
		if f.armed && f.doneOnGo {
			s |= 0x04
		}
		if f.armed && f.trigOnGo {
			s |= 0x02
		}
		if f.armed && f.validOnGo {
			s |= 0x01
		}
		return s, nil
	case 0x46:
		if f.armed && !f.halted && f.fillAdvance {
			f.fill += 64
			if f.fill > 0x7ff {
				f.fill = 0x7ff
			}
		}
		return f.fill, nil
	case 0x3a:
		return 0x90, nil
	case 0x3b:
		return 0x27, nil
	case selRollC1:
		if !f.armed {
			f.rollUnarmed = true
		}
		if f.rollDwell > 0 {
			f.rollNoLatch = true
		}
		f.rollDwell++
		c1, _ := f.wave(f.rollN)
		f.rollN++
		return uint16(c1) << 8, nil
	case selRollC2:
		_, c2 := f.wave(f.rollN)
		return uint16(c2) << 8, nil
	}
	return 0, nil
}

func (f *fakeBus) Write(plane uint8, sel, val uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, wr{plane, sel, val})
	if plane == 1 && sel == selArm {
		switch val {
		case opGo:
			f.armed, f.halted, f.fill = true, false, 0
			f.armCount++
		case opHalt:
			// Halt freezes the record and resets the read pointer: each
			// frame drains the wave from sample 0. drainSels accumulates
			// across frames for order assertions.
			f.halted = true
			f.drainN = 0
		case opLatch:
			f.rollDwell = 0
		}
	}
	return nil
}

func (f *fakeBus) DrainRead(sel uint16) uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.halted {
		f.earlyDrain = true
	}
	f.drainSels = append(f.drainSels, sel)
	c1, c2 := f.wave(f.drainN)
	f.drainN++
	return uint16(c1)<<8 | uint16(c2)
}

func (f *fakeBus) MmapDrain() bool { return true }

func (f *fakeBus) snapWrites() []wr {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]wr, len(f.writes))
	copy(out, f.writes)
	return out
}

func (f *fakeBus) clearWrites() {
	f.mu.Lock()
	f.writes = nil
	f.mu.Unlock()
}

func newTestEngine(t *testing.T, fb *fakeBus) (*Engine, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	e := New(Config{Bus: fb, Clock: clk.clock(), Logf: t.Logf})
	return e, clk
}

func wantWrites(t *testing.T, got, want []wr) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("write count = %d, want %d\ngot: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("write[%d] = {cs%d %#04x=%#04x}, want {cs%d %#04x=%#04x}",
				i, got[i].plane, got[i].sel, got[i].val,
				want[i].plane, want[i].sel, want[i].val)
		}
	}
}

func TestBringUpWriteOrder(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	// Spec 03 §4.1 — divisor-hi cleared FIRST, then class, lo, real hi.
	wantWrites(t, fb.snapWrites(), []wr{
		{1, selResetHd, 0x0001}, {1, selResetHd, 0x0000},
		{1, selRunWord, runAuto},
		{1, selReset2, 0x0000},
		{1, selDivHi, 0x0000},
		{1, selClass, 0x80}, {1, selDivLo, 0x0050}, {1, selDivHi, 0x0000},
	})
}

func TestArmSequence(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.armEngine()
	wantWrites(t, fb.snapWrites(), []wr{
		{1, selArm, opResetHead}, {1, selArm, opResetHead},
		{1, selWrPtr, 0x0001}, {1, selWrPtr, 0x0000},
		{1, selArm, opGo},
	})
}

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
	// Round-robin drain port order 0x30..0x34 repeating.
	fb.mu.Lock()
	defer fb.mu.Unlock()
	for i, sel := range fb.drainSels[:10] {
		if want := uint16(0x30 + i%5); sel != want {
			t.Fatalf("drain[%d] port %#04x, want %#04x", i, sel, want)
		}
	}
}

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
		if w.plane == 1 && w.sel == selArm && w.val == opHalt {
			t.Fatal("capture-halt issued on an unanchored decimated frame")
		}
	}
	if _, fresh := e.Consume(); fresh {
		t.Fatal("held frame reached the arena")
	}
}

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

func TestMemDepth(t *testing.T) {
	// The configurable decimated drain depth (fps↔data): a deeper setting drains
	// more samples per frame. Clamped to [decimWin, deepRecord].
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	if got := e.SetMemDepth(14336); got != 14336 {
		t.Fatalf("SetMemDepth(14336) = %d", got)
	}
	e.bringUp()
	e.oneFrame(false)
	if f, _ := e.Consume(); f.Valid != 14336 {
		t.Fatalf("deep drain: Valid=%d, want 14336", f.Valid)
	}
	if got := e.SetMemDepth(999999); got != deepRecord {
		t.Fatalf("clamp high = %d, want %d", got, deepRecord)
	}
	if got := e.SetMemDepth(10); got != decimWin {
		t.Fatalf("clamp low = %d, want %d", got, decimWin)
	}
}

func TestSingleForcesFullDepth(t *testing.T) {
	// A SINGLE capture ignores the shallow mem-depth setting and drains the FULL
	// deep record — the one frame you keep carries everything to zoom into.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetMemDepth(decimWin) // shallowest
	e.SetSingle()
	e.bringUp()
	e.oneFrame(false)
	if f, _ := e.Consume(); f.Valid != deepRecord {
		t.Fatalf("SINGLE drain: Valid=%d, want deepRecord %d", f.Valid, deepRecord)
	}
}

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

func TestSingleShotStopsAfterCapture(t *testing.T) {
	// SINGLE arms NORM and must STOP after the first triggered publish.
	fb := newFakeBus() // doneOnGo=true → NORM triggers immediately
	// A 2-period square in the 2048 window (like a real multi-wave display),
	// so the slope validation sees a clean adjacent plateau.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if (i/512)%2 == 0 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.SetSingle()
	if s := e.Snapshot(); !s.Single || !s.Running {
		t.Fatalf("after SetSingle: single=%v running=%v", s.Single, s.Running)
	}
	e.bringUp()
	e.oneFrame(true) // NORM triggered → publishes, then single stops
	s := e.Snapshot()
	if s.Published != 1 {
		t.Fatalf("single did not capture: published=%d", s.Published)
	}
	if s.Single || s.Running {
		t.Fatalf("single did not stop: single=%v running=%v", s.Single, s.Running)
	}
	// RUN cancels a pending single-shot.
	e.SetSingle()
	e.SetRunning(true)
	if s := e.Snapshot(); s.Single {
		t.Fatal("RUN did not cancel the pending single-shot")
	}
}

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
	e.SetChannelVdiv(0, 1) // 1 V/div: display code = 128 + volts·32
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

func TestTriggerLevelRecommit(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetTrigLevelCode(0x7530)
	fb.clearWrites()
	e.serviceCommands()
	// Spec 05 §1.3: level quad (lanes A+B, same code, hi self-latches),
	// preamble ×2, then the full re-arm.
	wantWrites(t, fb.snapWrites(), []wr{
		{3, cs3LevelALo, 0x30}, {3, cs3LevelAHi, 0x75},
		{3, cs3LevelBLo, 0x30}, {3, cs3LevelBHi, 0x75},
		{1, selPreamble, 0x0080}, {1, selPreamble, 0x0080},
		{1, selArm, opResetHead}, {1, selArm, opResetHead},
		{1, selWrPtr, 0x0001}, {1, selWrPtr, 0x0000},
		{1, selArm, opGo},
	})

	// Once-on-change: same code again must not re-emit.
	fb.clearWrites()
	e.SetTrigLevelCode(0x7530)
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("identical level re-emitted %d writes", n)
	}

	// A different code re-emits.
	e.SetTrigLevelCode(0x7560)
	e.serviceCommands()
	if n := len(fb.snapWrites()); n == 0 {
		t.Fatal("changed level did not emit")
	}
}

func TestTrigLevelClamp(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	if got := e.SetTrigLevelCode(20000); got != TrigCodeMin {
		t.Fatalf("clamp low: %d, want %d", got, TrigCodeMin)
	}
	if got := e.SetTrigLevelCode(60000); got != TrigCodeMax {
		t.Fatalf("clamp high: %d, want %d", got, TrigCodeMax)
	}
}

func TestOffsetDACFlush(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetOffsetDAC(0, 0x2968) // 10600
	e.SetOffsetDAC(1, 0x2A30) // 10800
	e.SetTrigLevelCode(30000) // trigger level flushes AFTER offsets
	fb.clearWrites()
	e.serviceCommands()
	got := fb.snapWrites()
	// Offsets: C1 lo/hi, C2 lo/hi, run-word re-assert — then the level quad.
	want := []wr{
		{3, cs3OffC1Lo, 0x68}, {3, cs3OffC1Hi, 0x29},
		{3, cs3OffC2Lo, 0x30}, {3, cs3OffC2Hi, 0x2A},
		{1, selRunWord, runAuto},
	}
	if len(got) < len(want) {
		t.Fatalf("only %d writes: %#v", len(got), got)
	}
	wantWrites(t, got[:len(want)], want)
	if got[len(want)].plane != 3 || got[len(want)].sel != cs3LevelALo {
		t.Fatalf("trigger level did not follow offsets: %#v", got[len(want)])
	}

	// Second service: nothing dirty → no writes.
	fb.clearWrites()
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("idle service emitted %d writes", n)
	}
}

func TestTrigLevelZeroKeepsBootComparator(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	if got := e.SetTrigLevelCode(0); got != 0 {
		t.Fatalf("SetTrigLevelCode(0) = %d, want 0", got)
	}
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("code 0 emitted %d writes, want none (inherited comparator kept)", n)
	}
}

func TestWedgeLadderNativeFast(t *testing.T) {
	fb := newFakeBus()
	fb.fillAdvance = false // 0x46 frozen: the wedge signature
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 } // flat drain
	fb.mu.Unlock()

	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(1e-6)
	e.band = b
	e.bringUp()

	// CONF_DONE high: dead runs accumulate and bring-up re-asserts, but a
	// flat input is indistinguishable from a partial wedge — never Wedged.
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	s := e.Snapshot()
	if s.DeadRuns != 50 {
		t.Fatalf("DeadRuns = %d, want 50", s.DeadRuns)
	}
	if s.Wedged {
		t.Fatal("wedged with CONF_DONE high (healthy flat input would crash-loop)")
	}

	// CONF_DONE lost: the fabric is dead — the next 50-multiple marks Wedged.
	fb.mu.Lock()
	fb.confDone = 0x00
	fb.mu.Unlock()
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); !s.Wedged {
		t.Fatal("CONF_DONE lost but not marked wedged")
	}
}

func TestWedgeLadderDecimated(t *testing.T) {
	// A real decimated wedge: fill frozen, drain flat, and the FPGA fabric
	// dead (CONF_DONE clear). AUTO now drains every frame, so the wedge is
	// caught on the DRAIN path (frozen fill + flat drain + CONF_DONE probe),
	// not the decimated hold path.
	fb := newFakeBus()
	fb.doneOnGo = false
	fb.fillAdvance = false
	fb.confDone = 0x00 // fabric configuration lost
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 } // flat drain
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	for i := 0; i < 50; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); !s.Wedged {
		t.Fatal("frozen decimated fill + flat drain + dead CONF_DONE not marked wedged")
	}
}

func TestWedgeLadderResetsOnActivity(t *testing.T) {
	fb := newFakeBus()
	fb.fillAdvance = false
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	b, _ := PlanTdiv(1e-6)
	e.band = b
	e.bringUp()
	for i := 0; i < 9; i++ {
		e.oneFrame(false)
	}
	if s := e.Snapshot(); s.DeadRuns != 9 {
		t.Fatalf("DeadRuns = %d, want 9", s.DeadRuns)
	}
	// Fill starts advancing again → ladder resets.
	fb.mu.Lock()
	fb.fillAdvance = true
	fb.mu.Unlock()
	e.oneFrame(false)
	if s := e.Snapshot(); s.DeadRuns != 0 {
		t.Fatalf("DeadRuns = %d after activity, want 0", s.DeadRuns)
	}
}

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
	if !fresh || f.Valid != deepRecord || !f.Interp {
		t.Fatalf("native-fast edge frame: fresh=%v valid=%d interp=%v", fresh, f.Valid, f.Interp)
	}
	if f.EdgeX < 0 {
		t.Fatalf("native-fast edge frame not centred: EdgeX=%v", f.EdgeX)
	}

	// Comparator fires but the content is a flat rail (an inconsistent/rare case): no lock,
	// so it HOLDS with the honest 60-frame flat fallback rather than centring noise.
	fb.mu.Lock()
	fb.wave = func(int) (uint8, uint8) { return 128, 128 }
	fb.mu.Unlock()
	for i := 0; i < nativeFlatFallbck-1; i++ {
		e.oneFrame(false)
	}
	if _, fresh := e.Consume(); fresh {
		t.Fatal("flat frame published before the fallback threshold")
	}
	e.oneFrame(false)
	f, fresh = e.Consume()
	if !fresh || f.EdgeX != -1 {
		t.Fatalf("flat fallback: fresh=%v EdgeX=%v, want fresh EdgeX=-1", fresh, f.EdgeX)
	}
}

func TestBandChangeAppliedAtBoundary(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	defer e.Stop(2 * time.Second)

	waitFor(t, func() bool { return e.Snapshot().Published >= 2 })

	if _, ok := e.SetTdiv(1e-6); !ok {
		t.Fatal("SetTdiv(1µs) rejected")
	}
	waitFor(t, func() bool {
		f, _ := e.Consume()
		return f != nil && f.TdivS == 1e-6 && f.Valid == deepRecord
	})
	if got := e.Snapshot().TdivS; got != 1e-6 {
		t.Fatalf("stats tdiv = %v, want 1µs", got)
	}
}

func TestStopKeepsHeartbeatAndServicesCommands(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	defer e.Stop(2 * time.Second)

	waitFor(t, func() bool { return e.Snapshot().Published >= 1 })
	e.SetRunning(false)
	// An iteration already past the running gate may still publish; wait for
	// two more loop boundaries so the stop has definitely taken effect.
	f0 := e.Snapshot().Frames
	waitFor(t, func() bool { return e.Snapshot().Frames > f0+2 })

	base := e.Snapshot()
	fb.clearWrites()
	waitFor(t, func() bool { return e.Snapshot().Frames > base.Frames+3 })
	pub := e.Snapshot().Published
	if pub != base.Published {
		t.Fatalf("published advanced while stopped: %d → %d", base.Published, pub)
	}
	for _, w := range fb.snapWrites() {
		if w.sel == selArm {
			t.Fatalf("arm opcode written while stopped: %#v", w)
		}
	}

	// Commands are still serviced while stopped.
	e.SetTrigLevelCode(30000)
	waitFor(t, func() bool {
		for _, w := range fb.snapWrites() {
			if w.plane == 3 && w.sel == cs3LevelALo {
				return true
			}
		}
		return false
	})

	// RUN resumes publishing.
	e.SetRunning(true)
	waitFor(t, func() bool { return e.Snapshot().Published > pub })
}

func TestMatrixAndLEDService(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)

	// Queue a matrix request, then service the boundary from another
	// goroutine's perspective: ReadMatrix blocks until serviceCommands runs.
	done := make(chan [5]uint16, 1)
	go func() {
		m, ok := e.ReadMatrix()
		if ok {
			done <- m
		}
		close(done)
	}()
	waitFor(t, func() bool { return len(e.matrixReq) == 1 })
	e.SetLEDs(0x2030)
	fb.clearWrites()
	e.serviceCommands()
	if _, ok := <-done; !ok {
		t.Fatal("ReadMatrix not served at the boundary")
	}
	// LED strobe: 0x0b=0, 0x0a=hi, 0x09=lo, 0x0b=1 — one indivisible burst.
	wantWrites(t, fb.snapWrites(), []wr{
		{3, 0x0b, 0}, {3, 0x0a, 0x20}, {3, 0x09, 0x30}, {3, 0x0b, 1},
	})

	// Same LED word again: compare-on-change suppresses the strobe.
	fb.clearWrites()
	e.SetLEDs(0x2030)
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("identical LED word re-strobed %d writes", n)
	}
}

func TestRunStopsAtBoundary(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	go e.Run()
	waitFor(t, func() bool { return e.Snapshot().Frames >= 1 })
	if !e.Stop(2 * time.Second) {
		t.Fatal("engine did not stop at the boundary")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached within 5s")
}
