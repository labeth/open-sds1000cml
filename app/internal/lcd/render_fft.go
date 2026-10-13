// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// fftBand is the visible bin band [kLo,kHi] of a half spectrum: the whole
// band, or a zoomed slice (the horizontal zoom reused as a frequency zoom,
// panned by ZoomOff).
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070
func fftBand(half, zoom int, zoomOff float64) (kLo, kHi int) {
	kLo, kHi = 1, half-1
	if zoom > 1 {
		bandSpan := (half - 1) / zoom
		if bandSpan < 4 {
			bandSpan = 4
		}
		center := (half-1)/2 + int(zoomOff*float64(half-1))
		kLo = center - bandSpan/2
		if kLo < 1 {
			kLo = 1
		}
		kHi = kLo + bandSpan
		if kHi > half-1 {
			kHi = half - 1
			if kLo = kHi - bandSpan; kLo < 1 {
				kLo = 1
			}
		}
	}
	return kLo, kHi
}

// drawFFT renders the Hann-windowed magnitude spectrum (dB, peak-normalised)
// of each shown channel across the plot (parity with the web FFT mode), the
// FFT source on top; the spectra come from the shared per-frame cache, so the
// SPECTRUM peak table and this plot transform a frame once. n is capped so
// the per-frame cost stays well under the render budget.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070, REQ-SDS-216
func drawFFT(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	sc1, sc2 := hud.shownChans()
	shown := [2]bool{sc1, sc2 && hud.TwoChan}
	src := hud.FFTSrc & 1
	order := []int{1 - src, src} // the source last, on top
	type label struct {
		s string
		c uint16
	}
	var labels []label
	var effNyq float64
	var kLo, kHi, half int
	for _, ch := range order {
		if !shown[ch] {
			continue
		}
		sp, ok := framecalc.Spectrum(f, ch, hud.SampleS)
		if !ok {
			continue
		}
		half = len(sp.Mags) // the bins up to the span (a filter can end it before N/2)
		effNyq = sp.SpanHz
		kLo, kHi = fftBand(half, hud.Zoom, hud.ZoomOff)
		col, name := colC1, "FFT C1 peak "
		if ch == 1 {
			col, name = colC2, "FFT C2 peak "
		}
		fpk := drawSpectrum(sf, p, sp.Mags, sp.Peak, col, effNyq, kLo, kHi)
		labels = append([]label{{name + fmtFreq(fpk), col}}, labels...)
	}
	if half == 0 {
		return
	}
	fLo := float64(kLo) / float64(half) * effNyq
	fHi := float64(kHi) / float64(half) * effNyq
	if sp := hud.UI.Spec; sp != nil && !sp.Bode {
		drawSpecMarker(sf, p, sp, fLo, fHi)
	}
	if hud.UI.Layout.HideViewCaptions {
		return
	}
	// The labels go on a backing, so no spectrum runs through them (tall
	// low-frequency peaks garble the labels and the span otherwise).
	labels = append(labels, label{fmtFreq(fLo) + ".." + fmtFreq(fHi), colDim})
	row := p.y0 + 10
	for _, l := range labels {
		fillRect(sf, 8, row-2, TextWidth(l.s, 1)+4, 11, colBG)
		DrawText(sf, 10, row, l.s, l.c, 1)
		row += 12
	}
}

// drawSpecMarker draws SPECTRUM's marker in the FFT plot: a dotted amber
// line at its frequency, a triangle at its level and a label; the delta
// reference as a dim line.
// TRLC-LINKS: REQ-SDS-216
func drawSpecMarker(sf Surface, p plotArea, m *wsview.SpecPane, fLo, fHi float64) {
	if m == nil || !m.HasMark || fHi <= fLo {
		return
	}
	xOf := func(hz float64) int { return int((hz - fLo) / (fHi - fLo) * float64(p.w-1)) }
	if m.HasRef {
		if x := xOf(m.RefHz); x >= 0 && x < p.w {
			vdash(sf, x, p.y0+2, H-3, colDim)
		}
	}
	x := xOf(m.MarkHz)
	if x < 0 || x >= p.w {
		return
	}
	vdash(sf, x, p.y0+2, H-3, colTrig)
	rel := math.Max(-80, math.Min(0, m.MarkRel))
	y := max(p.top+int(-rel/80*float64(p.bot-p.top)), p.y0+16) // the top peak's triangle stays in the plot
	for dy := 0; dy <= 4; dy++ {                               // a triangle pointing down onto the level
		hline(sf, x-(4-dy), x+(4-dy), y-7+dy, colTrig)
	}
	lw := TextWidth(m.MarkText, 1) + 4
	lx := min(max(x+4, 2), p.w-lw-2)
	fillRect(sf, lx, p.y0+4, lw, 10, colBG)
	DrawText(sf, lx+2, p.y0+5, m.MarkText, colTrig, 1)
}

