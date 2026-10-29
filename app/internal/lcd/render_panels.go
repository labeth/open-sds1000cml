// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
	"open-sds/app/internal/panel/wsview"
)

// measFor throttles the full-record auto-measurement to readout rate. The HUD
// bottom line and the MEASURE panel both recomputed measure.Compute (a full
// min/max/sum/sum²/histogram pass over 20480 samples, per channel) at EVERY LCD
// render (~8 Hz) — 11% of the core on the device profile, for a text readout
// that only needs to refresh a few times a second. Within one frame (same Seq +
// params) both surfaces share the identical Result, keeping HUD↔panel agreement
// exact; across frames the previous Result is reused until measRefresh elapses.
// The scale/coupling params key the cache so a knob turn recomputes immediately.
// Seq==0 (hand-built test frames) bypasses the cache entirely. The mutex covers
// the /api/screen.png render running on the HTTP goroutine.
const measRefresh = 250 * time.Millisecond

var (
	measMu    sync.Mutex
	measSlots [2]struct {
		seq, content uint64
		at           time.Time
		vpc, off, ss float64
		probe        float64
		cpl          int
		m            *measure.Result
	}
)

// TRLC-LINKS: REQ-SDS-021
func measFor(ch int, sig []uint8, q []uint16, guard int, seq, content uint64, vpc, off, ss, probe float64, cpl int) *measure.Result {
	if seq == 0 || ch < 0 || ch > 1 {
		return measure.ComputeAcquisition(sig, q, vpc, off, ss, cpl, guard)
	}
	measMu.Lock()
	defer measMu.Unlock()
	c := &measSlots[ch]
	// The same record re-shown at a new V/div or offset reads the same volts:
	// a knob turn on a deep stopped record must not re-measure it.
	if c.m != nil && content != 0 && c.content == content && c.probe == probe && c.ss == ss && c.cpl == cpl {
		return c.m
	}
	if c.m != nil && c.vpc == vpc && c.off == off && c.ss == ss && c.cpl == cpl {
		if seq == c.seq || time.Since(c.at) < measRefresh {
			return c.m
		}
	}
	m := measure.ComputeAcquisition(sig, q, vpc, off, ss, cpl, guard)
	c.seq, c.content, c.at, c.vpc, c.off, c.ss, c.probe, c.cpl, c.m = seq, content, time.Now(), vpc, off, ss, probe, cpl, m
	return m
}

// timedSlots hold each channel's envelope timing with the measurement it was
// taken on: while measFor answers with the same measurement (the same frame,
// or the readouts' refresh not yet due) the timing is that one's too. Taken
// again for every readout of every picture it was a tenth of the core.
var timedSlots [2]struct {
	base, timed  *measure.Result
	vpc, off, ss float64
}

// timedFor is take(base), or the one taken on the same base and scale.
// TRLC-LINKS: REQ-SDS-021
func timedFor(ch int, base *measure.Result, cache bool, vpc, off, ss float64, take func(*measure.Result) *measure.Result) *measure.Result {
	if !cache || base == nil || ch < 0 || ch > 1 {
		return take(base)
	}
	measMu.Lock()
	defer measMu.Unlock()
	t := &timedSlots[ch]
	if t.base != base || t.vpc != vpc || t.off != off || t.ss != ss {
		t.base, t.timed, t.vpc, t.off, t.ss = base, take(base), vpc, off, ss
	}
	return t.timed
}

