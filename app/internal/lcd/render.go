package lcd

import (
	"fmt"
	"strconv"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
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
	TwoChan          bool

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

// ---- value formatting (spec 07 §6.2): 3 sig figs, ASCII suffixes ----

func g3(x float64) string { return strconv.FormatFloat(x, 'g', 3, 64) }

func fmtVolt(v float64) string {
	x := v
	if x < 0 {
		x = -x
	}
	if x >= 1 {
		return g3(x) + "V"
	}
	return g3(x*1e3) + "mV"
}

// railed reports whether more than 0.5 % of a trace sits at the ADC rails —
// the same threshold the web frame uses, so both UIs flag clipping alike.
func railed(sig []uint8) bool {
	n := len(sig)
	if n == 0 {
		return false
	}
	c := 0
	for _, v := range sig {
		if v <= 1 || v >= 254 {
			c++
		}
	}
	return c*200 > n
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

// vppFreq computes the bottom-row readout (spec 07 §6.2): vpp at the fixed
// render scale 1/127 V per code; frequency from mid-level rising crossings.
func vppFreq(sig []uint8, sampleS float64) (vpp float64, freq float64, ok bool) {
	if len(sig) == 0 {
		return 0, 0, false
	}
	cmin, cmax := int(sig[0]), int(sig[0])
	for _, v := range sig {
		if int(v) < cmin {
			cmin = int(v)
		}
		if int(v) > cmax {
			cmax = int(v)
		}
	}
	vpp = float64(cmax-cmin) / 127.0
	lvl := uint8((cmin + cmax) / 2)
	first, last, n := -1, -1, 0
	for c := 1; c < len(sig); c++ {
		if sig[c-1] < lvl && sig[c] >= lvl {
			if first < 0 {
				first = c
			}
			last = c
			n++
		}
	}
	if n >= 2 && last > first && sampleS > 0 {
		period := float64(last-first) / float64(n-1) * sampleS
		return vpp, 1 / period, true
	}
	return vpp, 0, false
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
	if f != nil && len(f.C1) > 0 {
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

	drawMarkers(sf, hud)
	drawHUD(sf, f, hud)
	drawMenu(sf, hud)
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
		if railed(f.C1[:f.Valid]) {
			DrawText(sf, 4, 14, "CLIP", colTrig, 1)
		}
		if hud.TwoChan && railed(f.C2[:f.Valid]) {
			DrawText(sf, 96, 14, "CLIP", colTrig, 1)
		}
	}
	DrawText(sf, 200, 2, "M "+fmtTdiv(hud.TdivS), colInfo, 1)

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
	line := func(x int, sig []uint8, col uint16, name string) {
		vpp, freq, ok := vppFreq(sig, hud.SampleS)
		s := name + " Vpp " + fmtVolt(vpp)
		if ok {
			s += "  f " + fmtFreq(freq)
		}
		DrawText(sf, x, H-9, s, col, 1)
	}
	line(4, f.C1[:valid], colC1, "C1")
	if hud.TwoChan && len(f.C2) >= valid {
		line(410, f.C2[:valid], colC2, "C2")
	}
}
