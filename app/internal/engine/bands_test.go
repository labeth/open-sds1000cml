package engine

import (
	"math"
	"testing"
)

func TestPlanTdivLadder(t *testing.T) {
	cases := []struct {
		tdiv       float64
		class      uint16
		lo         uint16
		nativeFast bool
		drain      int
		winCols    int
	}{
		{1e-9, 0x20, 0x0000, true, deepRecord, 10},
		{25e-9, 0x20, 0x0000, true, deepRecord, 250},
		{200e-9, 0x20, 0x0000, true, deepRecord, 2000},
		{500e-9, 0x01, 0x0000, true, deepRecord, 1250},
		{1e-6, 0x01, 0x0000, true, deepRecord, 2500},
		{2e-6, 0x80, 0x0001, true, deepRecord, 2000},
		{20e-6, 0x80, 0x0004, true, deepRecord, 5000},
		{50e-6, 0x80, 0x0008, false, decimDrain, 2048},
		{500e-6, 0x80, 0x0050, false, decimDrain, 2048},
		{2e-3, 0x80, 0x0190, false, decimDrain, 2048},
	}
	for _, c := range cases {
		b, ok := PlanTdiv(c.tdiv)
		if !ok {
			t.Fatalf("PlanTdiv(%g) not found", c.tdiv)
		}
		if b.Class != c.class || b.Lo != c.lo {
			t.Errorf("PlanTdiv(%g) = class %#02x lo %#04x, want %#02x/%#04x",
				c.tdiv, b.Class, b.Lo, c.class, c.lo)
		}
		if b.NativeFast() != c.nativeFast {
			t.Errorf("PlanTdiv(%g).NativeFast = %v, want %v", c.tdiv, b.NativeFast(), c.nativeFast)
		}
		if b.DrainCols() != c.drain {
			t.Errorf("PlanTdiv(%g).DrainCols = %d, want %d", c.tdiv, b.DrainCols(), c.drain)
		}
		if b.WinCols() != c.winCols {
			t.Errorf("PlanTdiv(%g).WinCols = %d, want %d", c.tdiv, b.WinCols(), c.winCols)
		}
	}
}

func TestPlanTdivRejectsOffLadder(t *testing.T) {
	for _, v := range []float64{20e-9, 0, 3.3e-6, 7e-3, 100} {
		if _, ok := PlanTdiv(v); ok {
			t.Errorf("PlanTdiv(%g) accepted, want reject (not a detent)", v)
		}
	}
}

func TestPlanTdivTolerance(t *testing.T) {
	// Float round-trips through JSON must still hit rows.
	if _, ok := PlanTdiv(500e-6 * (1 + 5e-7)); !ok {
		t.Error("1e-6 relative tolerance not honoured")
	}
}

func TestDisplayedSdiv(t *testing.T) {
	// Class 0x20: displayed equals the label (the 1 ns nominal sizing).
	b, _ := PlanTdiv(5e-9)
	if got := b.DisplayedSdivS(); math.Abs(got-5e-9) > 1e-15 {
		t.Errorf("displayed(5ns) = %g, want 5e-9", got)
	}
	// Decimated: the 2048 clamp changes the on-screen s/div (spec 04 §2).
	b, _ = PlanTdiv(50e-6)
	if got := b.DisplayedSdivS(); math.Abs(got-16.384e-6) > 1e-12 {
		t.Errorf("displayed(50µs) = %g, want 16.384µs", got)
	}
	b, _ = PlanTdiv(1e-3)
	if got := b.DisplayedSdivS(); math.Abs(got-409.6e-6) > 1e-12 {
		t.Errorf("displayed(1ms) = %g, want 409.6µs", got)
	}
}

func TestWaitBudgetClamp(t *testing.T) {
	b, _ := PlanTdiv(1e-6) // native-fast → floors at 40 ms
	if got := b.WaitBudgetNs(); got != 40e6 {
		t.Errorf("budget(1µs) = %d, want 40ms floor", got)
	}
	b, _ = PlanTdiv(2e-3) // 3·4µs·512 = 6.1ms → still floors
	if got := b.WaitBudgetNs(); got != 40e6 {
		t.Errorf("budget(2ms) = %d, want 40ms floor", got)
	}
}

func TestSupportedTdivsAscending(t *testing.T) {
	td := SupportedTdivs()
	if len(td) != 33 {
		t.Fatalf("ladder has %d rows, want 33", len(td))
	}
	for i := 1; i < len(td); i++ {
		if td[i] <= td[i-1] {
			t.Fatalf("ladder not ascending at %d: %g after %g", i, td[i], td[i-1])
		}
	}
}
