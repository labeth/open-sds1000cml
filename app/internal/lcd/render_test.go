package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
)

func testFrame(valid int) *engine.Frame {
	f := &engine.Frame{
		C1: make([]uint8, valid), C2: make([]uint8, valid),
		EnvMin: make([]uint8, 800), EnvMax: make([]uint8, 800),
		EnvMin2: make([]uint8, 800), EnvMax2: make([]uint8, 800),
		Valid: valid, WinCols: valid, EdgeX: float64(valid) / 2,
		Ptp: 144, Seq: 1, TdivS: 500e-6, SampleS: 800e-9,
	}
	for i := range f.C1 {
		if (i/128)%2 == 0 {
			f.C1[i] = 200
		} else {
			f.C1[i] = 56
		}
		f.C2[i] = 128
	}
	return f
}

func defaultHUD() HUD {
	return HUD{C1VdivV: 1, C2VdivV: 1, TdivS: 500e-6, TrigRising: true,
		Running: true, SampleS: 800e-9, TwoChan: true}
}

func countColor(m *MemSurface, c uint16) int {
	n := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if m.At(x, y) == c {
				n++
			}
		}
	}
	return n
}

func countColorIn(m *MemSurface, c uint16, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if m.At(x, y) == c {
				n++
			}
		}
	}
	return n
}

// The selected softkey is a FILLED inverted amber bar (not a 1px outline): its
// slot must be a solid block of colTrig with dark (colBG) inverted text on top.
func TestRenderMenuSelectedSoftkeyFilled(t *testing.T) {
	m := NewMemSurface()
	h := defaultHUD()
	h.MenuOpen = true
	h.MenuTitle = "TRIGGER"
	h.MenuItems = []MenuItem{{"Mode", "AUTO"}, {"Slope", "Rise"}, {"Source", "C1"}}
	h.MenuSel = 1 // second slot selected → sy = 40 + (444-40)*1/5 = 120
	Render(m, testFrame(2048), h, true)
	// slot i=1 bar: y in [118,136], x in [687,797]
	fill := countColorIn(m, colTrig, 687, 118, 797, 137)
	if fill < 800 {
		t.Fatalf("selected softkey should be a FILLED amber bar, got only %d colTrig px in the slot", fill)
	}
	if inv := countColorIn(m, colBG, 687, 118, 797, 137); inv < 10 {
		t.Fatalf("selected softkey text should be inverted (dark on amber), got %d colBG px in the bar", inv)
	}
}

