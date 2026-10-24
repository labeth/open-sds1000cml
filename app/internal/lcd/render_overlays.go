// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// drawXY plots C1 (x) against C2 (y) — the Lissajous view (parity with the web
// X-Y mode). Codes 0..255 map across the graticule (x) and up it (y); a stride
// keeps dense records cheap.
// TRLC-LINKS: REQ-SDS-021
func drawXY(sf Surface, p plotArea, f *engine.Frame, hud HUD) {
	valid := frameValid(f)
	cap := !hud.UI.Layout.HideViewCaptions
	if len(f.C2) < valid {
		if cap {
			DrawText(sf, 10, p.y0+10, "X-Y needs CH2", colDim, 1)
		}
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
		x := int(float64(c1[i]) * float64(p.w-1) / 255.0)
		y := p.sy(float64(c2[i]))
		if px >= 0 {
			drawLine(sf, px, py, x, y, colMath)
		}
		px, py = x, y
	}
	if cap {
		DrawText(sf, 10, p.y0+10, "X:C1  Y:C2", colDim, 1)
	}
}

// mathScale is the math trace's displayed units per division. Products use
// four times the product of the channel scales, in V²/div; sums use C1 V/div.
// TRLC-LINKS: REQ-SDS-021
func mathScale(hud HUD) float64 {
	a := hud.C1VdivV * math.Max(hud.Probe1, 1)
	b := hud.C2VdivV * math.Max(hud.Probe2, 1)
	if a <= 0 {
		a = 1
	}
	if b <= 0 {
		b = 1
	}
	if hud.MathMode == 4 {
		return 4 * a * b
	}
	return a
}

// mathCodes combines calibrated input voltages, not offset display codes.
// The math trace has its own zero at screen centre.
// TRLC-LINKS: REQ-SDS-021
func mathCodes(c1, c2 []uint8, hud HUD) []uint8 {
	scale := mathScale(hud) / 25
	v1 := hud.C1VdivV * float64(max(hud.Zoom1, 1)) / 25 * math.Max(hud.Probe1, 1)
	v2 := hud.C2VdivV * float64(max(hud.Zoom2, 1)) / 25 * math.Max(hud.Probe2, 1)
	if v1 <= 0 {
		v1 = 1.0 / 25
	}
	if v2 <= 0 {
		v2 = 1.0 / 25
	}
	off1, off2 := hud.OffC1V*math.Max(hud.Probe1, 1), hud.OffC2V*math.Max(hud.Probe2, 1)
	if hud.Cpl1 != 0 {
		off1 = 0
	}
	if hud.Cpl2 != 0 {
		off2 = 0
	}
	// Each input's volts per code, tabled once; the mode chosen outside the
	// sample loop.
	var va, vb [256]float64
	for c := range va {
		va[c] = (float64(c)-128)*v1 - off1
		vb[c] = (float64(c)-128)*v2 - off2
	}
	m := make([]uint8, min(len(c1), len(c2)))
	c1, c2 = c1[:len(m)], c2[:len(m)]
	put := func(i int, v float64) { m[i] = uint8(math.Round(max(0, min(255, 128+v/scale)))) } // builtins: NaN propagates as in math.Max
	switch hud.MathMode {
	case 1:
		for i := range m {
			put(i, va[c1[i]]+vb[c2[i]])
		}
	case 2:
		for i := range m {
			put(i, va[c1[i]]-vb[c2[i]])
		}
	case 3:
		for i := range m {
			put(i, vb[c2[i]]-va[c1[i]])
		}
	case 4:
		for i := range m {
			put(i, va[c1[i]]*vb[c2[i]])
		}
	default:
		for i := range m {
			put(i, 0)
		}
	}
	return m
}

// TRLC-LINKS: REQ-SDS-021
func drawMath(sf Surface, p plotArea, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	valid := frameValid(f)
	if len(f.C2) < valid {
		return
	}
	c1 := coupledDisplay(f.C1[:valid], hud.Cpl1)
	c2 := coupledDisplay(f.C2[:valid], hud.Cpl2)
	m := mathCodes(c1, c2, hud)
	drawTraceID(sf, p, traceID{}, m, win, xc, f.Interp, colMath, posFrac)
	unit := "V/div"
	if hud.MathMode == 4 {
		unit = "V^2/div"
	}
	DrawText(sf, 300, 12, "M "+fmt.Sprintf("%.3g %s", mathScale(hud), unit), colMath, 1)
}

