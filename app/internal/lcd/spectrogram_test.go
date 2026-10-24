// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"bytes"
	"math"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
)

// TRLC-LINKS: REQ-SDS-068
func sgSine(n int, freqHz, sampleS float64) *engine.Frame {
	c := make([]uint8, n)
	for i := 0; i < n; i++ {
		v := 128 + 90*math.Sin(2*math.Pi*freqHz*float64(i)*sampleS)
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		c[i] = uint8(v)
	}
	return &engine.Frame{C1: c, C2: c, Valid: n, SampleS: sampleS}
}

// The colormap must be monotone in brightness (higher dB reads hotter).
// TRLC-LINKS: REQ-SDS-068
func TestHeatMonotone(t *testing.T) {
	lum := func(c uint16) int {
		r := int((c >> 11) & 0x1f)
		g := int((c >> 5) & 0x3f)
		b := int(c & 0x1f)
		return r*2 + g + b // rough luminance in 565 units
	}
	prev := -1
	for i := 0; i <= 20; i++ {
		l := lum(heat(float64(i) / 20))
		if l < prev-2 { // allow tiny non-monotonicity from the 565 quantisation
			t.Errorf("heat not monotone at t=%.2f (lum %d < %d)", float64(i)/20, l, prev)
		}
		prev = l
	}
}

// A single-tone frame's spectrogram row must peak at the frequency's column.
// TRLC-LINKS: REQ-SDS-068
func TestSpectrogramPeakColumn(t *testing.T) {
	const n = 4096
	const dt = 2e-9 // Nyquist 250 MHz
	sg := NewSpectrogram()
	f := 50e6 // 50 MHz -> 20% of Nyquist -> column ~0.2·sgCols
	sg.Push(sgSine(n, f, dt), 0, 0.5/dt)

	// scan the top row for the brightest column, map back to a frequency
	best, bestLum := 0, -1
	lum := func(c uint16) int { return int((c>>11)&0x1f)*2 + int((c>>5)&0x3f) + int(c&0x1f) }
	for x := 0; x < sgCols; x++ {
		l := lum(sg.img.At(sgX0+x, sgY0))
		if l > bestLum {
			bestLum, best = l, x
		}
	}
	gotHz := float64(best) / float64(sgCols) * sg.effNyq
	if math.Abs(gotHz-f) > f*0.05 {
		t.Errorf("brightest column at %.1f MHz, tone is %.1f MHz", gotHz/1e6, f/1e6)
	}
}

// "FFT over time": a frequency that STEPS across successive frames must move
// the bright column DOWN the rows (older rows keep the earlier frequency).
// TRLC-LINKS: REQ-SDS-068
func TestSpectrogramTracksSteppedFrequency(t *testing.T) {
	const n = 4096
	const dt = 2e-9
	sg := NewSpectrogram()
	freqs := []float64{20e6, 60e6, 100e6}
	brightCol := func(row int) int {
		best, bestLum := 0, -1
		lum := func(c uint16) int { return int((c>>11)&0x1f)*2 + int((c>>5)&0x3f) + int(c&0x1f) }
		for x := 0; x < sgCols; x++ {
			l := lum(sg.img.At(sgX0+x, sgY0+row))
			if l > bestLum {
				bestLum, best = l, x
			}
		}
		return best
	}
	// push each frequency once; a 4096-sample record paints two rows, so the
	// newest is at rows 0-1, the previous at rows 2-3, etc.
	for _, f := range freqs {
		sg.Push(sgSine(n, f, dt), 0, 0.5/dt)
	}
	// rows 0-1 = last (100 MHz), 2-3 = 60 MHz, 4-5 = 20 MHz — columns increasing downward-to-up
	col0 := brightCol(0) // 100 MHz -> highest column
	col1 := brightCol(2) // 60 MHz
	col2 := brightCol(4) // 20 MHz -> lowest column
	if brightCol(1) != col0 || brightCol(3) != col1 {
		t.Errorf("a record's two rows differ: %d/%d, %d/%d", col0, brightCol(1), col1, brightCol(3))
	}
	if !(col0 > col1 && col1 > col2) {
		t.Errorf("stepped frequency not tracked over rows: cols top→down = %d, %d, %d (want decreasing)", col0, col1, col2)
	}
	// map row 0 back to ~100 MHz
	gotHz := float64(col0) / float64(sgCols) * sg.effNyq
	if math.Abs(gotHz-100e6) > 100e6*0.06 {
		t.Errorf("newest row peak %.0f MHz, want 100 MHz", gotHz/1e6)
	}
}