func TestRenderMeasPanel(t *testing.T) {
	box := rgb(6, 10, 22) // drawMeasPanel background fill
	// Off: no MEASURE panel box.
	off := NewMemSurface()
	Render(off, testFrame(2048), defaultHUD(), true)
	if countColor(off, box) != 0 {
		t.Fatal("MEASURE panel drawn while ShowMeas is off")
	}
	// On: the panel box + calibrated text appear at the top-left.
	h := defaultHUD()
	h.ShowMeas = true
	on := NewMemSurface()
	Render(on, testFrame(2048), h, true)
	if countColor(on, box) < 200 {
		t.Fatalf("MEASURE panel box not drawn (%d fill px)", countColor(on, box))
	}
	found := false
	for x := 5; x < 112 && !found; x++ {
		for y := 24; y < 90; y++ {
			if on.At(x, y) == colInfo {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("MEASURE panel text not drawn")
	}
}

func TestRenderCursors(t *testing.T) {
	// Off: no cursor readout box.
	box := rgb(6, 10, 22)
	off := NewMemSurface()
	Render(off, testFrame(2048), defaultHUD(), true)
	baseline := countColor(off, box)

	// On, time cursors at 0.3/0.7: the readout box appears and the active
	// cursor (A) draws in the trigger colour, the other in the grid colour.
	h := defaultHUD()
	h.CurOn, h.CurType, h.CurSel = true, 0, 0
	h.CurX = [2]float64{0.3, 0.7}
	on := NewMemSurface()
	Render(on, testFrame(2048), h, true)
	if countColor(on, box) <= baseline {
		t.Fatal("cursor Δ readout box not drawn")
	}
	// The active vertical cursor column has trigger-colour dashes.
	fx := h.CurX[0]
	xA := int(fx * float64(W-1))
	n := 0
	for y := 8; y < 470; y++ {
		if on.At(xA, y) == colTrig {
			n++
		}
	}
	if n < 20 {
		t.Fatalf("active time cursor not drawn at x=%d (%d px)", xA, n)
	}
}

func TestRenderTraceFrame(t *testing.T) {
	m := NewMemSurface()
	Render(m, testFrame(2048), defaultHUD(), true)

	if n := countColor(m, colC1); n < 500 {
		t.Fatalf("C1 trace pixels = %d, want a real trace", n)
	}
	if n := countColor(m, colC2); n < 500 {
		t.Fatalf("C2 trace pixels = %d", n)
	}
	// Liveness strip green on rows 0-1 (fresh + real ptp).
	if m.At(400, 0) != colOK || m.At(400, 1) != colOK {
		t.Fatal("liveness strip not green on a fresh frame")
	}
	// Graticule centre cross uses the axis colour somewhere mid-screen.
	if m.At(W/2, 100) != colAxis && m.At((W-1)/2, 100) != colAxis {
		t.Fatalf("centre vertical axis missing (got %#04x)", m.At(W/2, 100))
	}
	// HUD text present (any colInfo pixels near the M readout).
	found := false
	for x := 200; x < 320 && !found; x++ {
		for y := 2; y < 10; y++ {
			if m.At(x, y) == colInfo {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatal("timebase readout not drawn")
	}
}

func TestRenderHeldFrameStripRed(t *testing.T) {
	m := NewMemSurface()
	Render(m, testFrame(2048), defaultHUD(), false) // held → red strip
	if m.At(400, 0) != colStale {
		t.Fatal("liveness strip not red on a held frame")
	}
	// The trace must still be drawn (never blank on hold).
	if n := countColor(m, colC1); n < 500 {
		t.Fatal("held frame blanked the trace")
	}
}

func TestRenderEnvelopeBranch(t *testing.T) {
	f := testFrame(4096)
	f.IsEnv = true
	f.EnvCols = 800
	for i := 0; i < 800; i++ {
		f.EnvMin[i], f.EnvMax[i] = 80, 180
		f.EnvMin2[i], f.EnvMax2[i] = 100, 140
	}
	m := NewMemSurface()
	Render(m, f, defaultHUD(), true)
	// The envelope fill covers a band: far more C1 pixels than a line trace.
	if n := countColor(m, colC1); n < 50000 {
		t.Fatalf("envelope fill pixels = %d, want a solid band", n)
	}
}

func TestRenderNilFrame(t *testing.T) {
	m := NewMemSurface()
	Render(m, nil, defaultHUD(), false)
	if m.At(400, 240) != colBG && m.At(400, 240) != colGrid && m.At(400, 240) != colAxis {
		t.Fatal("nil frame should render graticule over background only")
	}
}

func TestRenderFlatRailNoFabrication(t *testing.T) {
	f := testFrame(2048)
	for i := range f.C1 {
		f.C1[i] = 128
		f.C2[i] = 128
	}
	f.EdgeX = -1
	f.Ptp = 0
	m := NewMemSurface()
	Render(m, f, defaultHUD(), true)
	// Flat rail draws at mid-screen; strip red (ptp < 8 even though fresh).
	if m.At(400, 0) != colStale {
		t.Fatal("flat rail should show a red strip")
	}
	y := sampleToY(128)
	foundMid := false
	for x := 300; x < 500; x++ {
		if m.At(x, y) == colC1 || m.At(x, y-1) == colC1 || m.At(x, y+1) == colC1 {
			foundMid = true
			break
		}
	}
	if !foundMid {
		t.Fatal("flat rail not drawn at mid-level")
	}
}

func TestFontMetrics(t *testing.T) {
	if TextWidth("ABC", 1) != 18 {
		t.Fatalf("TextWidth(ABC) = %d, want 18", TextWidth("ABC", 1))
	}
	m := NewMemSurface()
	DrawText(m, 10, 10, "A", colInfo, 1)
	n := 0
	for y := 10; y < 17; y++ {
		for x := 10; x < 15; x++ {
			if m.At(x, y) == colInfo {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("glyph A drew nothing")
	}
}

func TestFormatters(t *testing.T) {
	if got := fmtTdiv(500e-6); got != "500us" {
		t.Fatalf("fmtTdiv(500µs) = %q", got)
	}
	if got := fmtTdiv(2); got != "2s" {
		t.Fatalf("fmtTdiv(2s) = %q", got)
	}
	if got := fmtVolt(0.5); got != "500mV" {
		t.Fatalf("fmtVolt(0.5) = %q", got)
	}
	if got := fmtFreq(1000); got != "1kHz" {
		t.Fatalf("fmtFreq(1000) = %q", got)
	}
}