// drawRefs overlays the saved reference waveforms (REF A/B) as dim traces for
// comparison against the live trace (parity with the web REF A/B). A reference
// is the screen as saved, one point per column, so it is drawn across the
// plot and aligns while the timebase is unchanged. Its codes are re-read at
// the live V/div and offset, so it keeps the volts it was saved at. A is the
// info tint and B dim, distinct from the live channels and the purple math.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-138
func drawRefs(sf Surface, p plotArea, hud HUD) {
	cols := [2]uint16{colInfo, colDim}
	live := [2][2]float64{ // per channel: analog V/div, offset volts
		{hud.C1VdivV * float64(max(hud.Zoom1, 1)), hud.OffC1V},
		{hud.C2VdivV * float64(max(hud.Zoom2, 1)), hud.OffC2V},
	}
	liveProbe := [2]float64{max(hud.Probe1, 1), max(hud.Probe2, 1)}
	zoom := [2]int{hud.Zoom1, hud.Zoom2}
	for i := 0; i < 2; i++ {
		if !hud.RefShow[i] {
			continue
		}
		for ch, r := range [2][]uint8{hud.RefC1[i], hud.RefC2[i]} {
			if len(r) == 0 || (ch == 1 && !hud.TwoChan) {
				continue
			}
			codes := r
			if v := hud.RefVdiv[i][ch]; v > 0 && live[ch][0] > 0 {
				// its volts at the tip, through the probe it was stored with,
				// as the live input's through the probe on it now
				k := max(hud.RefProbe[i][ch], 1) / liveProbe[ch]
				codes = rescaleRef(r, v*k, hud.RefOff[i][ch]*k, live[ch][0], live[ch][1])
			}
			drawTraceID(sf, p, traceID{}, codes, len(codes), float64(len(codes))/2, false, cols[i], .5, zoom[ch])
		}
	}
}

// rescaleRef re-expresses codes saved at one analog V/div and offset at
// another: a code reads (code-128)*vdiv/25 - off volts. Off-range clips.
// TRLC-LINKS: REQ-SDS-021
func rescaleRef(r []uint8, vdiv, off, toVdiv, toOff float64) []uint8 {
	k, b := vdiv/toVdiv, (toOff-off)*25/toVdiv
	out := make([]uint8, len(r))
	for x, v := range r {
		out[x] = uint8(math.Round(math.Max(0, math.Min(255, 128+(float64(v)-128)*k+b))))
	}
	return out
}

// decodeColor maps a decode span kind to a display colour.
// TRLC-LINKS: REQ-SDS-021
func decodeColor(kind string) uint16 {
	switch kind {
	case "start", "stop", "ack":
		return colOK
	case "addr", "rw":
		return colTrig
	case "nak", "frame-error", "parity-error":
		return colStale
	case "gap":
		return colDim
	default: // data
		return colInfo
	}
}

// protoNames are the strip's protocol labels (1, Auto, is a detection the
// panel resolves to a protocol: never drawn).
var protoNames = []string{"", "", "UART", "I2C", "SPI", "MANCH", "SENT", "CAN", "1553", "ARINC", "USB", "FLEXR",
	"LIN", "DMX", "MIDI", "MODBUS", "1WIRE", "WS281X", "DHT", "CEC", "J1850", "NEC", "SIRC", "RC5", "RC6", "DALI",
	"USBPD", "SPDIF", "PS2", "SWD", "MDIO", "WIEG", "QUAD", "STEP", "PWM", "RC", "OOK", "MILLER", "MORSE", "DCF77",
	"EM4100", "EM4305", "T55XX", "SWIM", "SDQ", "PJON", "DSI", "QI", "SD", "PDI", "CJTAG", "MAPLE", "CALIP", "IR"}

