package lcd

import (
	"fmt"
	"math"

	"open-sds/app/internal/engine"
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
	sgFFTN = 2048 // decimate the record to ~this many samples for the row FFT
)

// Spectrogram is the scrolling image plus the last effective Nyquist (for the
// frequency axis). Not safe for concurrent Push; the LCD loop is the only
// writer.
type Spectrogram struct {
	img     *MemSurface
	effNyq  float64 // Hz at the right edge of the heatmap
	rows    int     // rows painted so far (for the "warming up" hint)
	floorDB float64 // colour-map floor (dB below the row peak)
}

// NewSpectrogram allocates the waterfall image.
func NewSpectrogram() *Spectrogram {
	return &Spectrogram{img: NewMemSurface(), floorDB: -60}
}

// heat maps t∈[0,1] to a perceptual black→blue→cyan→green→yellow→red→white
// ramp (RGB565). Monotone in brightness so higher dB always reads "hotter".
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

// Push computes the frame's magnitude spectrum, scrolls the image down one row,
// and paints the new spectrum as the top row. Ch selects the source channel.
func (sg *Spectrogram) Push(f *engine.Frame, ch int, effNyq float64) {
	if f == nil {
		return
	}
	src := f.C1
	if ch == 1 {
		src = f.C2
	}
	valid := f.Valid
	if valid > len(src) {
		valid = len(src)
	}
	if valid < 32 {
		return
	}
	// Stride the whole record down to ~sgFFTN samples so the spectrum covers the
	// full capture at a USEFUL frequency resolution (like the FFT view) — not the
	// raw Nyquist, which crams a low-frequency signal into the leftmost sliver.
	stride := valid / sgFFTN
	if stride < 1 {
		stride = 1
	}
	mags, peak := spectrumMags(src[:valid], stride)
	if mags == nil || peak <= 0 {
		return
	}
	sg.effNyq = effNyq / float64(stride) // Nyquist shrinks by the decimation

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
func (sg *Spectrogram) Rows() int { return sg.rows }

// Clear blanks the waterfall.
func (sg *Spectrogram) Clear() {
	for i := range sg.img.Pix {
		sg.img.Pix[i] = 0
	}
	sg.rows = 0
}

// spectrumMags returns the Hann-windowed half-spectrum magnitudes of a real
// record (largest power-of-two ≤ len) and the peak. Mirrors the FFT view and
// the web spectrum().
func spectrumMags(src []uint8, stride int) ([]float64, float64) {
	if stride < 1 {
		stride = 1
	}
	avail := len(src) / stride
	n := 1
	for n*2 <= avail {
		n <<= 1
	}
	if n < 16 {
		return nil, 0
	}
	re := make([]float64, n)
	im := make([]float64, n)
	var mean float64
	for i := 0; i < n; i++ {
		mean += float64(src[i*stride])
	}
	mean /= float64(n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		re[i] = (float64(src[i*stride]) - mean) * w
	}
	fftRadix2(re, im)
	half := n / 2
	mags := make([]float64, half)
	peak := 1e-9
	for k := 0; k < half; k++ {
		mags[k] = math.Hypot(re[k], im[k])
		if mags[k] > peak {
			peak = mags[k]
		}
	}
	return mags, peak
}

// drawSpectrogram blits the waterfall image and draws the frequency axis + a dB
// colour key. `sg` is the accumulated image passed via the HUD.
func drawSpectrogram(sf Surface, sg *Spectrogram) {
	if sg == nil || sg.rows == 0 {
		DrawText(sf, W/2-170, H/2, "SPECTROGRAM — FFT over time; needs a triggered signal", colDim, 1)
		return
	}
	// blit the heatmap region
	for y := sgY0; y < sgY1; y++ {
		for x := sgX0; x < sgX1; x++ {
			sf.SetPixel(x, y, sg.img.At(x, y))
		}
	}
	// frequency axis (0 .. effNyq) with a few ticks
	if sg.effNyq > 0 {
		for i := 0; i <= 4; i++ {
			x := sgX0 + i*sgCols/4
			f := sg.effNyq * float64(i) / 4
			DrawText(sf, x-12, sgY1+6, fmtFreq(f), colDim, 1)
			sf.SetPixel(x, sgY1, colAxis)
		}
	}
	// time arrow (newest at top)
	DrawText(sf, 6, sgY0, "new", colDim, 1)
	DrawText(sf, 6, sgY1-10, "old", colDim, 1)
	// dB colour key along the top-right
	for i := 0; i < 40; i++ {
		c := heat(float64(i) / 39)
		fillRect(sf, sgX1-44+i, 6, 1, 8, c)
	}
	DrawText(sf, sgX1-64, 8, "dB", colDim, 1)
	DrawText(sf, sgX0+2, 8, fmt.Sprintf("spgm %.0fdB floor", sg.floorDB), colDim, 1)
}
