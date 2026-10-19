// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"

	"open-sds/app/internal/panel/wsview"
)

// SPECTRUM's SOUND view (ADR-SOUND): the tuner (the note large, a cents
// needle, a strobe band that drifts the way the pitch is off and the last
// ten seconds of pitch), two level meters, the readout page and the
// pitch-locked cycle in the plot below.

// Columns of the SOUND pane.
const (
	sndTunerX  = 6
	sndTunerW  = 286
	sndMeterX  = 300
	sndMeterW  = 172
	sndReadX   = 480
	sndStripY0 = 140
)

// scaleCol scales a colour's brightness by k (0..1).
// TRLC-LINKS: REQ-SDS-273
func scaleCol(c uint16, k float64) uint16 {
	r, g, b := unpack565(c)
	return rgb(uint8(float64(r)*k), uint8(float64(g)*k), uint8(float64(b)*k))
}

// chanCol is a channel's trace colour.
// TRLC-LINKS: REQ-SDS-273
func chanCol(ch int) uint16 {
	if ch == 1 {
		return colC2
	}
	return colC1
}

// drawSoundPane draws the tuner, the meters and the readout column.
// TRLC-LINKS: REQ-SDS-273, REQ-SDS-267, REQ-SDS-266
func drawSoundPane(sf Surface, p plotArea, s *wsview.SoundPane) {
	drawPaneFrame(sf, p, &s.Pane)
	bottom := p.y0 - 15 // above the tool line
	drawTuner(sf, s, bottom)
	vline(sf, sndMeterX-4, 28, bottom, colGrid)
	drawMeters(sf, s, bottom)
	vline(sf, sndReadX-6, 28, bottom, colGrid)
	x := sndReadX
	title := fmt.Sprintf("< %s >  %d/%d", s.Page, s.PageNum+1, s.Pages)
	DrawText(sf, x, 28, title, colTrig, 1)
	room := (p.w - x - 4) / 6
	for i, l := range s.Lines {
		y := 42 + i*11
		if y > bottom-9 {
			break
		}
		DrawText(sf, x, y, cutText(l.Text, room), colorOf(l.Col), 1)
	}
}

// drawTuner draws the note, the needle, the strobe and the pitch strip.
// TRLC-LINKS: REQ-SDS-267, REQ-SDS-273
func drawTuner(sf Surface, s *wsview.SoundPane, bottom int) {
	x0, w := sndTunerX, sndTunerW
	noteCol := colDim
	if s.HasPitch {
		switch a := math.Abs(s.Cents); {
		case a < 2:
			noteCol = colOK
		case a < 10:
			noteCol = colTrig
		default:
			noteCol = colInfo
		}
	}
	DrawText(sf, x0+2, 30, s.Note, noteCol, 5)
	DrawText(sf, x0+124, 30, s.Hz, chanCol(s.Ch), 2)
	if s.HasPitch {
		DrawText(sf, x0+124, 48, fmt.Sprintf("%+.1f c", s.Cents), noteCol, 2)
	} else {
		DrawText(sf, x0+124, 48, "-- c", colDim, 2)
	}
	DrawText(sf, x0+124, 66, cutText(s.Sub, (w-124)/6), colDim, 1)
	// The cents needle: -50..+50 across the width.
	y := 80
	cx := func(c float64) int { return x0 + int(float64(w)*(0.5+math.Max(-50, math.Min(50, c))/100)) }
	hline(sf, x0, x0+w, y+8, colAxis)
	for _, c := range []float64{-50, -25, -10, 0, 10, 25, 50} {
		h := 3
		if c == 0 {
			h = 8
		}
		vline(sf, cx(c), y+8-h, y+8+h, colAxis)
	}
	fillRect(sf, cx(-2), y+5, cx(2)-cx(-2)+1, 7, scaleCol(colOK, 0.35))
	if s.HasPitch {
		n := cx(s.Cents)
		fillRect(sf, n-1, y-2, 3, 21, noteCol)
	}
	DrawText(sf, x0, y+14, "-50", colDim, 1)
	DrawText(sf, cx(0)-3, y+14, "0", colDim, 1)
	DrawTextRight(sf, x0+w, y+14, "+50", colDim, 1)
	y += 10
	// The strobe: bands drift right when sharp, left when flat, still in tune.
	sy0, sh := y+16, 16
	fillRect(sf, x0, sy0, w, sh, colBoxBg)
	if s.HasPitch {
		const period = 24
		off := int(s.Strobe * period)
		for x := 0; x < w; x++ {
			if ((x+off)%period+period)%period < period/2 {
				vline(sf, x0+x, sy0+2, sy0+sh-3, noteCol)
			}
		}
	}
	// The pitch strip: the last ten seconds, cents from the nearest note.
	py0, ph := sndStripY0, bottom-sndStripY0
	if ph < 30 {
		return
	}
	fillRect(sf, x0, py0, w, ph, colBoxBg)
	mid := py0 + ph/2
	hline(sf, x0, x0+w-1, mid, colGrid)
	hline(sf, x0, x0+w-1, py0+ph/4, colGrid)
	hline(sf, x0, x0+w-1, py0+3*ph/4, colGrid)
	DrawText(sf, x0+2, py0+2, "pitch, 10 s  +-50c", colDim, 1)
	n := len(s.Strip)
	px, py := -1, -1
	for i, c := range s.Strip {
		x := x0 + w - 1 - (n-1-i)*(w-1)/max(1, 80)
		if x < x0 {
			px = -1
			continue
		}
		if i < len(s.StripOK) && !s.StripOK[i] {
			px = -1
			continue
		}
		yy := mid - int(math.Max(-50, math.Min(50, float64(c)))/50*float64(ph/2-2))
		if px >= 0 {
			drawLine(sf, px, py, x, yy, chanCol(s.Ch))
		} else {
			sf.SetPixel(x, yy, chanCol(s.Ch))
		}
		px, py = x, yy
	}
}

