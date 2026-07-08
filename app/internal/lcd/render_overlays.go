package lcd

import (
	"fmt"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
)

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
func drawMath(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
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
	drawTrace(sf, m, win, xc, f.Interp, colMath, posFrac)
}

// drawRefs overlays the saved reference waveforms (REF A/B) as dim traces for
// comparison against the live trace (parity with the web REF A/B). Screen-space
// snapshots — they align while the timebase/scale are unchanged. A is purple,
// B is the info tint, so they read apart from the live channels.
func drawRefs(sf Surface, hud HUD, win int, xc float64, interp bool, posFrac float64) {
	cols := [2]uint16{colInfo, colDim} // distinct from the purple math trace
	for i := 0; i < 2; i++ {
		if !hud.RefShow[i] {
			continue
		}
		if r := hud.RefC1[i]; len(r) > 0 {
			drawTrace(sf, r, win, xc, interp, cols[i], posFrac)
		}
		if hud.TwoChan {
			if r := hud.RefC2[i]; len(r) > 0 {
				drawTrace(sf, r, win, xc, interp, cols[i], posFrac)
			}
		}
	}
}

// decodeColor maps a decode span kind to a display colour.
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
func drawDecode(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if hud.DecProto == 0 || f == nil {
		return
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
		res = decode.Autodetect(ch(0), ch(1), f.SampleS, format)
	case 2:
		res = decode.DecodeUART(ch(hud.DecChA), f.SampleS, decode.UARTCfg{Baud: hud.DecBaud, Format: format})
	case 3:
		res = decode.DecodeI2C(ch(hud.DecChA), ch(hud.DecChB), f.SampleS, decode.I2CCfg{Format: format})
	case 4:
		res = decode.DecodeSPI(ch(hud.DecChA), ch(hud.DecChB), f.SampleS, decode.SPICfg{CPOL: hud.DecCPOL, CPHA: hud.DecCPHA, MSB: true, Format: format})
	}
	name := []string{"", "AUTO", "UART", "I2C", "SPI"}[hud.DecProto%5]
	if hud.DecProto == 1 { // Auto — label with whatever it found
		switch res.Proto {
		case "uart":
			name = "AUTO UART"
		case "i2c":
			name = "AUTO I2C"
		case "spi":
			name = "AUTO SPI"
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
	DrawText(sf, 10, yLbl, fmt.Sprintf("%s  %d bytes", name, len(res.Bytes)), colDim, 1)
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
func drawZoneMask(sf Surface, f *engine.Frame, hud HUD, win int, xc, posFrac float64) {
	if posFrac <= 0 || posFrac > 1 {
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
	DrawText(sf, 330, 2, line, col, 1) // top-centre gap: right of "M <tdiv>", left of the trigger readout
}
