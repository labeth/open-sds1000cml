package lcd

import (
	"fmt"
	"math"
)

// colSR is the super-res overlay colour — magenta, distinct from C1/C2/trigger.
var colSR = rgb(230, 120, 240)

// drawSuperresHUD overlays the super-res status line (focus/bits/count) along the
// bottom edge while stacking is active, and — in the gate-edit foci — the gate
// markers over the live trace with the active edge highlighted.
func drawSuperresHUD(sf Surface, hud HUD) {
	// Gate overlay while watching/editing (focus 0/1/2): two vertical markers at
	// the SELECTED span's edges (the user's region — not the internal one-period
	// stack gate, which would jump after arming); the edge being edited is drawn
	// solid/bright, the other dim.
	mLo, mHi := hud.SRWinLo, hud.SRWinHi
	if mHi <= mLo {
		mLo, mHi = hud.SRGateLo, hud.SRGateHi
	}
	if hud.SRFocus != 3 && hud.SRN > 0 && mHi > mLo {
		xAt := func(s int) int {
			x := s * W / hud.SRN
			if x < 0 {
				x = 0
			}
			if x > W-1 {
				x = W - 1
			}
			return x
		}
		marker := func(x int, active bool) {
			for y := traceTop; y < traceBot; y++ {
				if active || (y&3) == 0 { // active edge solid; idle edge dashed
					sf.SetPixel(x, y, colSR)
				}
			}
		}
		marker(xAt(mLo), hud.SRFocus == 1)
		marker(xAt(mHi), hud.SRFocus == 2)
	}

	tag := "SR " + srFocusTag(hud.SRFocus)
	msg := fmt.Sprintf("%s  +%.1fb  x%d  %s", tag, hud.SRBits, hud.SRk, hud.SRStatus)
	// Bottom-left, above the liveness strip; a thin backing bar keeps it legible
	// over the trace.
	y := H - 12
	for by := y - 1; by < y+9 && by < H; by++ {
		for bx := 0; bx < TextWidth(msg, 1)+8 && bx < W; bx++ {
			sf.SetPixel(bx, by, colBG)
		}
	}
	DrawText(sf, 4, y, msg, colSR, 1)
}

func srFocusTag(f int) string {
	switch f {
	case 1:
		return "GATE<" // editing the start edge
	case 2:
		return "GATE>" // editing the end edge
	case 3:
		return "REVIEW"
	default:
		return "WATCH"
	}
}

// drawSuperresTrace renders the super-resolved review across the trace area,
// spanning EXACTLY the selected window [SRWinLo,SRWinHi) — the same span the
// user froze, so the view does not change. The mean lives on the stack-gate's
// L·K fine grid; when SRPeriod > 0 the stack is ONE period and it is TILED
// across the window (phase-locked to the gate start), reconstructing the frozen
// multi-wave view from the fast one-period stack. Each screen column takes the
// min/max over its covered fine bins so detail survives any grid size.
func drawSuperresTrace(sf Surface, hud HUD) {
	m := hud.SRMean
	nb := len(m)
	if nb == 0 {
		return
	}
	K := hud.SRk
	if K < 1 {
		K = 1
	}
	winLo, winHi := float64(hud.SRWinLo), float64(hud.SRWinHi)
	if winHi <= winLo { // no recorded span: render the whole grid across the screen
		winLo, winHi = float64(hud.SRGateLo), float64(hud.SRGateLo)+float64(nb)/float64(K)
	}
	span := winHi - winLo
	gateLo := float64(hud.SRGateLo)
	period := float64(hud.SRPeriod)
	// binAt maps a raw-sample position within the window to a fine-grid bin.
	binAt := func(s float64) int {
		off := s - gateLo
		if period > 0 {
			off = math.Mod(off, period)
			if off < 0 {
				off += period
			}
		}
		return int(off * float64(K))
	}
	prevY, havePrev := 0, false
	for x := 0; x < W; x++ {
		s0 := winLo + float64(x)*span/float64(W)
		s1 := winLo + float64(x+1)*span/float64(W)
		b0, b1 := binAt(s0), binAt(s1)
		lo, hi, any := float32(1e9), float32(-1e9), false
		scan := func(a, b int) {
			for bi := a; bi <= b; bi++ {
				if bi < 0 || bi >= nb {
					continue
				}
				v := m[bi]
				if v < 0 { // gap bin
					continue
				}
				if v < lo {
					lo = v
				}
				if v > hi {
					hi = v
				}
				any = true
			}
		}
		if period > 0 && b1 < b0 { // the column wraps a tile boundary
			scan(b0, nb-1)
			scan(0, b1)
		} else {
			scan(b0, b1)
		}
		if !any {
			havePrev = false
			continue
		}
		yHi := sampleToY(float64(hi)) // higher code → higher on screen (smaller y)
		yLo := sampleToY(float64(lo))
		// Connect from the previous column so single-sample columns still form a line.
		if havePrev {
			if prevY < yHi {
				yHi = prevY
			}
			if prevY > yLo {
				yLo = prevY
			}
		}
		for y := yHi; y <= yLo; y++ {
			sf.SetPixel(x, y, colSR)
		}
		prevY, havePrev = sampleToY(float64((lo+hi)/2)), true
	}
}