// drawMeters draws the two level bars (+6 to -60 dB against the
// reference) with their figures beneath.
// TRLC-LINKS: REQ-SDS-266, REQ-SDS-273
func drawMeters(sf Surface, s *wsview.SoundPane, bottom int) {
	x0 := sndMeterX
	DrawText(sf, x0, 28, cutText(s.RefName, sndMeterW/6), colDim, 1)
	const top, dbTop, dbBot = 44, 6.0, -60.0
	barH := 82
	yOf := func(db float64) int {
		f := (dbTop - math.Max(dbBot, math.Min(dbTop, db))) / (dbTop - dbBot)
		return top + int(f*float64(barH))
	}
	// The scale between the two bars.
	sx := x0 + 64
	for _, db := range []float64{0, -12, -24, -36, -48, -60} {
		yy := yOf(db)
		hline(sf, sx-3, sx+3, yy, colAxis)
		DrawTextRight(sf, sx+28, yy-3, fmt.Sprintf("%.0f", db), colDim, 1)
	}
	for ch := 0; ch < 2; ch++ {
		m := s.Meter[ch]
		bx := x0 + 30 + ch*72
		lx := x0 + 4 + ch*126
		DrawText(sf, lx, top, fmt.Sprintf("C%d", ch+1), chanCol(ch), 1)
		fillRect(sf, bx, top, 22, barH+1, colBoxBg)
		if m.Clip {
			fillRect(sf, lx-2, top+12, 21, 9, colStale)
			DrawText(sf, lx-1, top+13, "CLP", colBG, 1)
		}
		if !m.On {
			continue
		}
		y := yOf(m.DB)
		col := chanCol(ch)
		if m.Hot {
			col = colWarn
		}
		fillRect(sf, bx+2, y, 18, top+barH-y, col)
		pk := yOf(m.PeakDB)
		hline(sf, bx, bx+21, pk, colInfo)
		for i, l := range m.Lines {
			yy := top + barH + 8 + i*10
			if yy > bottom-8 {
				break
			}
			DrawText(sf, x0+ch*86, yy, cutText(l, 14), map[bool]uint16{true: chanCol(ch), false: colInfo}[i == 0], 1)
		}
	}
}

// drawSoundTrace draws the pitch-locked cycles in the plot: the newest in
// the channel's colour over the older ones dimmed, and its figures.
// TRLC-LINKS: REQ-SDS-272, REQ-SDS-273
func drawSoundTrace(sf Surface, p plotArea, s *wsview.SoundPane) {
	if s == nil {
		return
	}
	if s.State != "" {
		DrawText(sf, 8, p.y0+6, cutText(s.State, (p.w-16)/6), colWarn, 1)
	}
	if len(s.Traces) == 0 || !(s.TraceHi > s.TraceLo) {
		msg := "Cycle trace: " + s.TraceWhy
		if s.TraceWhy == "" {
			msg = "Cycle trace: waiting"
		}
		DrawText(sf, (p.w-TextWidth(msg, 1))/2, p.y0+p.h/2, msg, colDim, 1)
		return
	}
	yOf := func(v float32) int {
		f := (float64(v) - s.TraceLo) / (s.TraceHi - s.TraceLo)
		return p.y0 + p.h - 1 - int(f*float64(p.h-1))
	}
	if s.TraceLo < 0 && s.TraceHi > 0 { // the zero line
		hline(sf, 0, p.w-1, yOf(0), colAxis)
	}
	n := len(s.Traces)
	for k, tr := range s.Traces {
		col := chanCol(s.Ch)
		if k < n-1 {
			col = scaleCol(col, 0.18+0.3*float64(k)/float64(n))
		}
		px, py := -1, -1
		for i, v := range tr {
			x := i * (p.w - 1) / max(1, len(tr)-1)
			y := yOf(v)
			if px >= 0 {
				drawLine(sf, px, py, x, y, col)
			}
			px, py = x, y
		}
	}
	for i, l := range s.TraceTop {
		t := cutText(l.Text, (p.w-80)/6)
		fillRect(sf, 6, p.y0+4+i*11, TextWidth(t, 1)+4, 11, colBG)
		DrawText(sf, 8, p.y0+6+i*11, t, colorOf(l.Col), 1)
	}
	DrawTextRight(sf, p.w-6, p.y0+6, si3(s.TraceHi)+"V", colDim, 1)
	DrawTextRight(sf, p.w-6, p.y0+p.h-12, si3(s.TraceLo)+"V", colDim, 1)
}

// si3 writes v to three significant digits.
// TRLC-LINKS: REQ-SDS-273
func si3(v float64) string { return fmt.Sprintf("%.3g", v) }
