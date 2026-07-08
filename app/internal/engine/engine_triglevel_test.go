package engine

import "testing"

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
