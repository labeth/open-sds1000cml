package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
)

// traceRows scans the mid-screen window (clear of the top-bar channel labels,
// the left-edge ground markers and the right-edge trigger handle) and returns
// the min/max row holding a pixel of the given trace colour.
func traceRows(sf *MemSurface, col uint16) (minY, maxY int, found bool) {
	minY, maxY = H, -1
	for y := 16; y <= 450; y++ {
		for x := 40; x <= 760; x++ {
			if sf.At(x, y) == col {
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	return minY, maxY, maxY >= 0
}

func flatFrame(v1, v2 uint8) *engine.Frame {
	const n = 2048
	f := &engine.Frame{
		C1: make([]uint8, n), C2: make([]uint8, n),
		Valid: n, WinCols: n, Seq: 1, EdgeX: -1, // flat capture → centre on the record
		TdivS: 500e-6, DisplayedS: 500e-6, SampleS: 800e-9, Ptp: 0,
	}
	for i := 0; i < n; i++ {
		f.C1[i], f.C2[i] = v1, v2
	}
	return f
}

// TestRenderINVSFlipsTrace pins the LCD half of the display-level INVS
// contract: with HUD.Inv set, the rendered Y-T trace is mirrored about the
// display centre — y(v) becomes y(255−v) — for BOTH channels, independently,
// while nothing else about the render is inverted.
func TestRenderINVSFlipsTrace(t *testing.T) {
	f := flatFrame(168, 88) // C1 above centre, C2 below — asymmetric on purpose
	hud := HUD{
		C1VdivV: 1, C2VdivV: 1, TdivS: 500e-6, TrigPosFrac: 0.5,
		TwoChan: true, ShowC1: true, ShowC2: true, Running: true,
	}

	render := func(h HUD) *MemSurface {
		sf := NewMemSurface()
		Render(sf, f, h, true)
		return sf
	}

	// Baseline: each flat trace sits on its sampleToY row.
	sf := render(hud)
	for _, c := range []struct {
		col  uint16
		code uint8
	}{{colC1, 168}, {colC2, 88}} {
		lo, hi, ok := traceRows(sf, c.col)
		if !ok || lo != hi || lo != sampleToY(float64(c.code)) {
			t.Fatalf("baseline code %d: rows [%d,%d] ok=%v, want the single row %d",
				c.code, lo, hi, ok, sampleToY(float64(c.code)))
		}
	}

	// Inv1 only: C1 mirrors to 255−168, C2 stays put.
	hud.Inv1 = true
	sf = render(hud)
	if lo, hi, ok := traceRows(sf, colC1); !ok || lo != hi || lo != sampleToY(255-168) {
		t.Fatalf("Inv1 C1: rows [%d,%d] ok=%v, want %d", lo, hi, ok, sampleToY(255-168))
	}
	if lo, _, ok := traceRows(sf, colC2); !ok || lo != sampleToY(88) {
		t.Fatalf("Inv1 must not move C1's neighbour: C2 row %d, want %d", lo, sampleToY(88))
	}

	// Both inverted: C2 mirrors too.
	hud.Inv2 = true
	sf = render(hud)
	if lo, hi, ok := traceRows(sf, colC2); !ok || lo != hi || lo != sampleToY(255-88) {
		t.Fatalf("Inv2 C2: rows [%d,%d] ok=%v, want %d", lo, hi, ok, sampleToY(255-88))
	}
}

// TestRenderINVSFlipsEnvelope pins the envelope (peak-detect/roll) path: the
// inverted band's rows are the mirror of the upright band's codes.
func TestRenderINVSFlipsEnvelope(t *testing.T) {
	const n = 400
	f := &engine.Frame{
		C1: make([]uint8, 8), C2: make([]uint8, 8), Valid: 8, Seq: 1,
		IsEnv: true, EnvCols: n,
		EnvMin: make([]uint8, n), EnvMax: make([]uint8, n),
		EnvMin2: make([]uint8, n), EnvMax2: make([]uint8, n),
		TdivS: 0.1, DisplayedS: 0.1,
	}
	for i := 0; i < n; i++ {
		f.EnvMin[i], f.EnvMax[i] = 100, 150
		f.EnvMin2[i], f.EnvMax2[i] = 60, 70
	}
	hud := HUD{
		C1VdivV: 1, C2VdivV: 1, TdivS: 0.1, TrigPosFrac: 0.5,
		TwoChan: true, ShowC1: true, ShowC2: true, Running: true,
	}
	sf := NewMemSurface()
	Render(sf, f, hud, true)
	lo, hi, ok := traceRows(sf, colC1)
	if !ok || lo != sampleToY(150) || hi != sampleToY(100) {
		t.Fatalf("baseline env band [%d,%d] ok=%v, want [%d,%d]",
			lo, hi, ok, sampleToY(150), sampleToY(100))
	}

	hud.Inv1 = true
	sf = NewMemSurface()
	Render(sf, f, hud, true)
	lo, hi, ok = traceRows(sf, colC1)
	if !ok || lo != sampleToY(255-100) || hi != sampleToY(255-150) {
		t.Fatalf("inverted env band [%d,%d] ok=%v, want [%d,%d]",
			lo, hi, ok, sampleToY(255-100), sampleToY(255-150))
	}
	// The un-inverted neighbour is untouched.
	if lo, hi, ok = traceRows(sf, colC2); !ok || lo != sampleToY(70) || hi != sampleToY(60) {
		t.Fatalf("Inv1 must not move C2's env band: [%d,%d] ok=%v", lo, hi, ok)
	}
}
