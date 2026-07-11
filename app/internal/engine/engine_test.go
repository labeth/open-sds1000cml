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

// DrainInto mirrors the real bulk drain via DrainRead so the fake keeps tracking
// earlyDrain / drainSels / drainN and generating the same wave.
func (f *fakeBus) DrainInto(c1, c2 []uint8, cols int) {
	sel := uint16(0x30)
	for i := 0; i < cols; i++ {
		w := f.DrainRead(sel)
		c1[i] = uint8(w >> 8)
		c2[i] = uint8(w)
		if sel++; sel > 0x34 {
			sel = 0x30
		}
	}
}

func (f *fakeBus) DrainWrite(sel, val uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, wr{1, sel, val})
	return nil
}

func (f *fakeBus) MmapDrain() bool                     { return true }
func (f *fakeBus) SetDrainMode(mode int) int           { return 0 }
func (f *fakeBus) SetReadCycle(ticks int) (int, error) { return 0, nil }

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

func TestHoldoffPacing(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetFramePeriod(0) // no base pacing floor, so holdoff is the only delay
	if got := e.SetHoldoff(0.2); got != 0.2 {
		t.Fatalf("SetHoldoff returned %v, want 0.2", got)
	}
	if e.Snapshot().HoldoffS != 0.2 {
		t.Fatalf("holdoff not reflected in stats: %v", e.Snapshot().HoldoffS)
	}
	// A triggered frame is held off by ~200 ms before the next arm.
	start := e.clk.Now()
	e.paceHold(start, true)
	if d := e.clk.Now().Sub(start); d < 200*time.Millisecond {
		t.Fatalf("triggered holdoff not applied: waited %v, want ≥200ms", d)
	}
	// An untriggered/AUTO frame is NOT held off (frame period is 0).
	start = e.clk.Now()
	e.paceHold(start, false)
	if d := e.clk.Now().Sub(start); d != 0 {
		t.Fatalf("untriggered frame held off %v, want 0", d)
	}
	// Clamp to [0,10] s and 0 disables.
	if got := e.SetHoldoff(-1); got != 0 {
		t.Fatalf("negative holdoff = %v, want 0", got)
	}
	if got := e.SetHoldoff(99); got != 10 {
		t.Fatalf("holdoff clamp = %v, want 10", got)
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
