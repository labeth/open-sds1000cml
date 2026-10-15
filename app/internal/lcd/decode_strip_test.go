package lcd

import (
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// TestDecodeStripClearsTrace: with a protocol decoded, the Y-T plot ends
// above the decode strip in every workspace, so a trace low on the screen
// (here 3.5 divisions under the centre, where the strip covered it) is drawn
// above the decoded bytes, and the -4 division row sits just over the strip.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func TestDecodeStripClearsTrace(t *testing.T) {
	const n = 1024
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, EdgeX: n / 2,
		Ptp: 0, TdivS: 100e-6, SampleS: 1e-6, Trigd: true}
	for i := range f.C1 {
		f.C1[i] = 128 - 88 // -3.52 div
	}
	for _, top := range []int{0, 104, 136, 142} {
		h := defaultHUD()
		h.TdivS, h.SampleS, h.TrigPosFrac = 100e-6, 1e-6, 0.5
		h.DecProto, h.DecBaud = 2, 9600
		h.ShowC2, h.ShowC1, h.ChanExplicit = false, true, true
		if top > 0 {
			h.UI.Layout = wsview.Layout{MenuW: 30, PlotTop: top}
		}
		p := plotOf(h.UI.Layout).aboveStrip()
		if p.sy(28) >= decodeStripTop || p.last() >= decodeStripTop {
			t.Fatalf("plot top %d: -4 div at row %d, plot ends %d: in the strip from %d", top, p.sy(28), p.last(), decodeStripTop)
		}
		sf := NewMemSurface()
		Render(sf, f, h, true)
		x, found := 300, -1
		for y := 12; y < decodeStripTop; y++ {
			if sf.At(x, y) == colC1 {
				found = y
			}
		}
		if found < 0 {
			t.Errorf("plot top %d: the trace at -3.5 div is not above the decode strip (column %d)", top, x)
		}
		h.DecProto = 0
		if q := plotOf(h.UI.Layout); decodeStrip(h) || q.last() != H-1 {
			t.Errorf("plot top %d: no decode, yet the plot ends at %d", top, q.last())
		}
	}
}
