package lcd

import (
	"fmt"
	"math"
)

// drawBode renders the accumulated FRA / Bode curve: magnitude (dB, colC1) in
// the upper panel and phase (deg, colC2) in the lower, against a LOG frequency
// axis with decade gridlines. Parity with the web bode.js render.
func drawBode(sf Surface, hud HUD) {
	n := len(hud.BodeFreq)
	if n < 1 {
		DrawText(sf, W/2-150, H/2, "BODE / FRA — arm and sweep the source frequency", colDim, 1)
		return
	}
	const padL, padR, padT, padB = 56, 20, 22, 30
	magH := (H - padT - padB) * 56 / 100
	gap := 18
	phY0 := padT + magH + gap
	phH := H - padB - phY0
	plotL, plotR := padL, W-padR
	plotW := plotR - plotL

	f0, f1 := hud.BodeFreq[0], hud.BodeFreq[n-1]
	if f0 <= 0 {
		f0 = 1
	}
	if f1 <= f0 {
		f1 = f0 * 10
	}
	lf0, lf1 := math.Log10(f0), math.Log10(f1)
	span := lf1 - lf0
	if span <= 0 {
		span = 1
	}
	xOf := func(f float64) int {
		if f <= 0 {
			f = f0
		}
		return plotL + int((math.Log10(f)-lf0)/span*float64(plotW))
	}
	gLo, gHi := niceRange(hud.BodeGain, -20, 20, 10)
	pLo, pHi := niceRange(hud.BodePhase, -180, 180, 45)
	magYOf := func(db float64) int { return padT + int((gHi-db)/(gHi-gLo)*float64(magH)) }
	phYOf := func(d float64) int { return phY0 + int((pHi-d)/(pHi-pLo)*float64(phH)) }

	// frequency decade gridlines
	d0, d1 := math.Floor(lf0), math.Ceil(lf1)
	for d := d0; d <= d1; d++ {
		for _, m := range []float64{1, 2, 5} {
			f := m * math.Pow(10, d)
			if f < f0*0.999 || f > f1*1.001 {
				continue
			}
			x := xOf(f)
			col := colGrid
			if m == 1 {
				col = colAxis
			}
			for y := padT; y <= padT+magH; y++ {
				sf.SetPixel(x, y, col)
			}
			for y := phY0; y <= phY0+phH; y++ {
				sf.SetPixel(x, y, col)
			}
			if m == 1 {
				DrawText(sf, x-10, H-padB+6, fmtFreq(f), colDim, 1)
			}
		}
	}
	// magnitude horizontal gridlines + dB labels
	for i := 0; i <= 4; i++ {
		db := gLo + (gHi-gLo)*float64(i)/4
		y := magYOf(db)
		col := colGrid
		if math.Abs(db) < 1e-6 {
			col = colAxis
		}
		for x := plotL; x <= plotR; x++ {
			sf.SetPixel(x, y, col)
		}
		DrawTextRight(sf, plotL-4, y, g3(db), colDim, 1)
	}
	// phase horizontal gridlines + deg labels
	for i := 0; i <= 4; i++ {
		dg := pLo + (pHi-pLo)*float64(i)/4
		y := phYOf(dg)
		col := colGrid
		if math.Abs(dg) < 1e-6 {
			col = colAxis
		}
		for x := plotL; x <= plotR; x++ {
			sf.SetPixel(x, y, col)
		}
		DrawTextRight(sf, plotL-4, y, g3(dg), colDim, 1)
	}
	// traces
	for i := 1; i < n; i++ {
		drawLine(sf, xOf(hud.BodeFreq[i-1]), magYOf(hud.BodeGain[i-1]), xOf(hud.BodeFreq[i]), magYOf(hud.BodeGain[i]), colC1)
		drawLine(sf, xOf(hud.BodeFreq[i-1]), phYOf(hud.BodePhase[i-1]), xOf(hud.BodeFreq[i]), phYOf(hud.BodePhase[i]), colC2)
	}
	// point dots
	for i := 0; i < n; i++ {
		fillRect(sf, xOf(hud.BodeFreq[i])-1, magYOf(hud.BodeGain[i])-1, 3, 3, colC1)
		fillRect(sf, xOf(hud.BodeFreq[i])-1, phYOf(hud.BodePhase[i])-1, 3, 3, colC2)
	}
	DrawText(sf, plotL+2, padT-14, "dB", colC1, 1)
	DrawText(sf, plotL+2, phY0-14, "phase deg", colC2, 1)
	DrawTextRight(sf, W-4, 4, fmt.Sprintf("%d pts", n), colDim, 1)
}

// niceRange pads [min,max] of vals to round multiples of step; fallback when
// empty. Mirrors bode.js bodeNiceRange.
func niceRange(vals []float64, flo, fhi, step float64) (float64, float64) {
	if len(vals) == 0 {
		return flo, fhi
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if !(hi > lo) {
		lo -= step
		hi += step
	}
	pad := (hi - lo) * 0.1
	if pad < step {
		pad = step
	}
	lo = math.Floor((lo-pad)/step) * step
	hi = math.Ceil((hi+pad)/step) * step
	return lo, hi
}