// drawDecode runs the protocol decoder on the frame and draws the decoded byte
// spans in a strip under the plot (decodeStrip: the plot ends above it, as
// the web's decode lane). Only in Y-T; the span sample indices map to screen x through the
// same trace window. In BUS the selected packet is boxed with its list
// number and the packets holding the match bytes are ticked.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-214
func drawDecode(sf Surface, p plotArea, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if hud.DecProto == 0 || f == nil {
		return
	}
	if !(posFrac >= 0 && posFrac <= 1) { // same normalization as drawTrace/drawZoneMask:
		posFrac = 0.5 // an invalid fraction must not shift the strip off the trace
	}
	res := decodeFrame(f, hud)
	name := protoNames[hud.DecProto%len(protoNames)]
	// Decode lane: a dark band that sits ABOVE the bottom Vpp/freq readout row
	// (drawn later by drawHUD at H-9), so the two never overwrite each other.
	yLbl, yTxt, yBar := decodeStripTop+2, H-26, H-14 // label / byte hex / span bars, top→bottom
	fillRect(sf, 0, yLbl-2, p.w, 27, colBoxBg)
	if !res.OK {
		DrawText(sf, 10, yLbl, name+": "+res.Error, colStale, 1)
		return
	}
	label := name
	unit := "bytes"
	switch res.Proto {
	case "mil1553", "arinc429", "manchester":
		unit = "words"
	case "sent":
		unit = "nibbles"
	}
	head := fmt.Sprintf("%s  %d %s", label, len(res.Bytes), unit)
	DrawText(sf, 10, yLbl, head, colDim, 1)
	headEnd := 10 + TextWidth(head, 1) + 6 // the label row's marks start right of it
	// Map a sample index to screen x via the same window the trace uses.
	left := xc - float64(win)*posFrac
	sx := func(s float64) int { return int((s - left) * float64(p.w) / float64(win)) }
	lastEnd, lastLen := -1<<30, 0 // the last label drawn
	// A bus layer's notes caption the packets in the byte row instead of
	// their bytes (the bars stay): one transaction read as a whole.
	captions := hud.UI.Bus != nil && layerNotes(hud.UI.Bus.Marks)
	for i, s := range res.Spans {
		x0, x1 := sx(float64(s.I0)), sx(float64(s.I1))
		if x1 < 0 || x0 >= p.w {
			continue // off-screen
		}
		col := decodeColor(s.Kind)
		if x1 < x0+2 {
			x1 = x0 + 2
		}
		for x := max(x0, 0); x <= x1 && x < p.w; x++ { // span bar
			sf.SetPixel(x, yBar, col)
			sf.SetPixel(x, yBar+1, col)
		}
		// Byte text, but only if it fits before the next span — otherwise a
		// dense stream turns the strip into unreadable mush; the coloured bars
		// remain. Next to a multi-character label keep three blank columns
		// (one between a label's own characters), so back-to-back bytes do not
		// read as one number ("4869205555AA"); a label that then no longer
		// fits its span is skipped.
		nextX0 := p.w
		if i+1 < len(res.Spans) {
			nextX0 = sx(float64(res.Spans[i+1].I0))
		}
		gap := 0
		if len(s.Text) > 1 || lastLen > 1 {
			gap = 2
		}
		tx := max(x0+1, lastEnd+gap)
		if end := tx + len(s.Text)*6; !captions && x0 >= 0 && s.Text != "" && end <= p.w && end <= nextX0 {
			DrawText(sf, tx, yTxt, s.Text, col, 1)
			lastEnd, lastLen = end, len(s.Text)
		}
	}
	// BUS: the selected packet boxed (with its list number), the matches ticked
	if b := hud.UI.Bus; b != nil {
		if captions {
			drawLayerCaptions(sf, p, b.Marks, sx, yTxt)
		}
		for _, m := range b.Marks {
			x0, x1 := sx(float64(m.I0)), sx(float64(m.I1))
			if x1 < 0 || x0 >= p.w {
				continue
			}
			x1 = max(x1, x0+2)
			// the tick and the number in the strip's label row: above it
			// they would hide the trace's bottom
			if tx := max(2, x0); m.Match && tx-3 >= headEnd {
				fillTri(sf, tx, yLbl, 3, colTrig)
			}
			if m.Sel {
				strokeRect(sf, x0-2, yLbl+7, x1-x0+5, 20, colTrig)
				lab := fmt.Sprintf("#%d", b.SelNum)
				lw := TextWidth(lab, 1) + 4
				lx := min(max(x0+6, headEnd), p.w-lw-2)
				fillRect(sf, lx, yLbl-1, lw, 9, colBoxBg)
				DrawText(sf, lx+2, yLbl, lab, colTrig, 1)
			}
		}
	}
}

