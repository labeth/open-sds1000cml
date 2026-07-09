package lcd

import (
	"fmt"
	"math"
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

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
	m := measure.Compute(sig, vdiv/25*probe, off*probe, hud.SampleS)
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

// softkeyY is the on-screen Y (slot centre) of each physical F1..F5 button,
// measured on the unit against the bezel (F1..F4 an 80 px pitch, F5 +90).
var softkeyY = [5]int{80, 160, 240, 320, 410}

// drawMenu overlays the softkey menu down the right edge (spec 08 §6): a title
// band + five slots (F1 top … F5 bottom) each a label over its current value,
// the active slot boxed. F1..F5 select/cycle; the ADJUST knob tracks the box.
func drawMenu(sf Surface, hud HUD) {
	if !hud.MenuOpen {
		return
	}
	const mw = 116 // menu width
	x0 := W - mw   // left edge of the panel
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
	// slots aligned to the physical F1..F5 buttons (measured centres in softkeyY)
	n := len(hud.MenuItems)
	if n == 0 {
		return
	}
	for i, it := range hud.MenuItems {
		if it.Label == "" || i >= len(softkeyY) {
			continue
		}
		sy := softkeyY[i] - 7 // label baseline; slot centre (highlight) lands on softkeyY[i]
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
	ly := sampleToY(128 + hud.TrigLvlDiv*25)
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
	// Per-channel GROUND (0 V) arrows on the left edge: code = 128 + offV·25/Vdiv.
	ground := func(vdiv, offV float64, col uint16) {
		if vdiv <= 0 {
			return
		}
		gy := sampleToY(128 + offV*25/vdiv)
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
	// it's C1 voltage, so "M <t/div>" would be misleading there. But X-Y/FFT
	// fall through to Y-T on an envelope frame, so only label them when the
	// alternate view actually renders (matches the Render() branch condition).
	altView := f != nil && !f.IsEnv && len(f.C1) > 0
	switch {
	case hud.ViewMode == 1 && altView:
		DrawText(sf, 200, 2, "X-Y", colMath, 1)
	case hud.ViewMode == 2 && altView:
		DrawText(sf, 200, 2, "FFT", colMath, 1)
	default:
		DrawText(sf, 200, 2, "M "+fmtTdiv(hud.TdivS), colInfo, 1)
		if hud.Zoom > 1 { // horizontal magnification tag
			DrawText(sf, 262, 2, fmt.Sprintf("Z%dx", hud.Zoom), colTrig, 1)
		}
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
		m := measure.Compute(sig, vdiv/25*probe, off*probe, hud.SampleS)
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
