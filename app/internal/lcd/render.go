package lcd

import (
	"fmt"
	"math"
	"strconv"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// Colour palette (spec 07 §2.2). The col* variables are GENERATED into
// palette_gen.go from ../web/tokens.json — the single source of truth shared with
// the web UI — so the two surfaces cannot diverge (they did: the trigger colour
// was red on web but green here). Edit tokens.json + `go generate ./internal/web`.

// HUD is the UI-state snapshot the overlay renders alongside the frozen
// frame (spec 07 §6). It carries no capture state.
type HUD struct {
	C1VdivV, C2VdivV float64
	Probe1, Probe2   float64 // probe attenuation (1/10/100); 0 treated as ×1
	Cpl1, Cpl2       int     // input coupling (analog.CplDC/CplAC/CplGND)
	TdivS            float64
	TrigSrc          int
	TrigRising       bool
	TrigLvlDiv       float64
	Running, Norm    bool
	Single           bool
	Trigd            bool
	SampleS          float64 // per-sample seconds (frequency readout)
	TrigPosFrac      float64 // horizontal trigger position, 0.5 = centre
	OffC1V, OffC2V   float64 // applied vertical offset volts (ground markers)
	ShowC1, ShowC2   bool    // per-channel display enable
	ShowMeas         bool    // on-device MEASURE panel overlay
	ViewMode         int     // 0 = Y-T, 1 = X-Y, 2 = FFT
	MathMode         int     // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2
	AutosetBusy      bool    // autoset sweep running → show a cancelable banner
	AutosetMsg       string  // banner text while AutosetBusy
	RefC1, RefC2     [2][]uint8 // saved reference waveforms (REF A/B); nil if unset
	RefShow          [2]bool
	TwoChan          bool

	// On-screen cursors: two X (time) and two Y (volts), positions as screen
	// fractions. CurType 0=X 1=Y; CurSel 0=A 1=B (the active one is highlighted).
	CurOn   bool
	CurType int
	CurSel  int
	CurX    [2]float64
	CurY    [2]float64

	// On-screen menu overlay (spec 08 §6): five softkey slots down the right edge.
	MenuOpen  bool
	MenuTitle string
	MenuItems []MenuItem
	MenuSel   int
}

// MenuItem is one softkey slot label + value for the LCD menu overlay.
type MenuItem struct{ Label, Value string }

const (
	traceTop = 8
	traceBot = H - 4 // 476
)

// sampleToY: higher code = higher on screen; clamp to panel (spec 07 §3.4).
func sampleToY(v float64) int {
	y := traceBot - int(v*float64(traceBot-traceTop)/255+0.5)
	if y < 0 {
		y = 0
	}
	if y > H-1 {
		y = H - 1
	}
	return y
}

func drawGraticule(sf Surface) {
	for c := 0; c <= 10; c++ {
		x := c * (W - 1) / 10
		col := colGrid
		if c == 5 {
			col = colAxis
		}
		for y := 0; y < H; y++ {
			sf.SetPixel(x, y, col)
		}
	}
	for r := 0; r <= 8; r++ {
		y := r * (H - 1) / 8
		col := colGrid
		if r == 4 {
			col = colAxis
		}
		for x := 0; x < W; x++ {
			sf.SetPixel(x, y, col)
		}
	}
}

// drawLine is a Bresenham segment (spec 07 §3.5).
func drawLine(sf Surface, x0, y0, x1, y1 int, c uint16) {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		sf.SetPixel(x0, y0, c)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// drawTrace maps the record window onto the panel (spec 07 §3.5): nearest
// sample when Interp is false, linear interpolation of REAL samples when
// true — never sinc, never a segment across a skipped column.
func drawTrace(sf Surface, sig []uint8, win int, xc float64, interp bool, col uint16, posFrac float64) {
	n := len(sig)
	if n == 0 {
		return
	}
	if win > n {
		win = n
	}
	if posFrac <= 0 || posFrac > 1 {
		posFrac = 0.5
	}
	// Anchor at posFrac; do NOT clamp `left` — extend the nearest rail off the
	// record (repeat-nearest), identical to web window() so LCD == web. Keeps the
	// anchor exactly at posFrac even when the record has no mid crossing.
	left := xc - float64(win)*posFrac
	prevX, prevY := -1, 0
	for x := 0; x < W; x++ {
		pos := left + float64(x)*float64(win)/float64(W)
		var y int
		if interp {
			if pos < 0 {
				pos = 0
			} else if pos > float64(n-1) {
				pos = float64(n - 1)
			}
			i := int(pos)
			v := float64(sig[i])
			if i+1 < n {
				frac := pos - float64(i)
				v = v*(1-frac) + float64(sig[i+1])*frac
			}
			y = sampleToY(v)
		} else {
			i := int(pos)
			if pos < 0 {
				i = 0
			} else if i > n-1 {
				i = n - 1
			}
			y = sampleToY(float64(sig[i]))
		}
		if prevX >= 0 {
			drawLine(sf, prevX, prevY, x, y, col)
		} else {
			sf.SetPixel(x, y, col)
		}
		prevX, prevY = x, y
	}
}

// drawEnvelope fills each column min→max (spec 07 §4): every pixel lies
// between a real captured min and max.
func drawEnvelope(sf Surface, mn, mx []uint8, cols int, col uint16) {
	for x := 0; x < W; x++ {
		c := x * cols / W
		if c >= cols || c >= len(mn) || c >= len(mx) {
			continue
		}
		yTop := sampleToY(float64(mx[c]))
		yBot := sampleToY(float64(mn[c]))
		if yTop > yBot {
			yTop, yBot = yBot, yTop
		}
		for y := yTop; y <= yBot; y++ {
			sf.SetPixel(x, y, col)
		}
	}
}

// frameValid clamps a frame's valid-sample count into range (shared by the
// alternate-view renderers below).
func frameValid(f *engine.Frame) int {
	valid := f.Valid
	if valid < 1 {
		valid = 1
	}
	if valid > len(f.C1) {
		valid = len(f.C1)
	}
	return valid
}

// coupledDisplay applies the software coupling model for a channel (mirrors the
// Y-T path so the alternate views see the same trace).
func coupledDisplay(sig []uint8, cpl int) []uint8 {
	if cpl != analog.CplDC {
		return analog.CoupleDisplay(sig, cpl)
	}
	return sig
}

// drawXY plots C1 (x) against C2 (y) — the Lissajous view (parity with the web
// X-Y mode). Codes 0..255 map across the graticule (x) and up it (y); a stride
// keeps dense records cheap.
func drawXY(sf Surface, f *engine.Frame, hud HUD) {
	valid := frameValid(f)
	if len(f.C2) < valid {
		DrawText(sf, 10, 10, "X-Y needs CH2", colDim, 1)
		return
	}
	c1 := coupledDisplay(f.C1[:valid], hud.Cpl1)
	c2 := coupledDisplay(f.C2[:valid], hud.Cpl2)
	step := 1
	if valid > 4000 {
		step = valid / 4000
	}
	px, py := -1, -1
	for i := 0; i < valid; i += step {
		x := int(float64(c1[i]) * float64(W-1) / 255.0)
		y := sampleToY(float64(c2[i]))
		if px >= 0 {
			drawLine(sf, px, py, x, y, colMath)
		}
		px, py = x, y
	}
	DrawText(sf, 10, 10, "X:C1  Y:C2", colDim, 1)
}

// drawMath overlays the math trace (C1+C2 / C1-C2 / C1×C2) in purple, in code
// space centred at 128 so it shares the Y-T trace mapping (parity with the web
// math card).
func drawMath(sf Surface, f *engine.Frame, hud HUD, win int, xc float64) {
	valid := frameValid(f)
	if len(f.C2) < valid {
		return
	}
	c1 := coupledDisplay(f.C1[:valid], hud.Cpl1)
	c2 := coupledDisplay(f.C2[:valid], hud.Cpl2)
	m := make([]uint8, valid)
	for i := 0; i < valid; i++ {
		a, b := int(c1[i])-128, int(c2[i])-128
		var v int
		switch hud.MathMode {
		case 1:
			v = 128 + a + b // C1+C2
		case 2:
			v = 128 + a - b // C1-C2
		case 3:
			v = 128 + b - a // C2-C1
		default:
			v = 128 + a*b/96 // C1×C2, scaled to stay on-screen (matches web)
		}
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		m[i] = uint8(v)
	}
	drawTrace(sf, m, win, xc, f.Interp, colMath, hud.TrigPosFrac)
}

// drawRefs overlays the saved reference waveforms (REF A/B) as dim traces for
// comparison against the live trace (parity with the web REF A/B). Screen-space
// snapshots — they align while the timebase/scale are unchanged. A is purple,
// B is the info tint, so they read apart from the live channels.
func drawRefs(sf Surface, hud HUD, win int, xc float64, interp bool) {
	cols := [2]uint16{colInfo, colDim} // distinct from the purple math trace
	for i := 0; i < 2; i++ {
		if !hud.RefShow[i] {
			continue
		}
		if r := hud.RefC1[i]; len(r) > 0 {
			drawTrace(sf, r, win, xc, interp, cols[i], hud.TrigPosFrac)
		}
		if hud.TwoChan {
			if r := hud.RefC2[i]; len(r) > 0 {
				drawTrace(sf, r, win, xc, interp, cols[i], hud.TrigPosFrac)
			}
		}
	}
}

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

	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	row := 10
	// Overlay both enabled channels' spectra (parity with the web), each in its
	// channel colour and normalised to its own peak.
	if sc2 && hud.TwoChan && len(f.C2) >= valid {
		fpk := fftTrace(sf, f.C2, n, stride, colC2, effNyq)
		DrawText(sf, 10, row, "FFT C2 peak "+fmtFreq(fpk), colC2, 1)
		row += 12
	}
	if sc1 {
		fpk := fftTrace(sf, f.C1, n, stride, colC1, effNyq)
		DrawText(sf, 10, row, "FFT C1 peak "+fmtFreq(fpk), colC1, 1)
		row += 12
	}
	DrawText(sf, 10, row, "0.."+fmtFreq(effNyq), colDim, 1)
}

// fftTrace draws one channel's Hann magnitude spectrum (dB, peak-normalised)
// across the graticule and returns its parabola-refined peak frequency. src is
// strided by `stride` down to `n` samples.
func fftTrace(sf Surface, src []uint8, n, stride int, col uint16, effNyq float64) float64 {
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
	// Exclude the DC bin (k=0) from BOTH the 0 dB reference and the peak search —
	// Hann leakage at DC would otherwise become a false 0 dB anchor.
	peak, peakK := 1e-9, 1
	for k := 1; k < half; k++ {
		mags[k] = math.Hypot(re[k], im[k])
		if mags[k] > peak {
			peak = mags[k]
		}
		if mags[k] > mags[peakK] {
			peakK = k
		}
	}
	const floorDb = -80.0 // match the web FFT full-scale span
	prevY := -1
	for x := 0; x < W; x++ {
		k := x * (half - 1) / (W - 1)
		if k < 1 {
			k = 1
		}
		db := 20 * math.Log10(mags[k]/peak+1e-12)
		if db < floorDb {
			db = floorDb
		}
		y := traceTop + int(-db/-floorDb*float64(traceBot-traceTop))
		if prevY >= 0 {
			drawLine(sf, x-1, prevY, x, y, col)
		}
		prevY = y
	}
	// Mark the significant spectral peaks (local maxima above -40 dBc) with a small
	// tick above each — parity with the web's peak markers, capped to avoid clutter.
	markFloor := peak * math.Pow(10, -40.0/20)
	for k, marks := 2, 0; k < half-1 && marks < 8; k++ {
		if mags[k] > mags[k-1] && mags[k] >= mags[k+1] && mags[k] > markFloor {
			mx := k * (W - 1) / (half - 1)
			db := 20 * math.Log10(mags[k]/peak+1e-12)
			my := traceTop + int(-db/-floorDb*float64(traceBot-traceTop))
			for d := 2; d < 6; d++ {
				sf.SetPixel(mx, my-d, col)
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

// ---- value formatting (spec 07 §6.2): 3 sig figs, ASCII suffixes ----

func g3(x float64) string { return strconv.FormatFloat(x, 'g', 3, 64) }

func fmtVolt(v float64) string {
	neg := ""
	x := v
	if x < 0 { // keep the sign — Vmin/Vtop/Vbase/Vavg can be negative
		neg, x = "-", -x
	}
	if x >= 1 {
		return neg + g3(x) + "V"
	}
	return neg + g3(x*1e3) + "mV"
}

// fillRect paints a solid rectangle (clipped to the surface).
func fillRect(sf Surface, x, y, w, h int, c uint16) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			sf.SetPixel(xx, yy, c)
		}
	}
}

// drawMeasPanel draws the on-device MEASURE overlay — a calibrated measurement
// box PER enabled channel (via the shared measure package, so it matches the web
// exactly), so both C1 and C2 are readable at once without disturbing the trigger
// source. Uses the real probe-scaled volts/div and the channel's software coupling.
func drawMeasPanel(sf Surface, f *engine.Frame, hud HUD) {
	if f == nil || f.Valid == 0 {
		return
	}
	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	x := 4
	if sc1 {
		measBox(sf, f, hud, 0, x)
		x += 116
	}
	if sc2 && hud.TwoChan && len(f.C2) >= f.Valid {
		measBox(sf, f, hud, 1, x)
	}
}

// measBox renders one channel's full measurement set at left edge x.
func measBox(sf Surface, f *engine.Frame, hud HUD, ch, x int) {
	valid := f.Valid
	if valid > len(f.C1) {
		valid = len(f.C1)
	}
	sig := f.C1[:valid]
	vdiv, probe, off, cpl, col, name := hud.C1VdivV, hud.Probe1, hud.OffC1V, hud.Cpl1, colC1, "C1"
	if ch == 1 {
		sig = f.C2[:valid]
		vdiv, probe, off, cpl, col, name = hud.C2VdivV, hud.Probe2, hud.OffC2V, hud.Cpl2, colC2, "C2"
	}
	if probe < 1 {
		probe = 1
	}
	if cpl != analog.CplDC { // match the displayed (coupled) trace
		sig = analog.CoupleDisplay(sig, cpl)
		off = 0
	}
	m := measure.Compute(sig, vdiv/32*probe, off*probe, hud.SampleS)
	if m == nil {
		return
	}
	rows := [][2]string{
		{"Vpp", fmtVolt(m.Vpp)},
		{"Vmax", fmtVolt(m.Vmax)},
		{"Vmin", fmtVolt(m.Vmin)},
		{"Vamp", fmtVolt(m.Vampl)},
		{"Vtop", fmtVolt(m.Vtop)},
		{"Vbase", fmtVolt(m.Vbase)},
		{"Vrms", fmtVolt(m.Vrms)},
		{"Vavg", fmtVolt(m.Vmean)},
	}
	if m.HasTiming {
		rows = append(rows,
			[2]string{"Freq", fmtFreq(m.Freq)},
			[2]string{"Per", fmtTdiv(m.Period)},
			[2]string{"Duty", fmt.Sprintf("%.0f%%", m.Duty)})
		if m.RiseS > 0 {
			rows = append(rows, [2]string{"Rise", fmtTdiv(m.RiseS)})
		}
		if m.FallS > 0 {
			rows = append(rows, [2]string{"Fall", fmtTdiv(m.FallS)})
		}
		if m.PosWidthS > 0 {
			rows = append(rows, [2]string{"+Wid", fmtTdiv(m.PosWidthS)})
		}
		if m.NegWidthS > 0 {
			rows = append(rows, [2]string{"-Wid", fmtTdiv(m.NegWidthS)})
		}
		if m.Overshoot > 0 {
			rows = append(rows, [2]string{"OS", fmt.Sprintf("%.0f%%", m.Overshoot)})
		}
	}
	const y0, w = 22, 112
	h := 14 + len(rows)*10
	fillRect(sf, x, y0, w, h, rgb(6, 10, 22))
	for yy := y0; yy < y0+h; yy++ { // thin border
		sf.SetPixel(x, yy, colGrid)
		sf.SetPixel(x+w-1, yy, colGrid)
	}
	DrawText(sf, x+3, y0+2, name+" MEAS", col, 1)
	for i, r := range rows {
		yy := y0 + 12 + i*10
		DrawText(sf, x+3, yy, r[0], colInfo, 1)
		DrawTextRight(sf, x+w-3, yy, r[1], colInfo, 1)
	}
}

// drawCursors draws the on-screen X (time) or Y (volts) cursor pair and a Δ
// readout. Time Δ uses the labelled t/div × 10 divisions; volts Δ uses the
// trigger-source channel's probe-scaled V/div × 8 divisions.
func drawCursors(sf Surface, hud HUD) {
	if !hud.CurOn {
		return
	}
	dash := func(vertical bool, at int, active bool) {
		col := colGrid
		if active {
			col = colTrig
		}
		if vertical {
			for y := traceTop; y <= traceBot; y += 2 {
				sf.SetPixel(at, y, col)
			}
		} else {
			for x := 0; x < W; x += 2 {
				sf.SetPixel(x, at, col)
			}
		}
	}
	var label string
	if hud.CurType == 0 { // X (time) cursors
		xA := int(hud.CurX[0] * float64(W-1))
		xB := int(hud.CurX[1] * float64(W-1))
		dash(true, xA, hud.CurSel == 0)
		dash(true, xB, hud.CurSel == 1)
		dt := math.Abs(hud.CurX[0]-hud.CurX[1]) * hud.TdivS * 10
		label = "dt " + fmtTdiv(dt)
		if dt > 0 {
			label += "  1/dt " + fmtFreq(1/dt)
		}
	} else { // Y (volts) cursors — report ΔV for BOTH channels (web parity)
		yA := traceTop + int(hud.CurY[0]*float64(traceBot-traceTop))
		yB := traceTop + int(hud.CurY[1]*float64(traceBot-traceTop))
		dash(false, yA, hud.CurSel == 0)
		dash(false, yB, hud.CurSel == 1)
		frac := math.Abs(hud.CurY[0] - hud.CurY[1])
		dvOf := func(vdiv, probe float64) string {
			if probe < 1 {
				probe = 1
			}
			return fmtVolt(frac * vdiv * probe * 8) // 8 vertical divisions
		}
		sc1, sc2 := hud.ShowC1, hud.ShowC2
		if !sc1 && !sc2 {
			sc1, sc2 = true, true
		}
		label = ""
		if sc1 {
			label = "dV1 " + dvOf(hud.C1VdivV, hud.Probe1)
		}
		if sc2 && hud.TwoChan {
			if label != "" {
				label += "  "
			}
			label += "dV2 " + dvOf(hud.C2VdivV, hud.Probe2)
		}
	}
	w := len(label)*6 + 6
	x := W/2 - w/2 // top-centre, clear of the MEAS panel (left) and menu (right)
	fillRect(sf, x, 22, w, 12, rgb(6, 10, 22))
	DrawText(sf, x+3, 24, label, colTrig, 1)
}

// cplTag returns the coupling suffix for the HUD, shown only when it is not the
// default DC (so the common case stays uncluttered): " AC" or " GND".
func cplTag(mode int) string {
	switch mode {
	case analog.CplAC:
		return " AC"
	case analog.CplGND:
		return " GND"
	}
	return ""
}

// vdivLabel formats a channel's volts/div at the probe tip. A probe factor
// >1 scales the electrical V/div and appends a "10x"/"100x" tag so the label
// matches what the operator actually measures.
func vdivLabel(vdivV, probe float64) string {
	if probe < 1 {
		probe = 1
	}
	s := fmtVolt(vdivV * probe)
	if probe != 1 {
		s += fmt.Sprintf(" %gx", probe)
	}
	return s
}

func fmtTdiv(s float64) string {
	switch {
	case s >= 1:
		return g3(s) + "s"
	case s >= 1e-3:
		return g3(s*1e3) + "ms"
	case s >= 1e-6:
		return g3(s*1e6) + "us"
	default:
		return g3(s*1e9) + "ns"
	}
}

func fmtFreq(f float64) string {
	switch {
	case f >= 1e6:
		return g3(f/1e6) + "MHz"
	case f >= 1e3:
		return g3(f/1e3) + "kHz"
	default:
		return g3(f) + "Hz"
	}
}


// Render draws one complete frame into the back buffer (spec 07 §3.2):
// fill → graticule → trace/envelope → liveness strip → readouts. Never
// blanks on a held frame; the strip goes red instead.
func Render(sf Surface, f *engine.Frame, hud HUD, live bool) {
	sf.Fill(colBG)
	drawGraticule(sf)

	// Both-off ⇒ show both (default / callers that don't set the flags).
	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	// X-Y / FFT only make sense on per-sample (native/decimated) frames — an
	// envelope/roll frame has no paired samples or usable spectrum, so fall
	// through to the Y-T envelope rendering (matches the web, which gates these).
	if hud.ViewMode == 1 && f != nil && !f.IsEnv && len(f.C1) > 0 {
		drawXY(sf, f, hud)
	} else if hud.ViewMode == 2 && f != nil && !f.IsEnv && len(f.C1) > 0 {
		drawFFT(sf, f, hud)
	} else if f != nil && len(f.C1) > 0 {
		valid := f.Valid
		if valid < 1 {
			valid = 1
		}
		if valid > len(f.C1) {
			valid = len(f.C1)
		}
		if f.IsEnv && f.EnvCols > 0 {
			if hud.TwoChan && sc2 {
				drawEnvelope(sf, f.EnvMin2, f.EnvMax2, f.EnvCols, colC2)
			}
			if sc1 {
				drawEnvelope(sf, f.EnvMin, f.EnvMax, f.EnvCols, colC1)
			}
		} else {
			win := f.WinCols
			if win <= 0 || win > valid {
				win = valid
			}
			c1 := f.C1[:valid]
			if hud.Cpl1 != analog.CplDC { // software coupling: AC removes DC, GND grounds
				c1 = analog.CoupleDisplay(c1, hud.Cpl1)
			}
			xc := f.EdgeX
			if xc < 0 {
				// Match web window(): an uncentred frame (EdgeX=-1) is always a
				// flat/DC capture under the engine's publish policy — centre on
				// the record middle. NEVER fabricate an edge from a marginal/noise
				// crossing (that would jitter a flat line and diverge from web).
				xc = float64(valid) / 2
			}
			if hud.TwoChan && sc2 && len(f.C2) >= valid {
				c2 := f.C2[:valid]
				if hud.Cpl2 != analog.CplDC {
					c2 = analog.CoupleDisplay(c2, hud.Cpl2)
				}
				drawTrace(sf, c2, win, xc, f.Interp, colC2, hud.TrigPosFrac)
			}
			if sc1 {
				drawTrace(sf, c1, win, xc, f.Interp, colC1, hud.TrigPosFrac)
			}
			if hud.MathMode != 0 {
				drawMath(sf, f, hud, win, xc)
			}
			drawRefs(sf, hud, win, xc, f.Interp)
		}
	}

	// Liveness strip: green only for a fresh frame with real signal.
	strip := colStale
	if live && f != nil && f.Ptp >= 8 {
		strip = colOK
	}
	for y := 0; y <= 1; y++ {
		for x := 0; x < W; x++ {
			sf.SetPixel(x, y, strip)
		}
	}

	// Trigger/ground markers, the MEASURE panel and cursors are time-domain
	// concepts — only overlay them in Y-T, not on the X-Y/FFT plots.
	if hud.ViewMode == 0 {
		drawMarkers(sf, hud)
	}
	drawHUD(sf, f, hud)
	if hud.ShowMeas && hud.ViewMode == 0 {
		drawMeasPanel(sf, f, hud)
	}
	if hud.ViewMode == 0 {
		drawCursors(sf, hud)
	}
	drawMenu(sf, hud)
	if hud.AutosetBusy {
		drawAutosetBanner(sf, hud.AutosetMsg)
	}
}

// drawAutosetBanner overlays a centred "AUTOSET…" progress banner while the
// sweep runs, with the cancel hint (a second AUTO press stops it).
func drawAutosetBanner(sf Surface, msg string) {
	if msg == "" {
		msg = "AUTOSET..."
	}
	const bw, bh = 300, 44
	x0, y0 := (W-bw)/2, (H-bh)/2
	for y := y0; y < y0+bh; y++ {
		for x := x0; x < x0+bw; x++ {
			sf.SetPixel(x, y, rgb(8, 12, 28))
		}
	}
	for x := x0; x < x0+bw; x++ {
		sf.SetPixel(x, y0, colTrig)
		sf.SetPixel(x, y0+bh-1, colTrig)
	}
	for y := y0; y < y0+bh; y++ {
		sf.SetPixel(x0, y, colTrig)
		sf.SetPixel(x0+bw-1, y, colTrig)
	}
	DrawText(sf, x0+(bw-TextWidth(msg, 2))/2, y0+6, msg, colTrig, 2)
	// Only the working banner is cancelable; a result note (e.g. "no signal")
	// just clears on its own.
	hint := "AUTO again to cancel"
	if msg != "AUTOSET..." {
		hint = "check the probe / scale"
	}
	DrawText(sf, x0+(bw-TextWidth(hint, 1))/2, y0+30, hint, colDim, 1)
}

// drawMenu overlays the softkey menu down the right edge (spec 08 §6): a title
// band + five slots (F1 top … F5 bottom) each a label over its current value,
// the active slot boxed. F1..F5 select/cycle; the ADJUST knob tracks the box.
func drawMenu(sf Surface, hud HUD) {
	if !hud.MenuOpen {
		return
	}
	const mw = 116          // menu width
	x0 := W - mw            // left edge of the panel
	// dim the panel background
	for y := 12; y < H-2; y++ {
		for x := x0; x < W; x++ {
			sf.SetPixel(x, y, rgb(8, 12, 28))
		}
	}
	for y := 12; y < H-2; y++ { // left border
		sf.SetPixel(x0, y, colTrig)
	}
	DrawText(sf, x0+6, 16, hud.MenuTitle, colTrig, 1)
	// five slots evenly spaced from y≈40 to y≈H-30
	n := len(hud.MenuItems)
	if n == 0 {
		return
	}
	top, bot := 40, H-36
	for i, it := range hud.MenuItems {
		if it.Label == "" {
			continue
		}
		sy := top + (bot-top)*i/5
		labelCol, valueCol := colInfo, colTrig
		if i == hud.MenuSel {
			// Filled inverted highlight bar (was a 1px outline) — clearer which
			// softkey is active, and legible at arm's length. Text goes dark on
			// the amber fill.
			for y := sy - 2; y <= sy+16; y++ {
				for x := x0 + 3; x < W-3; x++ {
					sf.SetPixel(x, y, colTrig)
				}
			}
			labelCol, valueCol = colBG, colBG
		}
		DrawText(sf, x0+6, sy, it.Label, labelCol, 1)
		DrawText(sf, x0+6, sy+8, it.Value, valueCol, 1)
	}
}

// drawMarkers overlays the trigger level (horizontal line + right handle), the
// trigger position (top pointer), and the per-channel ground/offset arrows on
// the left edge — the same references the web canvas shows (spec 07 §6).
func drawMarkers(sf Surface, hud HUD) {
	px := func(x, y int, c uint16) {
		if x >= 0 && x < W && y >= 0 && y < H {
			sf.SetPixel(x, y, c)
		}
	}
	// Trigger LEVEL: dashed horizontal line + right-edge arrow at the level code.
	ly := sampleToY(128 + hud.TrigLvlDiv*32)
	if ly >= 2 && ly < H {
		for x := 0; x < W; x += 7 {
			px(x, ly, colTrig)
			px(x+1, ly, colTrig)
		}
		for dy := -4; dy <= 4; dy++ {
			for dx := 0; dx < 5-absI(dy); dx++ {
				px(W-1-dx, ly+dy, colTrig)
			}
		}
	}
	// Trigger POSITION: downward pointer at the top.
	tx := int(hud.TrigPosFrac * float64(W))
	if hud.TrigPosFrac <= 0 || hud.TrigPosFrac > 1 {
		tx = W / 2
	}
	for dy := 0; dy <= 7; dy++ {
		hw := (7 - dy) / 2
		for dx := -hw; dx <= hw; dx++ {
			px(tx+dx, dy+2, colTrig)
		}
	}
	// Per-channel GROUND (0 V) arrows on the left edge: code = 128 + offV·32/Vdiv.
	ground := func(vdiv, offV float64, col uint16) {
		if vdiv <= 0 {
			return
		}
		gy := sampleToY(128 + offV*32/vdiv)
		for dx := 0; dx <= 6; dx++ {
			for dy := -(6 - dx); dy <= 6-dx; dy++ {
				px(dx, gy+dy, col)
			}
		}
	}
	ground(hud.C1VdivV, hud.OffC1V, colC1)
	if hud.TwoChan {
		ground(hud.C2VdivV, hud.OffC2V, colC2)
	}
}

func absI(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func drawHUD(sf Surface, f *engine.Frame, hud HUD) {
	DrawText(sf, 4, 2, "C1 "+vdivLabel(hud.C1VdivV, hud.Probe1)+cplTag(hud.Cpl1), colC1, 1)
	if hud.TwoChan {
		DrawText(sf, 96, 2, "C2 "+vdivLabel(hud.C2VdivV, hud.Probe2)+cplTag(hud.Cpl2), colC2, 1)
	}
	if f != nil && f.Valid > 0 { // clipping warning — railed traces make readings suspect
		valid := frameValid(f) // clamp to len(C1) — f.Valid can exceed the slice
		if measure.Clipped(f.C1[:valid]) {
			DrawText(sf, 4, 14, "CLIP", colTrig, 1)
		}
		if hud.TwoChan && len(f.C2) >= valid && measure.Clipped(f.C2[:valid]) {
			DrawText(sf, 96, 14, "CLIP", colTrig, 1)
		}
	}
	// The horizontal axis is time only in Y-T; in FFT it's frequency and in X-Y
	// it's C1 voltage, so "M <t/div>" would be misleading there.
	switch hud.ViewMode {
	case 1:
		DrawText(sf, 200, 2, "X-Y", colMath, 1)
	case 2:
		DrawText(sf, 200, 2, "FFT", colMath, 1)
	default:
		DrawText(sf, 200, 2, "M "+fmtTdiv(hud.TdivS), colInfo, 1)
	}
	// Math legend so the purple trace is identified (and not confused with a ref).
	if hud.MathMode != 0 && hud.ViewMode == 0 {
		names := []string{"", "C1+C2", "C1-C2", "C2-C1", "C1xC2"} // ASCII font has no '×'
		if m := hud.MathMode; m >= 1 && m < len(names) {
			DrawText(sf, 300, 2, "M:"+names[m], colMath, 1)
		}
	}

	edge := "^"
	if !hud.TrigRising {
		edge = "v"
	}
	state := "AUTO"
	if hud.Norm {
		state = "NORM"
	}
	switch {
	case hud.Single && hud.Running:
		state = "SNGL" // armed, waiting for the single trigger
	case !hud.Running && hud.Single:
		state = "STOP" // (should not persist: single clears on capture)
	case !hud.Running:
		state = "STOP"
	case hud.Trigd:
		state = "T'D"
	case hud.Norm:
		state = "WAIT"
	}
	DrawTextRight(sf, 796, 2,
		fmt.Sprintf("T C%d %s %+.2fdiv %s", hud.TrigSrc+1, edge, hud.TrigLvlDiv, state),
		colTrig, 1)

	if f == nil || f.Valid == 0 {
		return
	}
	valid := f.Valid
	if valid > len(f.C1) {
		valid = len(f.C1)
	}
	// Bottom quick-readout: calibrated Vpp + frequency per channel, via the
	// shared measure package (real probe-scaled V/div + software coupling) so it
	// agrees with the MEASURE panel and the web.
	line := func(x int, sig []uint8, vdiv, probe, off float64, cpl int, col uint16, name string) {
		if probe < 1 {
			probe = 1
		}
		if cpl != analog.CplDC {
			sig = analog.CoupleDisplay(sig, cpl)
			off = 0
		}
		m := measure.Compute(sig, vdiv/32*probe, off*probe, hud.SampleS)
		if m == nil {
			return
		}
		s := name + " Vpp " + fmtVolt(m.Vpp)
		if m.HasTiming {
			s += "  f " + fmtFreq(m.Freq)
		}
		DrawText(sf, x, H-9, s, col, 1)
	}
	line(4, f.C1[:valid], hud.C1VdivV, hud.Probe1, hud.OffC1V, hud.Cpl1, colC1, "C1")
	if hud.TwoChan && len(f.C2) >= valid {
		line(410, f.C2[:valid], hud.C2VdivV, hud.Probe2, hud.OffC2V, hud.Cpl2, colC2, "C2")
	}
}
