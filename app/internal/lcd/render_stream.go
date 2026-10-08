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
	s := fmt.Sprintf("STREAM %s  %s  %d units  %s/s  err %d  lost %d", v.Label, v.State, v.Units,
		siScale(v.UnitsPerSecond, []siUnit{{1e6, "M"}, {1e3, "k"}, {1, ""}}), v.Errors, v.Lost)
	if v.Err != "" {
		s += "  " + v.Err
	}
	return s
}

// drawStream fills the graticule with the hexdump transcript: status, column
// header, the page of lines, and on a stopped stream where the page sits and
// which controls move it.
// TRLC-LINKS: REQ-SDS-018
func drawStream(sf Surface, hud HUD) {
	v, p := hud.Stream, hud.StreamPage
	fillRect(sf, 0, 14, W, H-28, rgb(4, 8, 16))
	col := colInfo
	if v.State == streamview.Failed {
		col = colStale
	}
	DrawText(sf, 6, 18, streamStatus(v), col, 1)
	DrawText(sf, 6, 30, p.Header, colDim, 1)
	const lineH = 10
	for i, l := range p.Lines {
		c := colC1
		if p.Top+i == p.TriggerLine {
			c = colTrig
		}
		DrawText(sf, 6, 42+i*lineH, l, c, 1)
	}
	if v.State != streamview.Streaming && v.State != streamview.Failed {
		last := p.Top + len(p.Lines)
		DrawText(sf, 6, H-24, fmt.Sprintf("lines %d-%d of %d   HORIZ POS: scroll  push: trigger   F2: waveform   RUN: stream again",
			p.Top+1, last, p.Total), colDim, 1)
	}
}

// drawStreamStrip overlays the transcript lines around the trigger on the
// recalled record's waveform.
// TRLC-LINKS: REQ-SDS-018
func drawStreamStrip(sf Surface, hud HUD) {
	v, p := hud.Stream, hud.StreamPage
	const lineH = 10
	fillRect(sf, 0, 14, W, 26+lineH*len(p.Lines), rgb(4, 8, 16))
	DrawText(sf, 6, 16, streamStatus(v)+"   F2: list", colInfo, 1)
	DrawText(sf, 6, 26, p.Header, colDim, 1)
	for i, l := range p.Lines {
		c := colC1
		if p.Top+i == p.TriggerLine {
			c = colTrig
		}
		DrawText(sf, 6, 36+i*lineH, l, c, 1)
	}
}
