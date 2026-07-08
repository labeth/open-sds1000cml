package lcd

import (
	"math"
	"open-sds/app/internal/engine"
)

// fftRadix2 is an in-place iterative Cooley–Tukey FFT (len must be a power of
// two). Kept here (not shared with peaks.js) so the LCD has no JS dependency.
func fftRadix2(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		ang := -2 * math.Pi / float64(l)
		wr, wi := math.Cos(ang), math.Sin(ang)
		for i := 0; i < n; i += l {
			cr, ci := 1.0, 0.0
			for k := 0; k < l/2; k++ {
				tr := re[i+k+l/2]*cr - im[i+k+l/2]*ci
				ti := re[i+k+l/2]*ci + im[i+k+l/2]*cr
				re[i+k+l/2] = re[i+k] - tr
				im[i+k+l/2] = im[i+k] - ti
				re[i+k] += tr
				im[i+k] += ti
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// drawFFT renders the Hann-windowed magnitude spectrum (dB, peak-normalised) of
// the display channel across the graticule (parity with the web FFT mode). n is
// capped so the per-frame cost stays well under the render budget.
func drawFFT(sf Surface, f *engine.Frame, hud HUD) {
	valid := frameValid(f)
	n := 1
	for n*2 <= valid && n < 8192 {
		n <<= 1
	}
	if n < 16 {
		return
	}
	// Stride the WHOLE record down to n (not the leading prefix) so the spectrum
	// represents the full capture; the effective sample interval grows by stride,
	// so the axis' Nyquist shrinks accordingly.
	stride := valid / n
	if stride < 1 {
		stride = 1
	}
	nyq := 0.0
	if hud.SampleS > 0 {
		nyq = 0.5 / hud.SampleS
	}
	effNyq := nyq / float64(stride)

	// Frequency ZOOM: reuse the horizontal-zoom control to show a narrow band
	// (effNyq/zoom wide, panned by ZoomOff) magnified across the screen.
	half := n / 2
	kLo, kHi := 1, half-1
	if hud.Zoom > 1 {
		bandSpan := (half - 1) / hud.Zoom
		if bandSpan < 4 {
			bandSpan = 4
		}
		center := (half-1)/2 + int(hud.ZoomOff*float64(half-1))
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

	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	row := 10
	// Overlay both enabled channels' spectra (parity with the web), each in its
	// channel colour and normalised to its own peak.
	if sc2 && hud.TwoChan && len(f.C2) >= valid {
		fpk := fftTrace(sf, f.C2, n, stride, colC2, effNyq, kLo, kHi)
		DrawText(sf, 10, row, "FFT C2 peak "+fmtFreq(fpk), colC2, 1)
		row += 12
	}
	if sc1 {
		fpk := fftTrace(sf, f.C1, n, stride, colC1, effNyq, kLo, kHi)
		DrawText(sf, 10, row, "FFT C1 peak "+fmtFreq(fpk), colC1, 1)
		row += 12
	}
	fLo := float64(kLo) / float64(half) * effNyq
	fHi := float64(kHi) / float64(half) * effNyq
	DrawText(sf, 10, row, fmtFreq(fLo)+".."+fmtFreq(fHi), colDim, 1)
}

// fftTrace draws one channel's Hann magnitude spectrum (dB, peak-normalised)
// across the graticule and returns its parabola-refined peak frequency. src is
// strided by `stride` down to `n` samples.
func fftTrace(sf Surface, src []uint8, n, stride int, col uint16, effNyq float64, kLo, kHi int) float64 {
	samples := make([]float64, n)
	for i := 0; i < n; i++ {
		samples[i] = float64(src[i*stride])
	}
	return fftCore(sf, samples, col, effNyq, kLo, kHi)
}

// fftCore Hann-windows `samples` (DC included; mean-subtracted here), FFTs it,
// draws the magnitude over the visible band [kLo,kHi] magnified across the
// screen, marks peaks, and returns the refined peak frequency. Shared by the
// live FFT (uint8) and the super-res FFT (float fine grid — the float input is
// what lets the crunched sub-LSB bits lower the spectrum noise floor).
func fftCore(sf Surface, samples []float64, col uint16, effNyq float64, kLo, kHi int) float64 {
	n := len(samples)
	re := make([]float64, n)
	im := make([]float64, n)
	var mean float64
	for i := 0; i < n; i++ {
		mean += samples[i]
	}
	mean /= float64(n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		re[i] = (samples[i] - mean) * w
	}
	fftRadix2(re, im)
	half := n / 2
	mags := make([]float64, half)
	// Exclude the DC bin (k=0) from the 0 dB reference — Hann leakage at DC would
	// otherwise become a false 0 dB anchor. Normalise to the WHOLE spectrum so a
	// zoomed band keeps true relative levels.
	peak := 1e-9
	for k := 1; k < half; k++ {
		mags[k] = math.Hypot(re[k], im[k])
		if mags[k] > peak {
			peak = mags[k]
		}
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
		return traceTop + int(-db/-floorDb*float64(traceBot-traceTop))
	}
	// Draw + peak-search over the VISIBLE band [kLo,kHi] (magnified across W).
	prevY, peakK := -1, kLo
	for x := 0; x < W; x++ {
		k := kLo + x*span/(W-1)
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
			mx := (k - kLo) * (W - 1) / span
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