// srFilled returns a gap-free float64 copy of a fine-grid mean: -1 (uncovered)
// bins are linear-interpolated from their nearest valid neighbours (held at the
// ends). The crunched float values are preserved, so the sub-LSB super-res bits
// survive into the FFT/X-Y — the whole point of super-resolving these views.
func srFilled(mean []float32) []float64 {
	nb := len(mean)
	out := make([]float64, nb)
	first, last := -1, -1
	for i := 0; i < nb; i++ {
		if mean[i] < 0 {
			out[i] = math.NaN()
		} else {
			out[i] = float64(mean[i])
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 { // all gaps → flat mid-scale
		for i := range out {
			out[i] = 128
		}
		return out
	}
	for i := 0; i < first; i++ {
		out[i] = out[first]
	}
	for i := last + 1; i < nb; i++ {
		out[i] = out[last]
	}
	for i := first; i <= last; {
		if math.IsNaN(out[i]) {
			j := i
			for j <= last && math.IsNaN(out[j]) {
				j++
			}
			a, b := out[i-1], out[j]
			for k := i; k < j; k++ {
				out[k] = a + (b-a)*float64(k-i+1)/float64(j-i+1)
			}
			i = j
		} else {
			i++
		}
	}
	return out
}

// srResampleWindow samples the DISPLAYED super-res waveform (the same span the
// Y-T review shows: [SRWinLo,SRWinHi), period-tiled when SRPeriod>0) uniformly
// into nOut float64 code values — the common representation the stacked FFT and
// X-Y both consume, guaranteeing they match what Y-T draws.
func srResampleArray(mean []float32, nOut int) (out []float64, valid []bool) {
	nb := len(mean)
	if nb == 0 || nOut < 2 {
		return nil, nil
	}
	filled := srFilled(mean)
	out = make([]float64, nOut)
	valid = make([]bool, nOut)
	for i := 0; i < nOut; i++ {
		p := (float64(i) + 0.5) * float64(nb) / float64(nOut) // sample centres
		i0 := int(p)
		if i0 >= nb-1 {
			out[i] = filled[nb-1]
			valid[i] = mean[nb-1] >= 0
		} else {
			out[i] = filled[i0] + (filled[i0+1]-filled[i0])*(p-float64(i0))
			valid[i] = mean[i0] >= 0 && mean[i0+1] >= 0
		}
	}
	return out, valid
}

// srFFTPlan sizes the super-res FFT: n = smallest pow2 ≥ the fine-grid length nb
// (capped for the ARM redraw budget), at dt = (nb/n)·(SRSampleS/K) ≈ SRSampleS/K,
// so effNyq ≈ 0.5·K/SRSampleS — the FULL K× fine-grid Nyquist. It transforms the
// one-period fine grid DIRECTLY (like the web FFTs res.mean), NOT the tiled Y-T
// display window, so the super-res band is never decimated/aliased away.
func srFFTPlan(hud HUD) (n int, effNyq float64, kLo, kHi int, ok bool) {
	nb := len(hud.SRMean)
	if nb < 16 || hud.SRSampleS <= 0 {
		return 0, 0, 0, 0, false
	}
	K := hud.SRk
	if K < 1 {
		K = 1
	}
	n = 1
	for n < nb && n < 8192 { // smallest pow2 ≥ nb (capped): dt ≈ fine dt, no aliasing
		n <<= 1
	}
	if n < 16 {
		return 0, 0, 0, 0, false
	}
	dtFine := hud.SRSampleS / float64(K)
	if dtOut := float64(nb) * dtFine / float64(n); dtOut > 0 {
		effNyq = 0.5 / dtOut
	}
	// Frequency zoom (reuse the horizontal-zoom control), same as the live FFT.
	half := n / 2
	kLo, kHi = 1, half-1
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
	return n, effNyq, kLo, kHi, true
}

// drawSuperresFFT renders the FFT of the crunched stack (both channels, parity
// with the web). The transform runs on the FLOAT fine grid at dt = SRSampleS/K,
// so its Nyquist extends K× past the raw single-shot Nyquist AND its noise floor
// drops by the gained bits — the super-res win, now visible in the spectrum.
func drawSuperresFFT(sf Surface, hud HUD) {
	n, effNyq, kLo, kHi, ok := srFFTPlan(hud)
	if !ok {
		DrawText(sf, 10, 10, "SR FFT — stacking...", colDim, 1)
		return
	}
	half := n / 2
	// Map the align/other means back to physical C1/C2 for colour + labels.
	c1Mean, c2Mean := hud.SRMean, hud.SRMean2
	if hud.SRAlign == 1 {
		c1Mean, c2Mean = hud.SRMean2, hud.SRMean
	}
	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	row := 10
	if sc2 && hud.TwoChan && len(c2Mean) > 0 {
		if s2, _ := srResampleArray(c2Mean, n); s2 != nil {
			fpk := fftCore(sf, s2, colC2, effNyq, kLo, kHi)
			DrawText(sf, 10, row, "SR FFT C2 peak "+fmtFreq(fpk), colC2, 1)
			row += 12
		}
	}
	if sc1 {
		if s1, _ := srResampleArray(c1Mean, n); s1 != nil {
			fpk := fftCore(sf, s1, colC1, effNyq, kLo, kHi)
			DrawText(sf, 10, row, "SR FFT C1 peak "+fmtFreq(fpk), colC1, 1)
			row += 12
		}
	}
	fLo := float64(kLo) / float64(half) * effNyq
	fHi := float64(kHi) / float64(half) * effNyq
	DrawText(sf, 10, row, fmtFreq(fLo)+".."+fmtFreq(fHi)+"  (fine "+fmtFreq(effNyq)+" Nyq)", colDim, 1)
}

// drawSuperresXY plots the two crunched channel means as a Lissajous — X = the
// C1 stack, Y = the C2 stack, sampled index-for-index on the same fine grid so
// they pair up. Float grid ⇒ smoother than the live X-Y. The pen lifts over
// uncovered (-1 gap) bins rather than drawing a false chord (matches the web).
func drawSuperresXY(sf Surface, hud HUD) {
	c1Mean, c2Mean := hud.SRMean, hud.SRMean2
	if hud.SRAlign == 1 {
		c1Mean, c2Mean = hud.SRMean2, hud.SRMean
	}
	if !hud.TwoChan || len(c2Mean) == 0 {
		DrawText(sf, 10, 10, "X-Y needs CH2", colDim, 1)
		return
	}
	const nOut = 2000
	x, vx := srResampleArray(c1Mean, nOut)
	y, vy := srResampleArray(c2Mean, nOut)
	if x == nil || y == nil {
		return
	}
	px, py := -1, -1
	for i := 0; i < nOut; i++ {
		if !vx[i] || !vy[i] { // gap in either channel → lift the pen
			px = -1
			continue
		}
		sx := int(x[i] * float64(W-1) / 255.0)
		sy := sampleToY(y[i])
		if px >= 0 {
			drawLine(sf, px, py, sx, sy, colMath)
		}
		px, py = sx, sy
	}
	DrawText(sf, 10, 10, "X:C1  Y:C2 (SR)", colDim, 1)
}
