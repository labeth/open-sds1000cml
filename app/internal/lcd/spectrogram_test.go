package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
)

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
	// push each frequency once; the newest is at row 0, previous at row 1, etc.
	for _, f := range freqs {
		sg.Push(sgSine(n, f, dt), 0, 0.5/dt)
	}
	// row 0 = last (100 MHz), row 1 = 60 MHz, row 2 = 20 MHz — columns increasing downward-to-up
	col0 := brightCol(0) // 100 MHz -> highest column
	col1 := brightCol(1) // 60 MHz
	col2 := brightCol(2) // 20 MHz -> lowest column
	if !(col0 > col1 && col1 > col2) {
		t.Errorf("stepped frequency not tracked over rows: cols top→down = %d, %d, %d (want decreasing)", col0, col1, col2)
	}
	// map row 0 back to ~100 MHz
	gotHz := float64(col0) / float64(sgCols) * sg.effNyq
	if math.Abs(gotHz-100e6) > 100e6*0.06 {
		t.Errorf("newest row peak %.0f MHz, want 100 MHz", gotHz/1e6)
	}
}

// The spectrogram VIEW render path (Render → ViewMode 4 → drawSpectrogram) had
// no coverage: dispatch + the heatmap blit, and the empty-Spect hint.
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

func absU8(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}