// layerNotes reports packets a bus layer has noted.
// TRLC-LINKS: REQ-SDS-241
func layerNotes(ms []wsview.PacketMark) bool {
	for _, m := range ms {
		if m.Text != "" {
			return true
		}
	}
	return false
}

// drawLayerCaptions writes each packet's layer note in the strip's byte row
// from the packet's start to the next packet's (or the screen's end), cut to
// fit; a note with fewer than four characters of room is left out.
// TRLC-LINKS: REQ-SDS-241
func drawLayerCaptions(sf Surface, p plotArea, ms []wsview.PacketMark, sx func(float64) int, y int) {
	for i, m := range ms {
		if m.Text == "" {
			continue
		}
		x0 := max(sx(float64(m.I0)), 0)
		lim := p.w - 2
		if i+1 < len(ms) {
			lim = min(lim, sx(float64(ms[i+1].I0))-4)
		}
		n := (lim - x0) / 6
		if x0 >= p.w || n < 4 {
			continue
		}
		col := colInfo
		if m.Err {
			col = colStale
		}
		DrawText(sf, x0, y, cutText(m.Text, n), col, 1)
	}
}

// fillTri draws a small filled triangle pointing down, its base at row y.
// TRLC-LINKS: REQ-SDS-214
func fillTri(sf Surface, x, y, h int, c uint16) {
	for dy := 0; dy <= h; dy++ {
		hline(sf, x-(h-dy), x+(h-dy), y+dy, c)
	}
}

// colMask is the mask/zone overlay colour — dim steel blue, under the traces.
var colMask = rgb(90, 120, 160)

// drawZoneMask overlays the installed zones (edge-anchored rectangles) and the
// mask envelope boundaries, mapped with the SAME window mapping as drawTrace
// (left = xc - win*posFrac; column j of the mask reads raw sample left+j), so
// LCD == web == engine test point. A frame sampled otherwise than the mask (a
// lease's full-rate record, the envelope after one, a zoom) of the mask's
// timebase reads the mask through trigger-relative time, as the engine tests
// it and the web draws it.
// TRLC-LINKS: REQ-SDS-021
func drawZoneMask(sf Surface, p plotArea, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = 0.5
	}
	left := xc - float64(win)*posFrac
	// mask envelope boundary lines
	mw := hud.MaskWin
	if hud.MaskMode > 0 && mw > 0 && len(hud.MaskLo) == mw && len(hud.MaskHi) == mw {
		if mw == win {
			for x := 0; x < p.w; x++ {
				j := x * win / p.w
				sf.SetPixel(x, p.sy(float64(hud.MaskLo[j])), colMask)
				sf.SetPixel(x, p.sy(float64(hud.MaskHi[j])), colMask)
			}
		} else if hud.MaskSampleS > 0 && f.SampleS > 0 && f.EdgeX >= 0 && hud.MaskTdivS == f.TdivS {
			maskPos := hud.TrigPosFrac
			if !(maskPos >= 0 && maskPos <= 1) {
				maskPos = 0.5
			}
			for x := 0; x < p.w; x++ {
				s := left + float64(x)*float64(win)/float64(p.w)
				j := int(math.Round((s-f.EdgeX)*f.SampleS/hud.MaskSampleS + float64(mw)*maskPos))
				if j < 0 || j >= mw {
					continue
				}
				sf.SetPixel(x, p.sy(float64(hud.MaskLo[j])), colMask)
				sf.SetPixel(x, p.sy(float64(hud.MaskHi[j])), colMask)
			}
		}
	}
	// zone rectangles: dt (seconds from the edge) -> raw sample -> screen x
	if len(hud.Zones) > 0 && f.SampleS > 0 && f.EdgeX >= 0 {
		for _, z := range hud.Zones {
			x0 := int((f.EdgeX + z.DtLoS/f.SampleS - left) * float64(p.w) / float64(win))
			x1 := int((f.EdgeX + z.DtHiS/f.SampleS - left) * float64(p.w) / float64(win))
			if x1 < x0 {
				x0, x1 = x1, x0
			}
			if x1 < 0 || x0 >= p.w {
				continue
			}
			x0, x1 = max(x0, 0), min(x1, p.w-1)
			y0 := p.sy(float64(z.CodeHi)) // higher code = higher on screen
			y1 := p.sy(float64(z.CodeLo))
			col := colOK // intersect: the frame must enter -> green
			if z.Avoid {
				col = colStale // avoid: the frame must miss -> red/orange
			}
			drawLine(sf, x0, y0, x1, y0, col)
			drawLine(sf, x0, y1, x1, y1, col)
			drawLine(sf, x0, y0, x0, y1, col)
			drawLine(sf, x1, y0, x1, y1, col)
		}
	}
}