// chanMeasure is one channel's calibrated measurement of the frame, as the
// readouts show it (the probe-scaled V/div, the software coupling, the
// display invert); nil without the channel.
// TRLC-LINKS: REQ-SDS-021
func chanMeasure(f *engine.Frame, hud HUD, ch int) *measure.Result {
	valid := frameValid(f)
	if ch == 1 && len(f.C2) < valid {
		return nil
	}
	sig := f.C1[:valid]
	vdiv, probe, off, cpl := hud.C1VdivV, hud.Probe1, hud.OffC1V, hud.Cpl1
	if ch == 1 {
		sig = f.C2[:valid]
		vdiv, probe, off, cpl = hud.C2VdivV, hud.Probe2, hud.OffC2V, hud.Cpl2
	}
	if probe < 1 {
		probe = 1
	}
	if cpl != analog.CplDC { // match the displayed (coupled) trace
		sig = analog.CoupleDisplay(sig, cpl)
		off = 0
	}
	vpc := vdiv * float64(zoomOf(hud, ch)) / 25 * probe
	q := frameQ(f, ch)
	if a, b := f.MeasureRange(); b-a < len(sig) { // a roll view's captured stretch
		sig = sig[a:b]
		if len(q) >= b {
			q = q[a:b]
		} else {
			q = nil
		}
	}
	m := measFor(ch, sig, q, f.FilterGuard, f.Seq, f.Content, vpc, off*probe, hud.SampleS, probe, cpl)
	if f.PeakDetect {
		m = timedFor(ch, m, f.Seq != 0, vpc, off*probe, hud.SampleS, func(m *measure.Result) *measure.Result {
			return m.WithEnvelopeTiming(sig, vpc, off*probe, hud.SampleS).WithEdgeDetail(vpc, off*probe, f.EdgeWindows(ch)...)
		})
	}
	// After the timing: the invert swaps rise and fall and complements the
	// duty, which the envelope's timing would otherwise put back.
	if (ch == 0 && hud.Inv1) || (ch == 1 && hud.Inv2) {
		m = m.Inverted() // measure what the inverted trace shows
	}
	return m
}

// measRow is one readout: its short name and value text.
type measRow struct{ name, value string }

// measParamRow reads one parameter ("pkpk", "freq", ...) of a measurement,
// ok false when the measurement has none. The names, the order and the
// percent figures are the MEASURE pane's and the web's, so one parameter reads
// the same everywhere.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-217
func measParamRow(m *measure.Result, id string) (measRow, bool) {
	pct := func(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) + "%" }
	switch id {
	case "pkpk":
		return measRow{"Vpp", fmtVolt(m.Vpp)}, true
	case "max":
		return measRow{"Vmax", fmtVolt(m.Vmax)}, true
	case "min":
		return measRow{"Vmin", fmtVolt(m.Vmin)}, true
	case "ampl":
		return measRow{"Vampl", fmtVolt(m.Vampl)}, true
	case "top":
		return measRow{"Vtop", fmtVolt(m.Vtop)}, true
	case "base":
		return measRow{"Vbase", fmtVolt(m.Vbase)}, true
	case "rms": // an envelope of a signal faster than its buckets holds no RMS or mean
		return measRow{"Vrms", fmtVolt(m.Vrms)}, !m.EnvAliased
	case "acrms":
		return measRow{"ACrms", fmtVolt(m.VrmsAC)}, !m.EnvAliased
	case "mean":
		return measRow{"Vmean", fmtVolt(m.Vmean)}, !m.EnvAliased
	case "freq":
		return measRow{"Freq", fmtFreq(m.Freq)}, m.HasTiming
	case "period":
		return measRow{"Period", fmtTdiv(m.Period)}, m.HasTiming
	case "duty":
		return measRow{"Duty", pct(m.Duty)}, m.HasTiming
	case "rise":
		return measRow{"Rise", fmtTdiv(m.RiseS)}, m.RiseS > 0
	case "fall":
		return measRow{"Fall", fmtTdiv(m.FallS)}, m.FallS > 0
	case "pwid":
		return measRow{"+Width", fmtTdiv(m.PosWidthS)}, m.PosWidthS > 0
	case "nwid":
		return measRow{"-Width", fmtTdiv(m.NegWidthS)}, m.NegWidthS > 0
	case "ovsh": // an envelope alone holds no edge shape (the MEASURE pane's "---")
		return measRow{"Overshoot", pct(m.Overshoot)}, m.EdgeShape || m.HasTiming && !m.EnvTiming
	case "prsh":
		return measRow{"Preshoot", pct(m.Preshoot)}, m.EdgeShape || m.HasTiming && !m.EnvTiming
	}
	return measRow{}, false
}

// allMeasParams is every parameter, in the readout box's order: the web's.
var allMeasParams = []string{"pkpk", "max", "min", "mean", "rms", "freq", "period", "duty",
	"acrms", "top", "base", "ampl", "rise", "fall", "pwid", "nwid", "ovsh", "prsh"}

