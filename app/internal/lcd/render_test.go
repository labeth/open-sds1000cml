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

// Persistence: rendering two frames with the trace at different vertical
// positions must leave BOTH on the persist layer — the previous one faded (but
// still non-black) and the current one bright — i.e. an afterglow.
func TestRenderPersistence(t *testing.T) {
	persist := NewMemSurface()
	sf := NewMemSurface()
	h := defaultHUD()
	h.Persist = true
	h.ShowC1, h.ShowC2 = true, false
	flat := func(v uint8) *engine.Frame {
		f := testFrame(2048)
		for i := range f.C1 {
			f.C1[i] = v
		}
		return f
	}
	rowHasInk := func(m *MemSurface, y int) bool {
		for x := 0; x < W-160; x++ {
			if m.At(x, y) != 0 {
				return true
			}
		}
		return false
	}
	Render(sf, flat(210), h, true, persist) // high trace
	Render(sf, flat(46), h, true, persist)  // low trace (different position)
	if !rowHasInk(persist, sampleToY(210)) {
		t.Error("persistence lost the previous (faded) trace — no afterglow")
	}
	if !rowHasInk(persist, sampleToY(46)) {
		t.Error("persistence missing the current trace")
	}
	// With persistence OFF, the persist layer is untouched (no fade/draw).
	blank := NewMemSurface()
	h.Persist = false
	Render(sf, flat(128), h, true, blank)
	if rowHasInk(blank, sampleToY(128)) {
		t.Error("persistence off should not draw onto the persist layer")
	}
}

// The alternate views (web parity) must each render distinct output: X-Y and
// math draw the purple math colour; FFT draws a spectrum; the autoset banner
// draws its amber box centred.
func TestRenderAltViews(t *testing.T) {
	f := testFrame(2048)
	f.C2 = make([]uint8, len(f.C2))
	for i := range f.C2 { // give C2 a ramp so X-Y/math aren't degenerate
		f.C2[i] = uint8(i % 200)
	}

	xy := NewMemSurface()
	h := defaultHUD()
	h.ViewMode = 1
	Render(xy, f, h, true)
	if countColor(xy, colMath) == 0 {
		t.Error("X-Y view drew no math-coloured points")
	}

	fft := NewMemSurface()
	h = defaultHUD()
	h.ViewMode = 2
	Render(fft, f, h, true)
	// FFT overlays BOTH enabled channels' spectra (both colours present).
	if countColor(fft, colC1) == 0 {
		t.Error("FFT view drew no C1 spectrum")
	}
	if countColor(fft, colC2) == 0 {
		t.Error("FFT view drew no C2 spectrum (should overlay both channels)")
	}
	// No trigger-level dashed line / ground arrows should intrude on the FFT plot
	// BODY (below the top HUD bar, which legitimately shows the trigger state).
	if countColorIn(fft, colTrig, 0, 40, W-140, traceBot-20) > 20 {
		t.Error("trigger/ground markers drawn over the FFT plot")
	}
	// Sanity: the SAME markers DO appear in the Y-T plot body (so the test isn't
	// just checking an always-empty region).
	yt2 := NewMemSurface()
	hy := defaultHUD()
	hy.TrigLvlDiv = 0 // centre dashed line
	Render(yt2, f, hy, true)
	if countColorIn(yt2, colTrig, 0, 40, W-140, traceBot-20) < 40 {
		t.Error("expected trigger markers in the Y-T plot body (test region wrong)")
	}

	math := NewMemSurface()
	h = defaultHUD()
	h.MathMode = 1 // C1+C2
	Render(math, f, h, true)
	if countColor(math, colMath) == 0 {
		t.Error("math overlay drew no math-coloured trace")
	}

	banner := NewMemSurface()
	h = defaultHUD()
	h.AutosetBusy = true
	Render(banner, f, h, true)
	// the banner box is centred; assert amber pixels in the middle region.
	if countColorIn(banner, colTrig, W/2-160, H/2-30, W/2+160, H/2+30) == 0 {
		t.Error("autoset banner not drawn centre-screen")
	}
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

