package engine

import (
	"testing"

	"open-sds/app/internal/iface"
)

func TestTriggerLevelRecommit(t *testing.T) {
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.bringUp()
	e.SetTrigLevelCode(0x7530)
	fb.clearWrites()
	e.serviceCommands()
	// The MAX V comparator DAC quad (lanes A+B, same code, hi self-latches),
	// then the fabric's TRIG_LEVEL in sample codes (the same display-code
	// mapping the software anchor uses), then the re-arm. ACQ_CTRL is
	// unchanged (source/slope did not move) and is not rewritten.
	lvl := e.trigDispLevel(0)
	if lvl < 0 || lvl == trigLevelUnset {
		t.Fatalf("display level for 0x7530 = %d", lvl)
	}
	wantWrites(t, fb.snapWrites(), []wr{
		{3, cs3LevelALo, 0x30}, {3, cs3LevelAHi, 0x75},
		{3, cs3LevelBLo, 0x30}, {3, cs3LevelBHi, 0x75},
		{1, iface.SelTrigLevel, uint16(lvl)},
		{1, iface.SelOpcode, iface.OpGo},
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

	// Source/slope changes reach ACQ_CTRL at the next boundary (and re-arm);
	// the HW_SEL knob reaches TRIG_LEVEL.
	fb.clearWrites()
	e.SetTrigSource(1)
	e.SetTrigSlope(false)
	e.serviceCommands()
	wantWrites(t, fb.snapWrites(), []wr{
		{1, iface.SelAcqCtrl, acqDefault | iface.AcqCtrlTrigSrcMask | iface.AcqCtrlTrigSlopeMask},
		{1, iface.SelOpcode, iface.OpGo},
	})
	fb.clearWrites()
	e.tuneHwTrig.Store(true)
	e.serviceCommands()
	got := fb.snapWrites()
	if len(got) != 2 || got[0].sel != iface.SelTrigLevel || got[0].val&iface.TrigLevelHwSelMask == 0 {
		t.Fatalf("HW_SEL not applied: %#v", got)
	}
	// Stopped: the words still flush, but nothing re-arms.
	e.SetRunning(false)
	e.tuneHwTrig.Store(false)
	fb.clearWrites()
	e.serviceCommands()
	got = fb.snapWrites()
	if len(got) != 1 || got[0].sel != iface.SelTrigLevel {
		t.Fatalf("stopped flush: %#v", got)
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
	// Offsets: C1 lo/hi, C2 lo/hi (MAX V DACs) — then the level quad.
	want := []wr{
		{3, cs3OffC1Lo, 0x68}, {3, cs3OffC1Hi, 0x29},
		{3, cs3OffC2Lo, 0x30}, {3, cs3OffC2Hi, 0x2A},
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
	e.bringUp()
	fb.clearWrites()
	if got := e.SetTrigLevelCode(0); got != 0 {
		t.Fatalf("SetTrigLevelCode(0) = %d, want 0", got)
	}
	e.serviceCommands()
	if n := len(fb.snapWrites()); n != 0 {
		t.Fatalf("code 0 emitted %d writes, want none (inherited comparator kept)", n)
	}
}