// vdash draws a dotted vertical line (every other pixel).
// TRLC-LINKS: REQ-SDS-021
func vdash(sf Surface, x, y0, y1 int, c uint16) {
	for y := y0; y <= y1; y += 2 {
		sf.SetPixel(x, y, c)
	}
}

// hdash draws a dotted horizontal line (every other pixel).
// TRLC-LINKS: REQ-SDS-021
func hdash(sf Surface, x0, x1, y int, c uint16) {
	for x := x0; x <= x1; x += 2 {
		sf.SetPixel(x, y, c)
	}
}

// fftCore Hann-windows `samples` (DC included; mean-subtracted here), FFTs it,
// draws the magnitude over the visible band [kLo,kHi] magnified across the
// screen, marks peaks, and returns the refined peak frequency. Shared by the
// live FFT (uint8) and the super-res FFT (float fine grid — the float input is
// what lets the crunched sub-LSB bits lower the spectrum noise floor).
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070
func fftCore(sf Surface, samples []float64, col uint16, effNyq float64, kLo, kHi int) float64 {
	return fftCoreP(sf, fullPlot, samples, col, effNyq, kLo, kHi)
}

// fftCoreP is fftCore on plot p.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070
func fftCoreP(sf Surface, p plotArea, samples []float64, col uint16, effNyq float64, kLo, kHi int) float64 {
	mags, peak, _ := framecalc.Mags(samples)
	return drawSpectrum(sf, p, mags, peak, col, effNyq, kLo, kHi)
}

// drawSpectrum draws a half spectrum's visible band [kLo,kHi] across plot p
// (dB below peak, -80 dB at the bottom), marks the significant peaks, and
// returns the parabola-refined frequency of the band's largest peak.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-070
func drawSpectrum(sf Surface, p plotArea, mags []float64, peak float64, col uint16, effNyq float64, kLo, kHi int) float64 {
	half := len(mags)
	if half < 4 {
		return 0
	}
	if peak <= 0 {
		peak = 1e-9
	}
	if kLo < 1 {
		kLo = 1
	}
	if kHi >= half {
		kHi = half - 1
	}
	if kHi <= kLo {
		kHi = kLo + 1
	}
	span := kHi - kLo
	const floorDb = -80.0 // match the web FFT full-scale span
	yFor := func(m float64) int {
		db := 20 * math.Log10(m/peak+1e-12)
		if db < floorDb {
			db = floorDb
		}
		return p.top + int(-db/-floorDb*float64(p.bot-p.top))
	}
	// Draw + peak-search over the VISIBLE band [kLo,kHi] (magnified across the plot).
	prevY, peakK := -1, kLo
	for x := 0; x < p.w; x++ {
		k := kLo + x*span/(p.w-1)
		if k > kHi {
			k = kHi
		}
		if k > 1 && mags[k] > mags[peakK] {
			peakK = k
		}
		y := yFor(mags[k])
		if prevY >= 0 {
			drawLine(sf, x-1, prevY, x, y, col)
		}
		prevY = y
	}
	// Mark significant peaks (local maxima > -40 dBc) in the visible band.
	markFloor := peak * math.Pow(10, -40.0/20)
	for k, marks := kLo+1, 0; k < kHi && marks < 8; k++ {
		if mags[k] > mags[k-1] && mags[k] >= mags[k+1] && mags[k] > markFloor {
			mx := (k - kLo) * (p.w - 1) / span
			for d := 2; d < 6; d++ {
				sf.SetPixel(mx, yFor(mags[k])-d, col)
			}
			marks++
		}
	}
	// 3-point log-parabolic vertex refine of the peak bin (matches peaks.js).
	kref := float64(peakK)
	if peakK > 1 && peakK < half-1 {
		a, b, cc := math.Log(mags[peakK-1]+1e-12), math.Log(mags[peakK]+1e-12), math.Log(mags[peakK+1]+1e-12)
		if den := a - 2*b + cc; den < 0 {
			if d := 0.5 * (a - cc) / den; d > -1 && d < 1 {
				kref = float64(peakK) + d
			}
		}
	}
	return kref / float64(half) * effNyq
}