// drawMaskHUD paints the mask pass/fail meter (top edge, under the liveness
// strip) whenever mask testing or the zone trigger is on, plus the panel's
// build/status line.
// TRLC-LINKS: REQ-SDS-021
func drawMaskHUD(sf Surface, hud HUD) {
	if hud.MaskMode == 0 && hud.MaskMsg == "" && hud.ZoneMode == 0 {
		return
	}
	line := ""
	if hud.MaskMode > 0 {
		line = fmt.Sprintf("MASK pass %d  FAIL %d", hud.MaskPass, hud.MaskFail)
		if hud.MaskSkip > 0 {
			line += fmt.Sprintf("  skip %d", hud.MaskSkip)
		}
		if hud.MaskStopped {
			line += "  STOPPED"
		}
	}
	if hud.ZoneMode > 0 {
		if line != "" {
			line += "   "
		}
		line += fmt.Sprintf("ZONE x%d", len(hud.Zones))
		if hud.ZoneSkip > 0 {
			line += fmt.Sprintf(" (skip %d)", hud.ZoneSkip)
		}
	}
	if hud.MaskMsg != "" {
		if line != "" {
			line += "   "
		}
		line += hud.MaskMsg
	}
	col := colMask
	if hud.MaskFail > 0 || hud.MaskStopped {
		col = colStale // failures flash the meter red/orange
	}
	// Second row, right-aligned under the address: on the top row a status
	// over ~30 characters runs into the device URL. Under a
	// workspace pane it sits at the top of the plot instead.
	y := 14
	if t := hud.UI.Layout.PlotTop; t > 0 {
		y = t + 14
	}
	DrawTextRight(sf, min(664, plotOf(hud.UI.Layout).w-4), y, line, col, 1)
}

// decodeFrame decodes the frame's protocol through the shared per-frame
// cache (framecalc.Decode), so the strip and the BUS packet list decode a
// frame once, all of its record: the strip's spans are the list's, in record
// sample indices, and the screen shows those it reaches.
// TRLC-LINKS: REQ-SDS-018
func decodeFrame(f *engine.Frame, hud HUD) decode.Result {
	return framecalc.Decode(f, decodeParams(hud))
}

// decodeParams are the HUD's decode settings: the panel's (framecalc.Params),
// so the strip shares the packet list's decode of the record and its bytes.
// TRLC-LINKS: REQ-SDS-018
func decodeParams(hud HUD) framecalc.DecodeParams {
	return framecalc.Params(hud.DecProto, hud.DecBaud, hud.DecChA, hud.DecChB, hud.DecCPOL, hud.DecCPHA, hud.DecLSB, hud.DecFormat, hud.DecDelayNs, hud.DecParity)
}
