// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"strings"
)

// drawXY plots C1 (x) against C2 (y) — the Lissajous view (parity with the web
// X-Y mode). Codes 0..255 map across the graticule (x) and up it (y); a stride
// keeps dense records cheap.
// TRLC-LINKS: REQ-SDS-021
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
	m := make([]uint8, min(len(c1), len(c2)))
	for i := range m {
		a, b := (float64(c1[i])-128)*v1-off1, (float64(c2[i])-128)*v2-off2
		var v float64
		switch hud.MathMode {
		case 1:
			v = a + b
		case 2:
			v = a - b
		case 3:
			v = b - a
		case 4:
			v = a * b
		}
		m[i] = uint8(math.Round(math.Max(0, math.Min(255, 128+v/scale))))
	}
	return m
}

// TRLC-LINKS: REQ-SDS-021
func drawMath(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	valid := frameValid(f)
	if len(f.C2) < valid {
		return
	}
	c1 := coupledDisplay(f.C1[:valid], hud.Cpl1)
	c2 := coupledDisplay(f.C2[:valid], hud.Cpl2)
	m := mathCodes(c1, c2, hud)
	drawTrace(sf, m, win, xc, f.Interp, colMath, posFrac)
	unit := "V/div"
	if hud.MathMode == 4 {
		unit = "V^2/div"
	}
	DrawText(sf, 300, 12, "M "+fmt.Sprintf("%.3g %s", mathScale(hud), unit), colMath, 1)
}

// drawRefs overlays the saved reference waveforms (REF A/B) as dim traces for
// comparison against the live trace (parity with the web REF A/B). A reference
// is the screen as saved, one point per column, so it is drawn 1:1 across the
// screen and aligns while the timebase is unchanged. Its codes are re-read at
// the live V/div and offset, so it keeps the volts it was saved at. A is the
// info tint and B dim, distinct from the live channels and the purple math.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-138
func drawRefs(sf Surface, hud HUD) {
	cols := [2]uint16{colInfo, colDim}
	live := [2][2]float64{ // per channel: analog V/div, offset volts
		{hud.C1VdivV * float64(max(hud.Zoom1, 1)), hud.OffC1V},
		{hud.C2VdivV * float64(max(hud.Zoom2, 1)), hud.OffC2V},
	}
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
				codes = rescaleRef(r, v, hud.RefOff[i][ch], live[ch][0], live[ch][1])
			}
			drawTrace(sf, codes, len(codes), float64(len(codes))/2, false, cols[i], .5, zoom[ch])
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

