// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// Spectrogram ("FFT over time"): a scrolling waterfall of Hann-windowed
// magnitude spectra — X = frequency, Y = time (newest at top), colour = dB.
// One new row + a one-row scroll per frame (docs/spectrogram-plan.md). The LCD
// loop owns it and feeds fresh frames; Render blits its image so Render stays
// pure (screenshot-safe).

// spectrogram plot geometry on the 800×480 panel
const (
	sgX0   = 56     // left of the heatmap (room for dB axis on the left key)
	sgY0   = 22     // top
	sgX1   = W - 18 // right
	sgY1   = H - 26 // bottom (room for the frequency axis)
	sgCols = sgX1 - sgX0
	sgRows = sgY1 - sgY0
)

// Spectrogram is the scrolling image plus the last effective Nyquist (for the
// frequency axis). Not safe for concurrent Push; the LCD loop is the only
// writer.
// TRLC-LINKS: REQ-SDS-068
type Spectrogram struct {
	img     *MemSurface
	effNyq  float64 // Hz at the right edge of the heatmap
	rows    int     // rows painted so far (for the "warming up" hint)
	floorDB float64 // colour-map floor (dB below the row peak)
}

// NewSpectrogram allocates the waterfall image.
// TRLC-LINKS: REQ-SDS-068
func NewSpectrogram() *Spectrogram {
	return &Spectrogram{img: NewMemSurface(), floorDB: -60}
}

// heat maps t∈[0,1] to a perceptual black→blue→cyan→green→yellow→red→white
// ramp (RGB565). Monotone in brightness so higher dB always reads "hotter".
// TRLC-LINKS: REQ-SDS-068
func heat(t float64) uint16 {
	// Total in t: a NaN (from a degenerate 0·Inf in the paint math) must not
	// reach int(NaN) → a garbage stops[] index. NaN and the low tail → black.
	if math.IsNaN(t) || t <= 0 {
		return rgb(0, 0, 0)
	}
	if t >= 1 {
		return rgb(255, 255, 255)
	}
	// six segments across the ramp
	const seg = 6.0
	s := t * seg
	i := int(s)
	if i < 0 { // defensive: keep the index in range whatever float slips through
		i = 0
	}
	if i > 5 {
		i = 5
	}
	f := s - float64(i)
	lerp := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*f) }
	// inferno-like ramp: monotone in brightness so higher dB always reads hotter
	stops := [7][3]uint8{
		{0, 0, 0}, {40, 0, 90}, {130, 20, 90}, {200, 50, 20}, {240, 150, 10}, {250, 220, 80}, {255, 255, 255},
	}
	a, b := stops[i], stops[i+1]
	return rgb(lerp(a[0], b[0]), lerp(a[1], b[1]), lerp(a[2], b[2]))
}

// Push takes the frame's spectrum, scrolls the image down one row, and paints
// the new spectrum as the top row. Ch selects the source channel. The row is
// the FFT view's spectrum (framecalc.Spectrum, cached per frame), so the
// waterfall covers the span the SPECTRUM caption and the web's FFT and
// waterfall show (a 2048-point stride of its own draws 0 to 20.8 MHz under a
// caption of 0 to 83.3 MHz). effNyq is the frame's Nyquist, for a frame
// without a sample interval.
// TRLC-LINKS: REQ-SDS-068
func (sg *Spectrogram) Push(f *engine.Frame, ch int, effNyq float64) {
	if f == nil || len(f.C1) == 0 {
		return
	}
	sampleS := f.SampleS
	if !(sampleS > 0) && effNyq > 0 {
		sampleS = 0.5 / effNyq
	}
	sp, ok := framecalc.Spectrum(f, ch, sampleS)
	if !ok {
		return
	}
	mags, peak := sp.Mags, sp.Peak
	if len(mags) < 2 || !(peak > 0) {
		return
	}
	sg.effNyq = sp.SpanHz

	// scroll the heatmap region down one row (copy each row into the one below)
	rowBytes := W * 2
	for y := sgY1 - 1; y > sgY0; y-- {
		copy(sg.img.Pix[y*rowBytes+sgX0*2:y*rowBytes+sgX1*2],
			sg.img.Pix[(y-1)*rowBytes+sgX0*2:(y-1)*rowBytes+sgX1*2])
	}
	// paint the newest spectrum on the top row: column x → freq bin, dB → colour
	half := len(mags)
	floor := sg.floorDB
	if !(floor < 0) { // NaN or >=0 would make invFloor Inf/NaN → poison the paint
		floor = -60
	}
	invFloor := 1.0 / (-floor)
	for x := 0; x < sgCols; x++ {
		k := x * half / sgCols
		db := 20 * math.Log10(mags[k]/peak+1e-12)
		t := 1 + db*invFloor // db=0 → 1, db=floorDB → 0
		sg.img.SetPixel(sgX0+x, sgY0, heat(t))
	}
	if sg.rows < sgRows {
		sg.rows++
	}
}