// drawMeasPanel draws the MEASURE readout boxes: per shown channel, a box of
// its chosen parameters (every parameter when nothing is chosen), via the
// shared measure package so the values match the web exactly.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-217
func drawMeasPanel(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	if f == nil || f.Valid == 0 {
		return
	}
	sc1, sc2 := hud.shownChans()
	x := 4
	for ch, shown := range [2]bool{sc1, sc2 && hud.TwoChan} {
		if !shown {
			continue
		}
		params := allMeasParams
		if hud.MeasItems != nil {
			params = nil
			for _, it := range hud.MeasItems {
				if len(it) >= 2 && it[0] == byte('1'+ch) && it[1] == ':' { // "<ch>:<param>"
					params = append(params, it[2:])
				}
			}
			if len(params) == 0 {
				continue
			}
		}
		if measBox(sf, p, f, hud, ch, x, params) {
			x += 116
		}
	}
}

// measBox renders one channel's readouts at left edge x.
// TRLC-LINKS: REQ-SDS-021
func measBox(sf Surface, p plotArea, f *engine.Frame, hud HUD, ch, x int, params []string) bool {
	m := chanMeasure(f, hud, ch)
	if m == nil {
		return false
	}
	rows := make([]measRow, 0, len(params))
	for _, id := range params {
		r, ok := measParamRow(m, id)
		if !ok {
			if hud.MeasItems == nil {
				continue // the full box lists only what the signal has
			}
			r.value = "---"
		}
		rows = append(rows, r)
	}
	col, name := colC1, "C1"
	if ch == 1 {
		col, name = colC2, "C2"
	}
	y0, w := p.y0+22, 112
	h := 14 + len(rows)*10
	fillRect(sf, x, y0, w, h, colBoxBg)
	vline(sf, x, y0, y0+h-1, colGrid)
	vline(sf, x+w-1, y0, y0+h-1, colGrid)
	DrawText(sf, x+3, y0+2, name+" MEAS", col, 1)
	for i, r := range rows {
		yy := y0 + 12 + i*10
		DrawText(sf, x+3, yy, r.name, colInfo, 1)
		DrawTextRight(sf, x+w-3, yy, r.value, colInfo, 1)
	}
	return true
}

// edgeArrow draws a small filled arrow with its tip at (x, y), pointing
// along (dx, dy): one of left, right, up, down.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-217
func edgeArrow(sf Surface, x, y, dx, dy int, c uint16) {
	for d := 0; d <= 6; d++ {
		for w := -d / 2; w <= d/2; w++ {
			sf.SetPixel(x-dx*d+w*absI(dy), y-dy*d+w*absI(dx), c)
		}
	}
}

