package lcd

import (
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
	ShowMeas         bool    // on-device MEASURE panel overlay
	ViewMode         int     // 0=Y-T 1=X-Y 2=FFT 3=BODE 4=SPECTROGRAM
	MathMode         int     // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2
	AutosetBusy      bool    // autoset sweep running → show a cancelable banner
	AutosetMsg       string  // banner text while AutosetBusy
	Zoom             int     // horizontal magnification (1 = none)
	ZoomOff          float64 // zoom-window pan offset (fraction of the record)
	Persist          bool    // display persistence (afterglow)
	DecProto         int     // protocol decode: 0=off,1=Auto,2=UART,3=I2C,4=SPI
	DecBaud          int
	DecChA, DecChB   int // channel roles (0=C1,1=C2)
	DecCPOL, DecCPHA bool
	DecFormat        int        // byte display: 0=hex,1=ascii,2=both
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

	// Super-res stack-and-crunch overlay (reference-locked). SRActive shows the
	// status HUD; SRFocus 3 (review) replaces the live trace with the stacked mean;
	// SRFocus 1/2 overlay the gate markers (active edge highlighted) for editing.
	SRActive  bool
	SRFocus   int // 0=watch, 1=gate-start, 2=gate-end, 3=review
	SRStatus  string
	SRBits    float64
	SRMean    []float32 // align-channel review trace on the L·K fine grid; -1 = gap (nil unless review)
	SRMean2   []float32 // the OTHER channel's review trace (stacked X-Y / dual FFT); -1 = gap
	SRAlign   int       // which physical channel SRMean is (0=C1,1=C2); SRMean2 is the other
	SRSampleS float64   // the stack's raw per-sample seconds (fine dt = SRSampleS/SRk)
	SRk       int
	SRGateLo  int
	SRGateHi  int
	SRN       int
	SRWinLo   int // the selected span the review renders — the frozen view, unchanged
	SRWinHi   int
	SRPeriod  int // >0: the stack is one period; tile it across the span

	// Zone trigger + mask testing overlay (engine-side capture qualification;
	// parity with the web card). Zones render edge-anchored; the mask renders
	// as its envelope boundary lines when the window geometry matches.
	ZoneMode       int
	Zones          []engine.Zone
	MaskMode       int // 0 off, 1 test, 2 stop-on-fail
	MaskLo, MaskHi []uint8
	MaskWin        int
	MaskPass       int64
	MaskFail       int64
	MaskSkip       int64
	MaskStopped    bool
	MaskMsg        string // panel build/status line ("" when idle)
	ZoneSkip       int64  // zone-armed publishes that were untestable (env/roll)

	// FRA / Bode plot (bode.go): the accumulated response curve, rendered when
	// ViewMode == 3. Parallel arrays sorted ascending by frequency.
	BodeFreq  []float64
	BodeGain  []float64
	BodePhase []float64

	// Spectrogram ("FFT over time", spectrogram.go): the scrolling waterfall
	// image, accumulated by the LCD loop and blitted when ViewMode == 4.
	Spect *Spectrogram
}

// MenuItem is one softkey slot label + value for the LCD menu overlay.
type MenuItem struct{ Label, Value string }

const (
	traceTop = 8
	traceBot = H - 4 // 476
)

// sampleToY: higher code = higher on screen; clamp to panel (spec 07 §3.4).
// 25 codes/div render scale (spec 10 §7.1): 8 divisions = 200 codes centred on
// code 128, so the ADC's 256 codes span 10.24 div and the trace clips at the
// graticule edge beyond ±4 div.
func sampleToY(v float64) int {
	y := traceBot - int(((v-128)/200+0.5)*float64(traceBot-traceTop)+0.5)
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

// ---- value formatting (spec 07 §6.2): 3 sig figs, ASCII suffixes ----

// siUnit is one prefix row for siScale (high→low).
type siUnit struct {
	scale  float64
	suffix string
}

var (
	voltUnits = []siUnit{{1, "V"}, {1e-3, "mV"}}
	freqUnits = []siUnit{{1e6, "MHz"}, {1e3, "kHz"}, {1, "Hz"}}
	timeUnits = []siUnit{{1, "s"}, {1e-3, "ms"}, {1e-6, "us"}, {1e-9, "ns"}}
)

// fillRect paints a solid rectangle (clipped to the surface).
func fillRect(sf Surface, x, y, w, h int, c uint16) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			sf.SetPixel(xx, yy, c)
		}
	}
}