// Rows reports how many waterfall rows have been painted (debug/status).
// TRLC-LINKS: REQ-SDS-068
func (sg *Spectrogram) Rows() int { return sg.rows }

// Clear blanks the waterfall.
// TRLC-LINKS: REQ-SDS-068
func (sg *Spectrogram) Clear() {
	for i := range sg.img.Pix {
		sg.img.Pix[i] = 0
	}
	sg.rows = 0
}

// drawSpectrogram blits the waterfall image and draws the frequency axis + a dB
// colour key. `sg` is the accumulated image passed via the HUD.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-068
func drawSpectrogram(sf Surface, p plotArea, sg *Spectrogram, hideCaptions bool) {
	drawSpectrogramMark(sf, p, sg, hideCaptions, nil)
}

// drawSpectrogramMark blits the waterfall into plot p (scaled from its
// image: the full-screen plot is 1:1) with its axis and colour key, and
// SPECTRUM's marker when one is given.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-068, REQ-SDS-216
func drawSpectrogramMark(sf Surface, p plotArea, sg *Spectrogram, hideCaptions bool, m *wsview.SpecPane) {
	if sg == nil || sg.rows == 0 {
		msg := "SPECTROGRAM - FFT over time; needs a triggered signal"
		DrawText(sf, (p.w-TextWidth(msg, 1))/2, p.y0+p.h/2, msg, colDim, 1)
		return
	}
	x0, x1 := sgX0, p.w-(W-sgX1)
	y0, y1 := p.y0+sgY0, p.y0+p.h-(H-sgY1)
	cols, rows := x1-x0, y1-y0
	if cols < 8 || rows < 8 {
		return
	}
	// blit the heatmap region, nearest neighbour when the plot is smaller
	for y := 0; y < rows; y++ {
		sy := sgY0 + y*sgRows/rows
		for x := 0; x < cols; x++ {
			sf.SetPixel(x0+x, y0+y, sg.img.At(sgX0+x*sgCols/cols, sy))
		}
	}
	// frequency axis (0 .. effNyq) with a few ticks
	if sg.effNyq > 0 {
		for i := 0; i <= 4; i++ {
			x := x0 + i*cols/4
			f := sg.effNyq * float64(i) / 4
			if i == 4 { // right-aligned: centred, it ran off the screen edge
				DrawTextRight(sf, x, y1+6, fmtFreq(f), colDim, 1)
			} else {
				DrawText(sf, x-12, y1+6, fmtFreq(f), colDim, 1)
			}
			sf.SetPixel(x, y1, colAxis)
		}
	}
	// time arrow (newest at top)
	DrawText(sf, 6, y0, "new", colDim, 1)
	DrawText(sf, 6, y1-10, "old", colDim, 1)
	if m != nil && m.HasMark && sg.effNyq > 0 {
		xOf := func(hz float64) int { return x0 + int(math.Min(math.Max(hz/sg.effNyq, 0), 1)*float64(cols)) }
		if m.HasRef {
			vdash(sf, xOf(m.RefHz), y0, y1, colDim)
		}
		x := xOf(m.MarkHz)
		vdash(sf, x, y0, y1, colTrig)
		lw := TextWidth(m.MarkText, 1) + 4
		lx := min(max(x+4, 2), p.w-lw-2)
		fillRect(sf, lx, y0+2, lw, 10, colBG)
		DrawText(sf, lx+2, y0+3, m.MarkText, colTrig, 1)
	}
	if hideCaptions {
		return
	}
	// dB colour key along the top-right, clear of the status bar
	ky := max(p.y0+6, 13)
	for i := 0; i < 40; i++ {
		c := heat(float64(i) / 39)
		fillRect(sf, x1-44+i, ky, 1, 8, c)
	}
	DrawText(sf, x1-64, ky, "dB", colDim, 1)
	DrawText(sf, x0+2, ky, fmt.Sprintf("spgm %.0fdB floor", sg.floorDB), colDim, 1)
}