// drawCursors draws the on-screen X (time) or Y (volts) cursor pair and a Δ
// readout. Time Δ uses the labelled t/div × 10 divisions; volts Δ uses each
// shown channel's probe-scaled V/div × 8 divisions.
// TRLC-LINKS: REQ-SDS-021
func drawCursors(sf Surface, p plotArea, hud HUD) {
	if !hud.CurOn {
		return
	}
	dash := func(vertical bool, at int, active bool) {
		// The inactive cursor is dim, not the grid colour: in colGrid it
		// vanishes into the graticule.
		col := colDim
		if active {
			col = colTrig
		}
		if vertical {
			for y := p.top; y <= p.bot; y += 2 {
				sf.SetPixel(at, y, col)
			}
		} else {
			for x := 0; x < p.w; x += 2 {
				sf.SetPixel(x, at, col)
			}
		}
	}
	// A cursor stays on its signal time or level: beyond the screen it shows
	// as an arrow at the side it is beyond, with its letter.
	off := func(vertical bool, f float64, k int) bool {
		col := colDim
		if hud.CurSel == k {
			col = colTrig
		}
		lab := string(rune('A' + k))
		switch {
		case vertical && f < 0:
			y := p.top + 30 + 14*k
			edgeArrow(sf, 0, y, -1, 0, col)
			DrawText(sf, 9, y-3, lab, col, 1)
		case vertical && f > 1:
			y := p.top + 30 + 14*k
			edgeArrow(sf, p.w-1, y, 1, 0, col)
			DrawText(sf, p.w-15, y-3, lab, col, 1)
		case !vertical && f < 0:
			x := p.w/2 + 60 + 20*k
			edgeArrow(sf, x, p.y0, 0, -1, col)
			DrawText(sf, x+8, p.y0+2, lab, col, 1)
		case !vertical && f > 1:
			x, y := p.w/2+60+20*k, min(p.last(), H-12) // above the bottom readout row
			edgeArrow(sf, x, y, 0, 1, col)
			DrawText(sf, x+8, y-8, lab, col, 1)
		default:
			return false
		}
		return true
	}
	var label string
	if hud.CurType == 0 { // X (time) cursors
		for k := 0; k < 2; k++ {
			if !off(true, hud.CurX[k], k) {
				dash(true, int(hud.CurX[k]*float64(p.w-1)), hud.CurSel == k)
			}
		}
		span := hud.TdivS * 10
		if hud.Zoom > 1 {
			span /= float64(hud.Zoom)
		}
		dt := math.Abs(hud.CurX[0]-hud.CurX[1]) * span
		label = "dt " + fmtTdiv(dt)
		if dt > 0 {
			label += "  1/dt " + fmtFreq(1/dt)
		}
	} else { // Y (volts) cursors — report ΔV for BOTH channels (web parity)
		for k := 0; k < 2; k++ {
			if !off(false, hud.CurY[k], k) {
				dash(false, p.top+int(hud.CurY[k]*float64(p.bot-p.top)), hud.CurSel == k)
			}
		}
		frac := math.Abs(hud.CurY[0] - hud.CurY[1])
		dvOf := func(vdiv, probe float64) string {
			if probe < 1 {
				probe = 1
			}
			return fmtVolt(frac * vdiv * probe * 8) // 8 vertical divisions
		}
		sc1, sc2 := hud.shownChans()
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
	x := p.w/2 - w/2 // top-centre, clear of the MEAS boxes (left) and the right column
	fillRect(sf, x, p.y0+22, w, 12, colBoxBg)
	DrawText(sf, x+3, p.y0+24, label, colTrig, 1)
}

// drawAutosetBanner overlays a centred "AUTOSET…" progress banner while the
// sweep runs, with the cancel hint (a second AUTO press stops it).
// TRLC-LINKS: REQ-SDS-021
func drawAutosetBanner(sf Surface, p plotArea, msg string) {
	if msg == "" {
		msg = "AUTOSET..."
	}
	const bw, bh = 300, 44
	x0, y0 := (p.w-bw)/2, (H-bh)/2
	fillRect(sf, x0, y0, bw, bh, colMenuBg)
	strokeRect(sf, x0, y0, bw, bh, colTrig)
	DrawText(sf, x0+(bw-TextWidth(msg, 2))/2, y0+6, msg, colTrig, 2)
	// Only the working banner is cancelable; a result note (e.g. "no signal")
	// just clears on its own.
	hint := "AUTO again to cancel"
	if msg != "AUTOSET..." {
		hint = "check the probe / scale"
	}
	DrawText(sf, x0+(bw-TextWidth(hint, 1))/2, y0+30, hint, colDim, 1)
}

// drawNotice is a short notice over the plot: what a tool just did (a
// workspace's entry behaviour, a zoom, PRINT's saved file name).
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-211
func drawNotice(sf Surface, p plotArea, msg string) {
	maxC := (p.w - 40) / 6
	if len(msg) > maxC {
		msg = msg[:maxC-2] + ".."
	}
	bw, bh := TextWidth(msg, 1)+16, 20
	x0, y0 := (p.w-bw)/2, (H-bh)/2
	fillRect(sf, x0, y0, bw, bh, colMenuBg)
	strokeRect(sf, x0, y0, bw, bh, colInfo)
	DrawText(sf, x0+8, y0+6, msg, colInfo, 1)
}

// drawRemote is the one-line note of a change made from the phone remote,
// at the top of the plot so it does not cover the trace's middle.
// TRLC-LINKS: REQ-SDS-212
func drawRemote(sf Surface, p plotArea, msg string) {
	maxC := (p.w - 40) / 6
	if len(msg) > maxC {
		msg = msg[:maxC-2] + ".."
	}
	bw, bh := TextWidth(msg, 1)+12, 14
	x0, y0 := (p.w-bw)/2, p.y0+36
	fillRect(sf, x0, y0, bw, bh, colMenuBg)
	strokeRect(sf, x0, y0, bw, bh, colSR)
	DrawText(sf, x0+6, y0+4, msg, colSR, 1)
}

// softkeyY is the on-screen Y (slot centre) of each physical F1..F5 button,
// measured on the unit against the bezel (F1..F4 an 80 px pitch, F5 +90).
var softkeyY = [5]int{80, 160, 240, 320, 410}

// drawMarkers overlays the trigger level (horizontal line + right handle), the
// trigger position (top pointer), and the per-channel ground/offset arrows on
// the left edge — the same references the web canvas shows (spec 07 §6).
// TRLC-LINKS: REQ-SDS-021
func drawMarkers(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	px := func(x, y int, c uint16) {
		if x >= 0 && x < p.w && y >= p.y0 && y < H {
			sf.SetPixel(x, y, c)
		}
	}
	// Trigger LEVEL: dashed horizontal line + right-edge arrow at the level code.
	ly := p.sy(128 + hud.TrigLvlDiv*25)
	if ly >= p.y0+2 && ly < H {
		for x := 0; x < p.w; x += 7 {
			px(x, ly, colTrig)
			px(x+1, ly, colTrig)
		}
		for dy := -4; dy <= 4; dy++ {
			for dx := 0; dx < 5-absI(dy); dx++ {
				px(p.w-1-dx, ly+dy, colTrig)
			}
		}
	}
	// Trigger POSITION: downward pointer at the top (none while a zoom's
	// window leaves the trigger off the screen).
	tx := trigX(p, f, hud)
	for dy := 0; dy <= 7 && tx >= 0; dy++ {
		hw := (7 - dy) / 2
		for dx := -hw; dx <= hw; dx++ {
			px(tx+dx, p.y0+dy+2, colTrig)
		}
	}
	// Per-channel GROUND (0 V) arrows on the left edge: code = 128 + offV·25/Vdiv.
	ground := func(vdiv, offV float64, col uint16) {
		if vdiv <= 0 {
			return
		}
		gy := p.sy(128 + offV*25/vdiv)
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

// trigX is the trigger position's column, -1 when the screen does not show
// it (trigFrac).
// TRLC-LINKS: REQ-SDS-021
func trigX(p plotArea, f *engine.Frame, hud HUD) int {
	frac, ok := trigFrac(f, hud)
	if !ok {
		return -1
	}
	return min(int(frac*float64(p.w)), p.w-1)
}

// trigFrac is where across the screen the trigger shows: the trigger
// position, or zoomed, where the trace's window (traceWindow, centred on the
// trigger plus the pan) puts it; false when that is off the screen.
// TRLC-LINKS: REQ-SDS-021
func trigFrac(f *engine.Frame, hud HUD) (float64, bool) {
	frac := hud.TrigPosFrac
	if !(frac >= 0 && frac <= 1) {
		frac = 0.5
	}
	if hud.Zoom > 1 && f != nil && len(f.C1) > 0 {
		valid := frameValid(f)
		win, xc, posFrac := traceWindow(f, hud, valid)
		anchor := f.EdgeX
		if anchor < 0 {
			anchor = float64(valid) / 2
		}
		frac = (anchor - (xc - float64(win)*posFrac)) / float64(win)
	}
	return frac, frac >= 0 && frac <= 1
}

// TRLC-LINKS: REQ-SDS-021
func drawHUD(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	if f != nil && f.NoiseGainIdeal > 1 {
		label := fmt.Sprintf("Ideal +%.2f bits (not ENOB)", math.Log2(f.NoiseGainIdeal))
		if f.PassbandHz > 0 {
			label += "  pass " + fmtFreq(f.PassbandHz) + fmt.Sprintf("  FPGA /%d", f.Decimation)
		}
		DrawText(sf, 200, 14, label, colInfo, 1)
	}
	DrawText(sf, 4, 2, "C1 "+vdivLabel(hud.C1VdivV, hud.Probe1)+cplTag(hud.Cpl1), colC1, 1)
	if hud.TwoChan {
		DrawText(sf, 96, 2, "C2 "+vdivLabel(hud.C2VdivV, hud.Probe2)+cplTag(hud.Cpl2), colC2, 1)
	}
	if f != nil && f.Valid > 0 { // clipping warning — railed traces make readings suspect
		clip := frameClipped(f)
		if clip[0] {
			DrawText(sf, 4, 14, "CLIP", colTrig, 1)
		}
		if hud.TwoChan && clip[1] {
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
	case hud.ViewMode == 3:
		DrawText(sf, 200, 2, "BODE", colMath, 1)
	case hud.ViewMode == 4:
		DrawText(sf, 200, 2, "SPGM", colMath, 1)
	default:
		DrawText(sf, 200, 2, "M "+fmtTdiv(hud.TdivS), colInfo, 1)
		if hud.Zoom > 1 { // horizontal magnification tag
			DrawText(sf, 262, 2, fmt.Sprintf("x%d", hud.Zoom), colTrig, 1) // "Z50x" read as 250x
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
	case hud.Roll:
		state = "ROLL" // untriggered scrolling view (slow timebase)
	case hud.Waiting:
		state = "WAIT" // the held frame's T'D is stale
	case hud.Trigd:
		state = "T'D"
	case hud.Norm:
		state = "WAIT"
	}
	readout := fmt.Sprintf("T C%d %s %s %s", hud.TrigSrc+1, edge, fmtVolt(hud.TrigLvlV), state)
	if tag := trigTag(hud); tag != "" {
		readout = fmt.Sprintf("T %s %s", tag, state)
	}
	DrawTextRight(sf, 796, 2, readout, colTrig, 1)

	// Device URL (post-takeover support: "what do I browse to?"). Right-aligned
	// on the top bar in the dead zone between the math legend (ends ≤ ~345) and
	// the trigger readout (starts ≥ ~670) — clear of the trace band, readouts
	// and the menu panel in every view. No network → nothing.
	DrawStatusSlot(sf, hud)

	if f == nil || f.Valid == 0 || hud.UI.Layout.HideBottom {
		return
	}
	// Bottom quick-readout: calibrated Vpp + frequency per channel, via the
	// shared measure package (real probe-scaled V/div + software coupling) so it
	// agrees with the MEASURE readouts and the web.
	sc1, sc2 := hud.shownChans() // a hidden channel has no readout
	for ch, shown := range [2]bool{sc1, sc2 && hud.TwoChan} {
		if !shown {
			continue
		}
		m := chanMeasure(f, hud, ch)
		if m == nil {
			continue
		}
		col, name, x := colC1, "C1", 4
		if ch == 1 {
			col, name, x = colC2, "C2", 410
		}
		s := name + " Vpp " + fmtVolt(m.Vpp)
		if m.HasTiming {
			s += "  f " + fmtFreq(m.Freq)
		}
		DrawText(sf, x, H-9, s, col, 1)
	}
}

// TRLC-LINKS: REQ-SDS-021
func frameQ(f *engine.Frame, ch int) []uint16 {
	if ch == 1 {
		return f.Q2
	}
	return f.Q1
}

// zoomOf is a channel's display zoom: codes sit on the analog range, a
// zoomed detent's volts per code is VdivV·zoom/25.
// TRLC-LINKS: REQ-SDS-021
func zoomOf(hud HUD, ch int) int {
	z := hud.Zoom1
	if ch == 1 {
		z = hud.Zoom2
	}
	if z < 1 {
		return 1
	}
	return z
}

// clipCache holds the last published frame's rail state: scanning a stopped
// 1 M-sample record on every repaint takes as long as drawing it. A published
// frame's Seq is unique; Seq 0 is never cached.
var clipCache struct {
	sync.Mutex
	seq   uint64
	valid int
	clip  [2]bool
}

// frameClipped reports whether each channel's codes touch a rail.
// TRLC-LINKS: REQ-SDS-021
func frameClipped(f *engine.Frame) [2]bool {
	valid := frameValid(f) // clamp to len(C1) — f.Valid can exceed the slice
	clipCache.Lock()
	defer clipCache.Unlock()
	if f.Seq != 0 && clipCache.seq == f.Seq && clipCache.valid == valid {
		return clipCache.clip
	}
	var clip [2]bool
	clip[0] = measure.Clipped(f.C1[:valid])
	clip[1] = len(f.C2) >= valid && measure.Clipped(f.C2[:valid])
	clipCache.seq, clipCache.valid, clipCache.clip = f.Seq, valid, clip
	return clip
}

// DrawStatusSlot draws the top bar's status slot (between the math legend and
// the trigger readout): the loading indicator while a frame is being read or
// processed, else the device URL.
// TRLC-LINKS: REQ-SDS-168
func DrawStatusSlot(sf Surface, hud HUD) {
	if txt := statusSlotText(hud); txt != "" {
		c := colDim
		if hud.Busy != "" {
			c = colTrig
		}
		DrawTextRight(sf, 664, 2, txt, c, 1)
	}
}

// RedrawStatusSlot clears the slot (as wide as its widest text) and draws it
// again, so the LCD loop can turn the spinner without re-rendering a deep
// frame — which would steal the CPU from the very readout being shown.
// TRLC-LINKS: REQ-SDS-168
func RedrawStatusSlot(sf Surface, hud HUD) {
	idle := hud
	idle.Busy = ""
	busy := hud
	busy.Busy, busy.BusyPct = "x", 100
	w := max(TextWidth(statusSlotText(idle), 1), TextWidth(statusSlotText(busy), 1))
	fillRect(sf, 664-w, 0, w, 11, colBG)
	DrawStatusSlot(sf, hud)
}

// TRLC-LINKS: REQ-SDS-168
func statusSlotText(hud HUD) string {
	if hud.Busy == "" {
		return hud.URL
	}
	spin := `|/-\`[hud.BusyPhase&3 : hud.BusyPhase&3+1]
	if hud.BusyPct >= 0 {
		return fmt.Sprintf("LOADING %2.0f%% %s", hud.BusyPct, spin)
	}
	return "LOADING " + spin
}

// colorOf maps a wsview colour to the palette.
// TRLC-LINKS: REQ-SDS-211
func colorOf(c wsview.Col) uint16 {
	switch c {
	case wsview.Dim:
		return colDim
	case wsview.Amber:
		return colTrig
	case wsview.Stop:
		return colStale
	case wsview.Run:
		return colOK
	case wsview.Warn:
		return colWarn
	case wsview.C1:
		return colC1
	case wsview.C2:
		return colC2
	case wsview.Math:
		return colMath
	case wsview.SR:
		return colSR
	case wsview.Mask:
		return colMask
	case wsview.Screen:
		return colBG
	case wsview.Cursor:
		return colCursor
	}
	return colInfo
}

// trigTag names a trigger that is no plain edge for the readout: the serial
// match, the pulse or slope condition, the video line, a zone trigger ("" for
// an edge). Without it the readout said "T C1 ^ 1.37V" on the owner's serial
// trigger on AA, as for any edge.
// TRLC-LINKS: REQ-SDS-021
func trigTag(hud HUD) string {
	cond := func(c int, minNs, maxNs float64) string {
		switch c {
		case 1:
			return "<" + fmtTdiv(minNs*1e-9)
		case 2:
			return ">" + fmtTdiv(maxNs*1e-9)
		case 3:
			return fmtTdiv(minNs*1e-9) + "-" + fmtTdiv(maxNs*1e-9)
		}
		return "any"
	}
	src := fmt.Sprintf(" C%d", hud.TrigSrc+1)
	q := hud.TrigQual
	switch {
	case hud.SerialArmed:
		m := hud.SerialMatch
		if len(m) > 11 {
			m = m[:10] + ".."
		}
		if m == "" {
			m = "any"
		}
		return "SER " + m
	case hud.ZoneTrig:
		return "ZONE" + src
	case hud.TrigType == 1:
		return "PULSE " + cond(q.PulseCond, q.PulseMinNs, q.PulseMaxNs) + src
	case hud.TrigType == 2:
		return "SLOPE " + cond(q.SlopeCond, q.SlopeMinNs, q.SlopeMaxNs) + src
	case hud.TrigType == 3:
		return fmt.Sprintf("VIDEO L%d%s", q.VideoLine, src)
	}
	return ""
}