// Render draws one complete frame into the back buffer (spec 07 §3.2):
// fill → graticule → trace/envelope → liveness strip → readouts. Never
// blanks on a held frame; the strip goes red instead.
func Render(sf Surface, f *engine.Frame, hud HUD, live bool, persist ...*MemSurface) {
	sf.Fill(colBG)
	drawGraticule(sf)

	// Persistence: when on (Y-T only, non-envelope), draw the traces onto a
	// separate layer that decays each frame, then composite it over the fresh
	// graticule — an afterglow that catches jitter/glitches. The layer is owned
	// by the caller (LCD loop); nil disables it (screenshot path, tests).
	var pb *MemSurface
	if len(persist) > 0 {
		pb = persist[0]
	}
	persisting := pb != nil && hud.Persist && hud.ViewMode == 0 && f != nil && !f.IsEnv && len(f.C1) > 0
	traceTarget := sf
	if persisting {
		pb.FadeToBlack()
		traceTarget = pb
	}

	// Both-off ⇒ show both (default / callers that don't set the flags).
	sc1, sc2 := hud.ShowC1, hud.ShowC2
	if !sc1 && !sc2 {
		sc1, sc2 = true, true
	}
	// X-Y / FFT only make sense on per-sample (native/decimated) frames — an
	// envelope/roll frame has no paired samples or usable spectrum, so fall
	// through to the Y-T envelope rendering (matches the web, which gates these).
	srReview := hud.SRFocus == 3 && len(hud.SRMean) > 0
	if srReview {
		// Review the super-resolved stack in the SELECTED view, so FFT and X-Y
		// operate on the crunched (extra-bit, K× fine-grid) trace — not just Y-T.
		// Bode (3) / Spectrogram (4) are sweep/time-evolving views with no meaning
		// for a frozen stack, so they fall back to the Y-T super-res trace.
		switch hud.ViewMode {
		case 1:
			drawSuperresXY(sf, hud)
		case 2:
			drawSuperresFFT(sf, hud)
		default:
			drawSuperresTrace(traceTarget, hud)
		}
	} else if hud.ViewMode == 1 && f != nil && !f.IsEnv && len(f.C1) > 0 {
		drawXY(sf, f, hud)
	} else if hud.ViewMode == 2 && f != nil && !f.IsEnv && len(f.C1) > 0 {
		drawFFT(sf, f, hud)
	} else if hud.ViewMode == 3 {
		drawBode(sf, hud)
	} else if hud.ViewMode == 4 {
		drawSpectrogram(sf, hud.Spect)
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
			// Horizontal ZOOM: show win/zoom samples magnified across the screen,
			// panned by ZoomOff across the record (centred, so posFrac=0.5).
			posFrac := hud.TrigPosFrac
			if hud.Zoom > 1 {
				win /= hud.Zoom
				if win < 8 {
					win = 8
				}
				xc += hud.ZoomOff * float64(valid)
				posFrac = 0.5
			}
			drawZoneMask(traceTarget, f, hud, win, xc, posFrac)
			if hud.TwoChan && sc2 && len(f.C2) >= valid {
				c2 := f.C2[:valid]
				if hud.Cpl2 != analog.CplDC {
					c2 = analog.CoupleDisplay(c2, hud.Cpl2)
				}
				drawTrace(traceTarget, c2, win, xc, f.Interp, colC2, posFrac)
			}
			if sc1 {
				drawTrace(traceTarget, c1, win, xc, f.Interp, colC1, posFrac)
			}
			if hud.MathMode != 0 {
				drawMath(traceTarget, f, hud, win, xc, posFrac)
			}
			drawRefs(traceTarget, hud, win, xc, f.Interp, posFrac)
			if hud.DecProto != 0 {
				drawDecode(sf, f, hud, win, xc, posFrac)
			}
		}
	}
	if persisting { // composite the decayed trace layer over the graticule
		if ms, ok := sf.(*MemSurface); ok {
			ms.BlitBright(pb)
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
	if hud.SRActive {
		drawSuperresHUD(sf, hud)
	}
	drawMaskHUD(sf, hud)
	if hud.AutosetBusy {
		drawAutosetBanner(sf, hud.AutosetMsg)
	}
}

func absI(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
