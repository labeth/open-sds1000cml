// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// A record denser than the screen draws each column's full min..max: a
// one-sample spike in a 1 M-sample stopped record stays on screen.
// TRLC-LINKS: REQ-SDS-021
func TestDenseTraceKeepsSpikes(t *testing.T) {
	sig := make([]uint8, 1<<20)
	for i := range sig {
		sig[i] = 128
	}
	spike := 123457 // one sample, between two columns' sample points
	sig[spike] = 228
	sf := NewMemSurface()
	win := len(sig)
	drawTrace(sf, sig, win, float64(win)/2, false, 0xffff, .5)
	x := spike * W / win
	top := sampleToY(228)
	found := false
	for dx := -1; dx <= 1; dx++ {
		if sf.At(x+dx, top) == 0xffff {
			found = true
		}
	}
	if !found {
		t.Fatalf("spike at sample %d (column %d) not drawn up to y=%d", spike, x, top)
	}
}

// Running, a frame from another timebase is not drawn under the new label.
// TRLC-LINKS: REQ-SDS-021
func TestStaleTimebaseFrameNotDrawn(t *testing.T) {
	f := &engine.Frame{Seq: 1, C1: make([]uint8, 2048), C2: make([]uint8, 2048), Valid: 2048, WinCols: 2048, SampleS: 1e-3, TdivS: 0.1, EdgeX: -1}
	for i := range f.C1 {
		f.C1[i], f.C2[i] = 200, 200
	}
	count := func(hud HUD) int {
		sf := NewMemSurface()
		Render(sf, f, hud, true)
		n := 0
		for x := 0; x < W; x++ {
			if sf.At(x, sampleToY(200)) == colC1 {
				n++
			}
		}
		return n
	}
	hud := HUD{Running: true, TdivS: 0.1, C1VdivV: 1, C2VdivV: 1, TwoChan: true, RecordS: 137}
	if count(hud) < W/2 {
		t.Fatal("matching frame not drawn")
	}
	hud.TdivS = 10
	if n := count(hud); n != 0 {
		t.Fatalf("stale frame drawn across %d columns", n)
	}
}

// pixelSurface is a Surface that is not a MemSurface: drawing on it takes the
// per-pixel paths.
type pixelSurface struct{ m *MemSurface }

// TRLC-LINKS: REQ-SDS-021
func (s pixelSurface) SetPixel(x, y int, c uint16) { s.m.SetPixel(x, y, c) }

// TRLC-LINKS: REQ-SDS-021
func (s pixelSurface) Fill(c uint16) { s.m.Fill(c) }

// TRLC-LINKS: REQ-SDS-021
func (s pixelSurface) At(x, y int) uint16 { return s.m.At(x, y) }

// A dense trace filled in blocks (the rows all of a block's columns cover a
// row at a time) sets the same pixels as each column drawn down its span: held
// stretches, quiet and full-height columns, zoom and the plot's width included.
// TRLC-LINKS: REQ-SDS-021
func TestDenseTraceBlockFillMatchesColumns(t *testing.T) {
	sig := make([]uint8, 40000)
	for i := range sig {
		sig[i] = uint8(60 + (i*7919)%140) // fast, full-height columns
		if i/3000%2 == 1 {
			sig[i] = uint8(120 + i%9) // and quiet stretches
		}
	}
	held := make([]bool, len(sig))
	for i := 9000; i < 14000; i++ {
		held[i] = true
	}
	for _, c := range []struct {
		p    plotArea
		z    float64
		held []bool
		left float64
	}{{fullPlot, 1, nil, 0}, {plotOf(wsview.Layout{MenuW: 116, PlotTop: 120}), 2, held, -500}, {plotOf(wsview.Layout{MenuW: 116, PlotTop: 120}), 0.5, held, 1200}} {
		rows, cols := NewMemSurface(), NewMemSurface()
		drawDenseTrace(rows, c.p, traceID{1, 1, 0, false}, sig, c.held, c.left, len(sig), colC1, c.z)
		drawDenseTrace(pixelSurface{cols}, c.p, traceID{1, 1, 0, false}, sig, c.held, c.left, len(sig), colC1, c.z)
		drawn := 0
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				a, b := rows.At(x, y), cols.At(x, y)
				if a != b {
					t.Fatalf("plot %d wide, zoom %v: pixel (%d,%d) %#x by rows, %#x by columns", c.p.w, c.z, x, y, a, b)
				}
				if a != 0 {
					drawn++
				}
			}
		}
		if drawn < 20000 {
			t.Fatalf("plot %d wide: only %d pixels drawn", c.p.w, drawn)
		}
	}
}

// The min/max envelope is filled the same way: the same pixels as a column at
// a time, columns past the envelope's data left empty.
// TRLC-LINKS: REQ-SDS-021
func TestEnvelopeBlockFillMatchesColumns(t *testing.T) {
	mn, mx := make([]uint8, 700), make([]uint8, 700)
	for i := range mn {
		mn[i], mx[i] = uint8(70+i%6), uint8(190-i%5)
		if i/150%2 == 1 {
			mn[i], mx[i] = uint8(120+i%3), uint8(126+i%4) // a quiet stretch
		}
	}
	for _, cols := range []int{700, 800} { // 800: the last columns have no data
		rows, each := NewMemSurface(), NewMemSurface()
		drawEnvelope(rows, fullPlot, mn, mx, cols, colC2, 1)
		drawEnvelope(pixelSurface{each}, fullPlot, mn, mx, cols, colC2, 1)
		drawn := 0
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if a, b := rows.At(x, y), each.At(x, y); a != b {
					t.Fatalf("%d columns: pixel (%d,%d) %#x in blocks, %#x by columns", cols, x, y, a, b)
				} else if a != 0 {
					drawn++
				}
			}
		}
		if drawn < 20000 {
			t.Fatalf("%d columns: only %d pixels drawn", cols, drawn)
		}
	}
}
