// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"

	"open-sds/app/internal/streamview"
)

// streamFullScreen reports the transcript replacing the waveform: a running
// or failed stream, or a stopped one with its list open
// (ADR-PANEL-DECODED-STREAM).
// TRLC-LINKS: REQ-SDS-018
func streamFullScreen(hud HUD) bool {
	return hud.StreamOn && hud.StreamList
}

// streamStatus is the stream's one-line status.
// TRLC-LINKS: REQ-SDS-018
func streamStatus(v streamview.View) string {
	if v.State == streamview.Starting && v.Note != "" { // no counts yet: why it takes seconds
		return fmt.Sprintf("STREAM %s  %s  %s", v.Label, v.State, v.Note)
	}
	s := fmt.Sprintf("STREAM %s  %s  %d units  %s/s  err %d  lost %d", v.Label, v.State, v.Units,
		siScale(v.UnitsPerSecond, []siUnit{{1e6, "M"}, {1e3, "k"}, {1, ""}}), v.Errors, v.Lost)
	if v.Err != "" {
		s += "  " + v.Err
	}
	return s
}

// drawStream fills the plot with the hexdump transcript: status, column
// header, the page of lines, and on a stopped or scrolled-back stream where
// the page sits and which controls move it.
// TRLC-LINKS: REQ-SDS-018
func drawStream(sf Surface, p plotArea, hud HUD) {
	v, pg := hud.Stream, hud.StreamPage
	fillRect(sf, 0, p.y0+14, p.w, H-28-p.y0, colBoxBg)
	col := colInfo
	if v.State == streamview.Failed {
		col = colStale
	}
	DrawText(sf, 6, p.y0+18, streamStatus(v), col, 1)
	DrawText(sf, 6, p.y0+30, pg.Header, colDim, 1)
	const lineH = 10
	for i, l := range pg.Lines {
		y := p.y0 + 42 + i*lineH
		if y > H-36 {
			break
		}
		c := colC1
		if pg.Top+i == pg.TriggerLine {
			c = colTrig
		}
		DrawText(sf, 6, y, l, c, 1)
	}
	last := pg.Top + len(pg.Lines)
	switch {
	case v.State == streamview.Streaming && last < pg.Total: // scrolled back: held there
		DrawText(sf, 6, H-24, fmt.Sprintf("lines %d-%d of %d   push Position: follow the newest", pg.Top+1, last, pg.Total), colDim, 1)
	case v.State != streamview.Streaming && v.State != streamview.Failed:
		DrawText(sf, 6, H-24, fmt.Sprintf("lines %d-%d of %d   Position pushed: scroll   Zoom: wave   Run/Stop: stream again",
			pg.Top+1, last, pg.Total), colDim, 1)
	}
}

// drawStreamStrip overlays the transcript lines around the trigger on the
// recalled record's waveform.
// TRLC-LINKS: REQ-SDS-018
func drawStreamStrip(sf Surface, p plotArea, hud HUD) {
	v, pg := hud.Stream, hud.StreamPage
	const lineH = 10
	fillRect(sf, 0, p.y0+14, p.w, 26+lineH*len(pg.Lines), colBoxBg)
	DrawText(sf, 6, p.y0+16, streamStatus(v)+"   Zoom: list", colInfo, 1)
	DrawText(sf, 6, p.y0+26, pg.Header, colDim, 1)
	for i, l := range pg.Lines {
		c := colC1
		if pg.Top+i == pg.TriggerLine {
			c = colTrig
		}
		DrawText(sf, 6, p.y0+36+i*lineH, l, c, 1)
	}
}
