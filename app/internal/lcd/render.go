package lcd

import (
	"fmt"
	"strconv"

	"open-sds/app/internal/engine"
)

// Colour palette (spec 07 §2.2).
var (
	colBG    = rgb(0, 0, 16)
	colGrid  = rgb(40, 40, 60)
	colAxis  = rgb(80, 80, 110)
	colC1    = rgb(255, 236, 0)
	colC2    = rgb(0, 220, 255)
	colOK    = rgb(0, 200, 0)
	colStale = rgb(220, 40, 40)
	colInfo  = rgb(200, 200, 200)
	colTrig  = rgb(64, 255, 64)
)

// HUD is the UI-state snapshot the overlay renders alongside the frozen
// frame (spec 07 §6). It carries no capture state.
type HUD struct {
	C1VdivV, C2VdivV float64
	TdivS            float64
	TrigSrc          int
	TrigRising       bool
	TrigLvlDiv       float64
	Running, Norm    bool
	Trigd            bool
	SampleS          float64 // per-sample seconds (frequency readout)
	TwoChan          bool
}

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
func drawTrace(sf Surface, sig []uint8, win int, xc float64, interp bool, col uint16) {
	n := len(sig)
	if n == 0 {
		return
	}
	if win > n {
		win = n
	}
	// Clamp the window into the record so the screen fills with real data
	// (no end gaps); the edge stays centred except near the record ends.
	left := xc - float64(win)/2
	if left < 0 {
		left = 0
	}
	if left > float64(n-win) {
		left = float64(n - win)
	}
	prevX, prevY := -1, 0
	for x := 0; x < W; x++ {
		pos := left + float64(x)*float64(win)/float64(W)
		var y int
		if interp {
			if pos < 0 || pos > float64(n-1) {
				prevX = -1
				continue
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
			if pos < 0 || i < 0 || i >= n {
				prevX = -1
				continue
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

	if f != nil && len(f.C1) > 0 {
		valid := f.Valid
		if valid < 1 {
			valid = 1
		}
		if valid > len(f.C1) {
			valid = len(f.C1)
		}
		if f.IsEnv && f.EnvCols > 0 {
			if hud.TwoChan {
				drawEnvelope(sf, f.EnvMin2, f.EnvMax2, f.EnvCols, colC2)
			}
			drawEnvelope(sf, f.EnvMin, f.EnvMax, f.EnvCols, colC1)
		} else {
			win := f.WinCols
			if win <= 0 || win > valid {
				win = valid
			}
			c1 := f.C1[:valid]
			xc := f.EdgeX
			if xc < 0 {
				// Renderer-side centring fallback; NEVER fabricate an edge —
				// a flat rail centres on the record middle.
				if x := centerCrossR(c1); x >= 0 {
					xc = x
				} else {
					xc = float64(valid) / 2
				}
			}
			if hud.TwoChan && len(f.C2) >= valid {
				drawTrace(sf, f.C2[:valid], win, xc, f.Interp, colC2)
			}
			drawTrace(sf, c1, win, xc, f.Interp, colC1)
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

	drawHUD(sf, f, hud)
}

// centerCrossR is the renderer's rising mid-level fallback (spec 07 §3.3).
func centerCrossR(sig []uint8) float64 {
	n := len(sig)
	if n < 2 {
		return -1
	}
	mn, mx := int(sig[0]), int(sig[0])
	for _, v := range sig {
		if int(v) < mn {
			mn = int(v)
		}
		if int(v) > mx {
			mx = int(v)
		}
	}
	lvl := uint8((mn + mx) / 2)
	best, bestD := -1, n
	for c := 1; c < n; c++ {
		if sig[c-1] < lvl && sig[c] >= lvl {
			d := c - n/2
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = c, d
			}
		}
	}
	if best < 0 {
		return -1
	}
	a, b := float64(sig[best-1]), float64(sig[best])
	bf := 0.0
	if b != a {
		bf = (float64(lvl) - a) / (b - a)
		if bf < 0 || bf >= 1 {
			bf = 0
		}
	}
	return float64(best-1) + bf
}

func drawHUD(sf Surface, f *engine.Frame, hud HUD) {
	DrawText(sf, 4, 2, "C1 "+fmtVolt(hud.C1VdivV), colC1, 1)
	if hud.TwoChan {
		DrawText(sf, 96, 2, "C2 "+fmtVolt(hud.C2VdivV), colC2, 1)
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