// A record paints a row for each 2048-point stretch of it, the newest on
// top: a tone that steps inside one capture shows its later frequency on the
// top row and its earlier one below, and the waterfall fills four rows a
// capture of 8192 samples.
// TRLC-LINKS: REQ-SDS-068
func TestSpectrogramRowsPerStretch(t *testing.T) {
	const n, dt = 8192, 2e-9
	early, late := sgSine(n, 20e6, dt), sgSine(n, 100e6, dt)
	f := &engine.Frame{C1: append(append([]uint8{}, early.C1[:n/2]...), late.C1[n/2:]...), Valid: n, SampleS: dt}
	f.C2 = f.C1
	sg := NewSpectrogram()
	sg.Push(f, 0, 0.5/dt)
	if sg.Rows() != 4 {
		t.Fatalf("an 8192-sample record painted %d rows, want 4", sg.Rows())
	}
	lum := func(c uint16) int { return int((c>>11)&0x1f)*2 + int((c>>5)&0x3f) + int(c&0x1f) }
	hz := func(row int) float64 {
		best, bestLum := 0, -1
		for x := 0; x < sgCols; x++ {
			if l := lum(sg.img.At(sgX0+x, sgY0+row)); l > bestLum {
				bestLum, best = l, x
			}
		}
		return float64(best) / float64(sgCols) * sg.effNyq
	}
	for row, want := range []float64{100e6, 100e6, 20e6, 20e6} {
		if got := hz(row); math.Abs(got-want) > want*0.06 {
			t.Errorf("row %d peaks at %.0f MHz, want %.0f MHz", row, got/1e6, want/1e6)
		}
	}
	if math.Abs(sg.effNyq-250e6) > 1 {
		t.Errorf("rows span %g Hz, want the record's 250 MHz", sg.effNyq)
	}
}

// The spectrogram VIEW render path (Render → ViewMode 4 → drawSpectrogram) had
// no coverage: dispatch + the heatmap blit, and the empty-Spect hint.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-068
func TestRenderSpectrogramView(t *testing.T) {
	const n = 4096
	const dt = 2e-9
	// A broadband (noisy) record lights every frequency column, so the waterfall
	// densely fills the heatmap — a clean discriminator from the centred hint.
	noise := func() *engine.Frame {
		c := make([]uint8, n)
		s := uint32(12345)
		for i := range c {
			s = s*1664525 + 1013904223
			c[i] = uint8(64 + s>>25) // 64..191 spread
		}
		return &engine.Frame{C1: c, C2: c, Valid: n, SampleS: dt}
	}
	sg := NewSpectrogram()
	for i := 0; i < 30; i++ { // many rows so the waterfall (not just the hint) is drawn
		sg.Push(noise(), 0, 0.5/dt)
	}
	hud := defaultHUD()
	hud.ViewMode = 4
	hud.Spect = sg
	m := NewMemSurface()
	Render(m, noise(), hud, true)
	// count painted (non-background, non-black) pixels inside the heatmap rect —
	// proves the ViewMode==4 dispatch ran and the blit loop drew the waterfall.
	painted := 0
	bg := m.At(0, 0) // corner is background/graticule
	for y := sgY0; y < sgY1; y++ {
		for x := sgX0; x < sgX1; x++ {
			if c := m.At(x, y); c != bg && c != rgb(0, 0, 0) {
				painted++
			}
		}
	}
	if painted < 500 {
		t.Errorf("spectrogram view drew only %d painted pixels in the heatmap rect (want a filled waterfall)", painted)
	}
	// empty spectrogram must render the hint, not panic
	hud2 := defaultHUD()
	hud2.ViewMode = 4
	hud2.Spect = NewSpectrogram()
	Render(NewMemSurface(), nil, hud2, true)
	// nil Spect must also be safe
	hud3 := defaultHUD()
	hud3.ViewMode = 4
	Render(NewMemSurface(), nil, hud3, true)
}

