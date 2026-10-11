// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "open-sds/app/internal/panel/wsview"

// plotArea is the rectangle the waveform views draw in (REQ-SDS-211): the
// full screen when no UI is laid out, else left of the right column (the
// 30 px pictogram rail or a menu's F-key column) and below a workspace's
// pane. top and bot are the rows ±4 divisions map to.
type plotArea struct {
	w, y0, h int // x runs 0..w-1, y y0..y0+h-1
	top, bot int // the trace's +4 and -4 division rows
}

// plotOf is the plot area a layout leaves. With no pane the trace keeps the
// full-screen mapping (rows 8..476, clear of the status bar); under a pane
// the eight divisions span the plot exactly, aligned with its graticule.
// TRLC-LINKS: REQ-SDS-211
func plotOf(l wsview.Layout) plotArea {
	p := plotArea{w: W - max(0, min(l.MenuW, W-200)), y0: max(0, min(l.PlotTop, 300))}
	p.h = H - p.y0
	if p.y0 == 0 {
		p.top, p.bot = traceTop, traceBot
	} else {
		p.top, p.bot = p.y0, H-1
	}
	return p
}

// fullPlot is the whole screen (thumbnails and tests).
var fullPlot = plotArea{w: W, y0: 0, h: H, top: traceTop, bot: traceBot}

// sy maps a display code to a row: higher code = higher on screen; 25
// codes/div, code 128 at the centre (spec 07 §3.4, spec 10 §7.1).
// TRLC-LINKS: REQ-SDS-021
func (p plotArea) sy(v float64) int {
	y := p.bot - int(((v-128)/200+0.5)*float64(p.bot-p.top)+0.5)
	return min(max(y, p.y0), H-1)
}

// gridY is the row of graticule line r (0..8, top to bottom).
// TRLC-LINKS: REQ-SDS-021
func (p plotArea) gridY(r int) int { return p.y0 + r*(p.h-1)/8 }

// gridX is the column of graticule line c (0..10, left to right).
// TRLC-LINKS: REQ-SDS-021
func (p plotArea) gridX(c int) int { return c * (p.w - 1) / 10 }
