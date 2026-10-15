package lcd

import (
	"testing"

	"open-sds/app/internal/panel/wsview"
)

// A cursor whose signal time or level is off the screen keeps its value and
// is drawn as an arrow at the side it is beyond, not as a line pinned to the
// edge; one on the screen is its dashed line.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-217
func TestCursorOffScreenArrows(t *testing.T) {
	h := defaultHUD()
	h.UI.Layout = wsview.Layout{MenuW: 30, PlotTop: 142}
	p := plotOf(h.UI.Layout)
	h.CurOn, h.CurType, h.CurSel = true, 0, 0
	h.CurX = [2]float64{-0.4, 1.7} // A left of the screen, B right of it
	sf := NewMemSurface()
	drawCursors(sf, p, h)
	if sf.At(0, p.top+30) != colTrig || sf.At(p.w-1, p.top+44) != colDim {
		t.Fatalf("time cursors off the screen: arrow tips %#x (A, left) and %#x (B, right)", sf.At(0, p.top+30), sf.At(p.w-1, p.top+44))
	}
	for _, x := range []int{0, 1, p.w - 1, p.w - 2} { // no line pinned to an edge
		lit := 0
		for y := p.top + 60; y <= p.bot; y++ {
			if sf.At(x, y) != 0 {
				lit++
			}
		}
		if lit > 0 {
			t.Fatalf("column %d holds %d cursor pixels: a line pinned to the edge", x, lit)
		}
	}
	h.CurType, h.CurY = 1, [2]float64{-0.1, 0.5} // A above, B on the screen
	sf = NewMemSurface()
	drawCursors(sf, p, h)
	if x := p.w/2 + 60; sf.At(x, p.y0) != colTrig {
		t.Fatalf("volts cursor A above the screen: no arrow at the top (%#x)", sf.At(x, p.y0))
	}
	if y := p.top + int(0.5*float64(p.bot-p.top)); sf.At(0, y) != colDim {
		t.Fatalf("volts cursor B on the screen: no dashed line at row %d", y)
	}
}