// heatStops is the inferno ramp both heat() (Go) and sgHeat() (JS) hardcode.
// This test + the JS TestSpectrogramJS stop check lock the two in parity: change
// either side's stops and one of the two tests fails.
var heatStops = [7][3]uint8{
	{0, 0, 0}, {40, 0, 90}, {130, 20, 90}, {200, 50, 20}, {240, 150, 10}, {250, 220, 80}, {255, 255, 255},
}

// TRLC-LINKS: REQ-SDS-068
func TestHeatStops(t *testing.T) {
	for i := 0; i < 7; i++ {
		r, g, b := unrgb(heat(float64(i) / 6)) // t=i/6 lands exactly on stop i
		want := heatStops[i]
		// RGB565 quantisation: R/B 5-bit (±8), G 6-bit (±4)
		if absU8(r, want[0]) > 8 || absU8(g, want[1]) > 4 || absU8(b, want[2]) > 8 {
			t.Errorf("heat(%d/6) = (%d,%d,%d), want stop %v (±565)", i, r, g, b, want)
		}
	}
}

// TRLC-LINKS: REQ-SDS-068
func absU8(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// A spectrum with more bins than plot columns draws each column at the largest
// of its bins: a square wave's odd harmonics are single narrow tones, and a
// column that sampled one bin drew every other harmonic tens of dB low (the
// web's per-pixel maxima showed them all). The waterfall's pixels alike.
// TRLC-LINKS: REQ-SDS-068, REQ-SDS-070
func TestSpectrumColumnsShowNarrowTones(t *testing.T) {
	const half = 4096
	mags := make([]float64, half)
	for k := range mags {
		mags[k] = 1e-4
	}
	var tones []int
	for k := 49; k < half-1; k += 98 { // odd harmonics of a square: every 98 bins from 49
		mags[k] = 1
		tones = append(tones, k)
	}
	sf := NewMemSurface()
	p := fullPlot
	drawSpectrum(sf, p, mags, 1, colC1, 83.3e6, 1, half-1)
	span := half - 2
	for _, k := range tones {
		x := (k - 1) * (p.w - 1) / span
		lit := false
		for dx := -1; dx <= 1 && !lit; dx++ {
			for y := p.top; y <= p.top+2; y++ {
				if xx := x + dx; xx >= 0 && xx < p.w && sf.At(xx, y) == colC1 {
					lit = true
				}
			}
		}
		if !lit {
			t.Fatalf("tone at bin %d (column %d) not drawn at its 0 dB level", k, x)
		}
	}
	sg := NewSpectrogram()
	sg.pushMags(mags, 1)
	lum := func(c uint16) int { return int((c>>11)&0x1f)*2 + int((c>>5)&0x3f) + int(c&0x1f) }
	white := lum(heat(1))
	for _, k := range tones {
		x := 0
		for x+1 < sgCols && (x+1)*half/sgCols <= k { // the pixel whose bins hold k
			x++
		}
		if l := lum(sg.img.At(sgX0+x, sgY0)); l != white {
			t.Fatalf("waterfall pixel %d holding the tone at bin %d is not the peak colour (lum %d, want %d)", x, k, l, white)
		}
	}
}

// The waterfall blit into the back buffer equals the pixel-by-pixel blit,
// full screen and scaled into a smaller plot.
// TRLC-LINKS: REQ-SDS-068
func TestSpectrogramBlitMatchesPixels(t *testing.T) {
	sg := NewSpectrogram()
	for s := 0; s < 6; s++ {
		for r := 0; r < 10; r++ {
			sg.Push(sgSine(4096, 10e6+15e6*float64(s), 2e-9), 0, 0.5/2e-9)
		}
	}
	for _, p := range []plotArea{fullPlot, plotOf(paneLayout(136)), plotOf(wsview.Layout{MenuW: 116})} {
		fast, slow := NewMemSurface(), NewMemSurface()
		drawSpectrogram(fast, p, sg, false)
		drawSpectrogram(pixelSurface{slow}, p, sg, false)
		if !bytes.Equal(fast.Pix, slow.Pix) {
			t.Fatalf("plot %+v: fast blit differs from the pixel blit", p)
		}
	}
}