// drawDecode runs the protocol decoder on the frame and draws the decoded byte
// spans in a strip below the trace (parity with the web decode overlay). Only in
// Y-T; the span sample indices map to screen x through the same trace window.
// TRLC-LINKS: REQ-SDS-021
func drawDecode(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if hud.DecProto == 0 || f == nil {
		return
	}
	if !(posFrac >= 0 && posFrac <= 1) { // same normalization as drawTrace/drawZoneMask:
		posFrac = 0.5 // an invalid fraction must not shift the strip off the trace
	}
	valid := frameValid(f)
	ch := func(c int) []uint8 {
		if c == 1 && len(f.C2) >= valid {
			return f.C2[:valid]
		}
		return f.C1[:valid]
	}
	format := []string{"hex", "ascii", "both"}[hud.DecFormat%3]
	var res decode.Result
	switch hud.DecProto { // 0=off 1=Auto 2=UART 3=I2C 4=SPI
	case 1: // Auto: detect protocol / channel roles / sub-settings from the signal
		// Strided: a full Autodetect of a 500 k-sample screen took 14 s on
		// the scope's ARM and held the frame all that time.
		var st float64
		res, st = decode.AutodetectFast(ch(0), ch(1), f.SampleS, format)
		if k := int(st/f.SampleS + 0.5); k > 1 {
			for i := range res.Spans { // back to the frame's sample indices
				res.Spans[i].I0 *= k
				res.Spans[i].I1 *= k
			}
			res.SPB *= float64(k)
		}
	case 2:
		res = decode.DecodeUART(ch(hud.DecChA), f.SampleS, decode.UARTCfg{Baud: hud.DecBaud, Format: format})
	case 3:
		res = decode.DecodeI2C(ch(hud.DecChA), ch(hud.DecChB), f.SampleS, decode.I2CCfg{Format: format})
	case 4:
		res = decode.DecodeSPI(ch(hud.DecChA), ch(hud.DecChB), f.SampleS, decode.SPICfg{CPOL: hud.DecCPOL, CPHA: hud.DecCPHA, MSB: true, Format: format})
	case 5:
		res = decode.DecodeManchester(ch(hud.DecChA), f.SampleS, decode.ManchesterCfg{Bitrate: hud.DecBaud, IEEE: true, Format: format})
	case 6:
		res = decode.DecodeSENT(ch(hud.DecChA), f.SampleS, decode.SENTCfg{})
	case 7:
		res = decode.DecodeCANFD(ch(hud.DecChA), f.SampleS, decode.CANFDCfg{NominalBaud: hud.DecBaud, DominantLow: true})
	case 8:
		res = decode.DecodeMIL1553(ch(hud.DecChA), f.SampleS, decode.MIL1553Cfg{Bitrate: hud.DecBaud})
	case 9:
		res = decode.DecodeARINC429(ch(hud.DecChA), f.SampleS, decode.ARINC429Cfg{Bitrate: hud.DecBaud})
	case 10:
		res = decode.DecodeUSBLS(ch(hud.DecChA), f.SampleS, decode.USBLSCfg{Bitrate: hud.DecBaud})
	case 11:
		res = decode.DecodeFlexRay(ch(hud.DecChA), f.SampleS, decode.FlexRayCfg{Bitrate: hud.DecBaud})
	}
	name := []string{"", "AUTO", "UART", "I2C", "SPI", "MANCH", "SENT", "CAN", "1553", "ARINC", "USB", "FLEXR"}[hud.DecProto%12]
	if hud.DecProto == 1 { // Auto — label with whatever it found (all ten protocols)
		switch res.Proto {
		case "uart":
			name = "AUTO UART"
		case "i2c":
			name = "AUTO I2C"
		case "spi":
			name = "AUTO SPI"
		case "manchester":
			name = "AUTO MANCH"
		case "sent":
			name = "AUTO SENT"
		case "canfd":
			name = "AUTO CAN"
		case "mil1553":
			name = "AUTO 1553"
		case "arinc429":
			name = "AUTO ARINC"
		case "usbls":
			name = "AUTO USB"
		case "flexray":
			name = "AUTO FLEXR"
		}
	}
	// Decode lane: a dark band that sits ABOVE the bottom Vpp/freq readout row
	// (drawn later by drawHUD at H-9), so the two never overwrite each other.
	yLbl, yTxt, yBar := H-36, H-26, H-14 // label / byte hex / span bars, top→bottom
	fillRect(sf, 0, yLbl-2, W, 27, rgb(6, 10, 22))
	if !res.OK {
		DrawText(sf, 10, yLbl, name+": "+res.Error, colStale, 1)
		return
	}
	label := name
	if hud.DecProto == 1 { // Auto: show what it found, not just which protocol
		if res.Baud > 0 {
			if res.Proto == "uart" {
				label += fmt.Sprintf(" %d Bd", res.Baud)
			} else {
				label += " " + strings.TrimSuffix(fmtFreq(float64(res.Baud)), "Hz") + "b/s"
			}
		}
		if res.Src != "" {
			label += " " + res.Src
		}
	}
	unit := "bytes"
	switch res.Proto {
	case "mil1553", "arinc429", "manchester":
		unit = "words"
	case "sent":
		unit = "nibbles"
	}
	DrawText(sf, 10, yLbl, fmt.Sprintf("%s  %d %s", label, len(res.Bytes), unit), colDim, 1)
	// Map a sample index to screen x via the same window the trace uses.
	left := xc - float64(win)*posFrac
	sx := func(s float64) int { return int((s - left) * float64(W) / float64(win)) }
	for i, s := range res.Spans {
		x0, x1 := sx(float64(s.I0)), sx(float64(s.I1))
		if x1 < 0 || x0 >= W {
			continue // off-screen
		}
		col := decodeColor(s.Kind)
		if x1 < x0+2 {
			x1 = x0 + 2
		}
		for x := x0; x <= x1 && x < W; x++ { // span bar
			if x >= 0 {
				sf.SetPixel(x, yBar, col)
				sf.SetPixel(x, yBar+1, col)
			}
		}
		// Byte text, but only if it fits before the next span — otherwise a dense
		// stream turns the strip into unreadable mush; the coloured bars remain.
		nextX0 := W
		if i+1 < len(res.Spans) {
			nextX0 = sx(float64(res.Spans[i+1].I0))
		}
		if end := x0 + 1 + len(s.Text)*6; x0 >= 0 && end <= W && end <= nextX0 {
			DrawText(sf, x0+1, yTxt, s.Text, col, 1)
		}
	}
}

// colMask is the mask/zone overlay colour — dim steel blue, under the traces.
var colMask = rgb(90, 120, 160)

// drawZoneMask overlays the installed zones (edge-anchored rectangles) and the
// mask envelope boundaries, mapped with the SAME window mapping as drawTrace
// (left = xc - win*posFrac; column j of the mask reads raw sample left+j), so
// LCD == web == engine test point. The mask band only renders when the frame's
// window geometry matches the mask (zoom or a band change break the column
// alignment — the engine skips those frames too).
// TRLC-LINKS: REQ-SDS-021
func drawZoneMask(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = 0.5
	}
	left := xc - float64(win)*posFrac
	// mask envelope boundary lines
	if hud.MaskMode > 0 && hud.MaskWin == win && len(hud.MaskLo) == win && len(hud.MaskHi) == win {
		for x := 0; x < W; x++ {
			j := x * win / W
			sf.SetPixel(x, sampleToY(float64(hud.MaskLo[j])), colMask)
			sf.SetPixel(x, sampleToY(float64(hud.MaskHi[j])), colMask)
		}
	}
	// zone rectangles: dt (seconds from the edge) -> raw sample -> screen x
	if len(hud.Zones) > 0 && f.SampleS > 0 && f.EdgeX >= 0 {
		for _, z := range hud.Zones {
			x0 := int((f.EdgeX + z.DtLoS/f.SampleS - left) * float64(W) / float64(win))
			x1 := int((f.EdgeX + z.DtHiS/f.SampleS - left) * float64(W) / float64(win))
			if x1 < x0 {
				x0, x1 = x1, x0
			}
			if x1 < 0 || x0 >= W {
				continue
			}
			if x0 < 0 {
				x0 = 0
			}
			if x1 >= W {
				x1 = W - 1
			}
			y0 := sampleToY(float64(z.CodeHi)) // higher code = higher on screen
			y1 := sampleToY(float64(z.CodeLo))
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
	// over ~30 characters ran into the device URL (bench 2026-10-08).
	DrawTextRight(sf, 664, 14, line, col, 1)
}
