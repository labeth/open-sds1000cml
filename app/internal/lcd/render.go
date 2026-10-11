// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"fmt"
	"math"
	"sync"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/wsview"
	"open-sds/app/internal/streamview"
)

// Colour palette (spec 07 §2.2). The col* variables are GENERATED into
// palette_gen.go from ../web/tokens.json — the single source of truth shared with
// the web UI — so the two surfaces cannot diverge (they did: the trigger colour
// was red on web but green here). Edit tokens.json + `go generate ./internal/web`.

// HUD is the UI-state snapshot the overlay renders alongside the frozen
// frame (spec 07 §6). It carries no capture state.
// TRLC-LINKS: REQ-SDS-021
type HUD struct {
	C1VdivV, C2VdivV float64
	// Zoom1/Zoom2 magnify a channel's codes about mid-scale: the 2 and 5 mV
	// detents are ×5/×2 display zooms of the 10 mV analog range (0 means 1).
	Zoom1, Zoom2   int
	Probe1, Probe2 float64 // probe attenuation (1/10/100); 0 treated as ×1
	Cpl1, Cpl2     int     // input coupling (analog.CplDC/CplAC/CplGND)
	Inv1, Inv2     bool    // display-level trace invert (SCPI Cn:INVS — the shadow there is the truth)
	TdivS          float64
	TrigSrc        int
	TrigRising     bool
	TrigLvlDiv     float64
	TrigLvlV       float64         // trigger level at the probe tip, volts
	Waiting        bool            // NORMAL, running, nothing published lately
	StreamOn       bool            // DECODE Auto in Stream mode
	Stream         streamview.View // its state and counters
	StreamList     bool            // the transcript fills the screen
	StreamPage     streamview.Page // the transcript lines to show
	Running, Norm  bool
	RecordS        float64 // the capture in progress fills this long a record (0 unknown)
	Single         bool
	Trigd          bool
	SampleS        float64 // per-sample seconds (frequency readout)
	TrigPosFrac    float64 // horizontal trigger position, 0.5 = centre
	OffC1V, OffC2V float64 // applied vertical offset volts (ground markers)
	ShowC1, ShowC2 bool    // per-channel display enable
	// ChanExplicit marks ShowC1/ShowC2 as the user's choice (the panel set
	// them), so both off draws neither channel. Without it, both off means
	// "unset" and both are drawn.
	ChanExplicit bool
	ShowMeas     bool // on-device MEASURE readout boxes
	// MeasItems are the chosen readouts, "1:pkpk" (channel and parameter);
	// nil shows every parameter of each shown channel.
	MeasItems        []string
	ViewMode         int       // 0=Y-T 1=X-Y 2=FFT 3=BODE 4=SPECTROGRAM
	MathMode         int       // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C2-C1, 4 = C1×C2
	FFTSrc           int       // the FFT source channel (drawn on top; the SPECTRUM marker's)
	AutosetBusy      bool      // autoset sweep running → show a cancelable banner
	AutosetMsg       string    // banner text while AutosetBusy
	Notice           string    // a short notice (PRINT, workspace tools), "" when none
	Remote           string    // a remote change's one-line note, "" when none
	Match            MatchView // serial trigger match editor (full screen while open)
	Busy             string    // a frame is being read/processed (loading indicator); "" idle
	BusyPct          float64   // its progress 0..100, <0 unknown
	BusyPhase        int       // spinner step, advanced by the caller while busy
	ViewPending      bool      // stopped: the shown frame is about to be replaced at the new view
	Roll             bool      // the live view is rolling (slow timebase, AUTO)
	Zoom             int       // horizontal magnification (1 = none)
	ZoomOff          float64   // zoom-window pan offset (fraction of the record)
	Persist          bool      // display persistence (afterglow)
	DecProto         int       // 0=off,1=Auto,2=UART,3=I2C,4=SPI,5=Manchester,6=SENT,7=CAN,8=MIL1553,9=ARINC429,10=USB,11=FlexRay
	DecBaud          int
	DecChA, DecChB   int // channel roles (0=C1,1=C2)
	DecCPOL, DecCPHA bool
	DecFormat        int           // byte display: 0=hex,1=ascii,2=both
	RefC1, RefC2     [2][]uint8    // saved reference waveforms (REF A/B), one code per column; nil if unset
	RefVdiv, RefOff  [2][2]float64 // [slot][channel] analog V/div and offset volts at the save
	RefShow          [2]bool
	TwoChan          bool

	// On-screen cursors: two X (time) and two Y (volts), positions as screen
	// fractions. CurType 0=X 1=Y; CurSel 0=A 1=B (the active one is highlighted).
	CurOn   bool
	CurType int
	CurSel  int
	CurX    [2]float64
	CurY    [2]float64

	// URL is the device's control-page address ("http://<ip>:8080", from
	// lcd.DeviceURL) shown on the top bar so a taken-over scope tells the user
	// where its web UI lives. Empty (no network) renders nothing.
	URL string

	// UI is the task-workspace UI (ADR-TASK-WORKSPACES): the layout, the
	// pictogram rail, the workspace's pane, a settings sheet or the byte
	// picker. The zero View draws no UI over a full-screen plot.
	UI wsview.View

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

const (
	traceTop = 8
	traceBot = H - 4 // 476
)

// sampleToY: higher code = higher on screen; clamp to panel (spec 07 §3.4).
// 25 codes/div render scale (spec 10 §7.1): 8 divisions = 200 codes centred on
// code 128, so the ADC's 256 codes span 10.24 div and the trace clips at the
// graticule edge beyond ±4 div. This is the full-screen plot's mapping.
// TRLC-LINKS: REQ-SDS-021
func sampleToY(v float64) int { return fullPlot.sy(v) }

// drawGraticule draws the plot's 10 x 8 divisions, centre axes brighter.
// TRLC-LINKS: REQ-SDS-021
func drawGraticule(sf Surface, p plotArea) {
	for c := 0; c <= 10; c++ {
		x := p.gridX(c)
		col := colGrid
		if c == 5 {
			col = colAxis
		}
		vline(sf, x, p.y0, H-1, col)
	}
	for r := 0; r <= 8; r++ {
		y := p.gridY(r)
		col := colGrid
		if r == 4 {
			col = colAxis
		}
		hline(sf, 0, p.w-1, y, col)
	}
}

// hline draws row y from x0 to x1 inclusive.
// TRLC-LINKS: REQ-SDS-021
func hline(sf Surface, x0, x1, y int, c uint16) {
	for x := x0; x <= x1; x++ {
		sf.SetPixel(x, y, c)
	}
}

// drawLine is a Bresenham segment (spec 07 §3.5).
// TRLC-LINKS: REQ-SDS-021
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

// drawTrace maps the record window onto the full-screen plot (spec 07 §3.5).
// TRLC-LINKS: REQ-SDS-021
func drawTrace(sf Surface, sig []uint8, win int, xc float64, interp bool, col uint16, posFrac float64, zoom ...int) {
	drawTraceID(sf, fullPlot, traceID{}, sig, win, xc, interp, col, posFrac, zoom...)
}

// traceID names the samples a trace draws (frame Seq plus how the channel's
// codes were derived), so a redraw of the same frame reuses the dense trace's
// column extremes. A zero seq never caches.
type traceID struct {
	seq     uint64
	ch, cpl int
	inv     bool
}

// drawTraceID maps the record window onto plot p: nearest sample when
// interp is false, linear interpolation of REAL samples when true — never
// sinc, never a segment across a skipped column.
// TRLC-LINKS: REQ-SDS-021
func drawTraceID(sf Surface, p plotArea, id traceID, sig []uint8, win int, xc float64, interp bool, col uint16, posFrac float64, zoom ...int) {
	z := 1.0
	if len(zoom) > 0 && zoom[0] > 1 {
		z = float64(zoom[0])
	}
	n := len(sig)
	if n == 0 {
		return
	}
	if win > n {
		win = n
	}
	if !(posFrac >= 0 && posFrac <= 1) {
		posFrac = 0.5
	}
	// Anchor at posFrac; do NOT clamp `left` — extend the nearest rail off the
	// record (repeat-nearest), identical to web window() so LCD == web. Keeps the
	// anchor exactly at posFrac even when the record has no mid crossing.
	left := xc - float64(win)*posFrac
	if !interp && float64(win) > 1.5*float64(p.w) {
		drawDenseTrace(sf, p, id, sig, left, win, col, z)
		return
	}
	prevX, prevY := -1, 0
	for x := 0; x < p.w; x++ {
		pos := left + float64(x)*float64(win)/float64(p.w)
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
			y = p.sy(128 + (v-128)*z)
		} else {
			i := int(pos)
			if pos < 0 {
				i = 0
			} else if i > n-1 {
				i = n - 1
			}
			y = p.sy(128 + (float64(sig[i])-128)*z)
		}
		if prevX >= 0 {
			drawLine(sf, prevX, prevY, x, y, col)
		} else {
			sf.SetPixel(x, y, col)
		}
		prevX, prevY = x, y
	}
}

// drawAcquiring notes that the capture for the current timebase is still
// filling its record.
// TRLC-LINKS: REQ-SDS-021
func drawAcquiring(sf Surface, p plotArea, hud HUD) {
	msg := "ACQUIRING"
	if hud.RecordS >= 1 {
		msg += fmt.Sprintf("  %s record", fmtTdiv(hud.RecordS))
	}
	DrawText(sf, (p.w-TextWidth(msg, 2))/2, (p.top+p.bot)/2-8, msg, colDim, 2)
}

// drawDenseTrace draws a record with more samples than columns as each
// column's min..max, joined to its neighbour. One point per column dropped
// everything between: a stopped 1 M-sample record at 100 us/div showed one
// sample in 1250, so a captured glitch was not drawn (bench 2026-10-07).
// Columns beyond the record repeat its nearest end, as drawTrace does.
// TRLC-LINKS: REQ-SDS-021
func drawDenseTrace(sf Surface, p plotArea, id traceID, sig []uint8, left float64, win int, col uint16, z float64) {
	cols := denseColumns(id, sig, left, win, p.w)
	y := func(v uint8) int { return p.sy(128 + (float64(v)-128)*z) }
	prevY := -1
	for x := 0; x < p.w; x++ {
		yTop, yBot := y(cols.mx[x]), y(cols.mn[x])
		if prevY >= 0 { // join: the span reaches the previous column's last value
			yTop, yBot = min(yTop, prevY), max(yBot, prevY)
		}
		vline(sf, x, yTop, yBot, col)
		prevY = y(cols.last[x])
	}
}

// denseCols are a dense trace's per-column extremes and last sample.
type denseCols struct {
	key          denseKey
	mn, mx, last [W]uint8
}

type denseKey struct {
	id     traceID
	n, win int
	cols   int
	left   float64
	ptr    *uint8    // the samples' buffer…
	probe  [16]uint8 // …and a spread of them, against a reused Seq
}

// denseCache keeps the column extremes of the last traces drawn (one slot per
// channel role): scanning a deep record is most of an LCD frame's cost, and
// the same frame is redrawn as the HUD changes around it.
var denseCache struct {
	sync.Mutex
	slot [4]denseCols
}

// TRLC-LINKS: REQ-SDS-021
func denseColumns(id traceID, sig []uint8, left float64, win, ncols int) *denseCols {
	ncols = min(max(ncols, 1), W)
	key := denseKey{id: id, n: len(sig), win: win, cols: ncols, left: left, ptr: &sig[0]}
	for i := range key.probe {
		key.probe[i] = sig[i*(len(sig)-1)/(len(key.probe)-1)]
	}
	var c *denseCols
	if id.seq != 0 {
		denseCache.Lock()
		defer denseCache.Unlock()
		c = &denseCache.slot[id.ch&3]
		if c.key == key {
			return c
		}
	} else {
		c = new(denseCols)
	}
	n := len(sig)
	step := float64(win) / float64(ncols)
	for x := 0; x < ncols; x++ {
		lo := int(math.Floor(left + float64(x)*step))
		hi := int(math.Floor(left+float64(x+1)*step)) - 1
		lo, hi = min(max(lo, 0), n-1), min(max(hi, 0), n-1)
		mn, mx := sig[lo], sig[lo]
		for _, v := range sig[lo : hi+1] {
			if v < mn {
				mn = v
			} else if v > mx {
				mx = v
			}
		}
		c.mn[x], c.mx[x], c.last[x] = mn, mx, sig[hi]
	}
	c.key = key
	if id.seq == 0 {
		c.key = denseKey{}
	}
	return c
}

// drawEnvelope fills each column min→max (spec 07 §4): every pixel lies
// between a real captured min and max, magnified about mid-scale by the
// channel's display zoom like drawTrace.
// TRLC-LINKS: REQ-SDS-021
func drawEnvelope(sf Surface, p plotArea, mn, mx []uint8, cols int, col uint16, zoom int) {
	z := float64(max(zoom, 1))
	for x := 0; x < p.w; x++ {
		c := x * cols / p.w
		if c >= cols || c >= len(mn) || c >= len(mx) {
			continue
		}
		yTop := p.sy(128 + (float64(mx[c])-128)*z)
		yBot := p.sy(128 + (float64(mn[c])-128)*z)
		if yTop > yBot {
			yTop, yBot = yBot, yTop
		}
		vline(sf, x, yTop, yBot, col)
	}
}

// frameValid clamps a frame's valid-sample count into range (shared by the
// alternate-view renderers below).
// TRLC-LINKS: REQ-SDS-021
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
// TRLC-LINKS: REQ-SDS-021
func coupledDisplay(sig []uint8, cpl int) []uint8 {
	if cpl != analog.CplDC {
		return analog.CoupleDisplay(sig, cpl)
	}
	return sig
}

// invertCodes mirrors a code slice about the display centre (127.5): the
// display-level INVS transform v' = 255 − v, matching the web draw path
// byte-for-byte. Returns a fresh slice — the input may be the shared fan-out
// frame. Applied to the RENDERED Y-T trace/envelope only: measurements,
// decode, math, X-Y/FFT and mask/zone tests keep the true captured polarity
// (see the Cn:INVS handler for the rationale).
// TRLC-LINKS: REQ-SDS-021
func invertCodes(sig []uint8) []uint8 {
	out := make([]uint8, len(sig))
	for i, v := range sig {
		out[i] = 255 - v
	}
	return out
}

// ---- value formatting (spec 07 §6.2): 3 sig figs, ASCII suffixes ----

// siUnit is one prefix row for siScale (high→low).
// TRLC-LINKS: REQ-SDS-021
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
// TRLC-LINKS: REQ-SDS-021
func fillRect(sf Surface, x, y, w, h int, c uint16) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			sf.SetPixel(xx, yy, c)
		}
	}
}

// strokeRect draws a 1 px rectangle outline.
// TRLC-LINKS: REQ-SDS-021
func strokeRect(sf Surface, x, y, w, h int, c uint16) {
	if w <= 0 || h <= 0 {
		return
	}
	hline(sf, x, x+w-1, y, c)
	hline(sf, x, x+w-1, y+h-1, c)
	vline(sf, x, y, y+h-1, c)
	vline(sf, x+w-1, y, y+h-1, c)
}

// Render draws one complete frame into the back buffer (spec 07 §3.2):
// fill → graticule → trace/envelope → liveness strip → readouts → the
// workspace UI (the pane, the rail, a sheet or the picker). Never blanks on
// a held frame; the strip goes red instead.
// TRLC-LINKS: REQ-SDS-021, REQ-SDS-211
func Render(sf Surface, f *engine.Frame, hud HUD, live bool, persist ...*MemSurface) {
	sf.Fill(colBG)
	ui := &hud.UI
	sheet := ui.Sheet != nil
	p := plotOf(ui.Layout)
	traces := !sheet && !ui.Layout.HideTraces
	if traces {
		drawGraticule(sf, p)
	}
	// Running, a frame captured at another timebase is not this view: drawn
	// under the new label it misread by the ratio, and at 10 s/div the next
	// frame takes minutes to fill (bench 2026-10-07). Show the graticule and
	// say a record is filling instead, as a scope clears on a timebase change.
	acquiring := hud.Running && f != nil && f.TdivS > 0 && hud.TdivS > 0 &&
		math.Abs(f.TdivS-hud.TdivS) > 1e-9*hud.TdivS
	if acquiring {
		f = nil
		if traces {
			defer drawAcquiring(sf, p, hud)
		}
	}

	// Persistence: when on (Y-T only, non-envelope), draw the traces onto a
	// separate layer that decays each frame, then composite it over the fresh
	// graticule — an afterglow that catches jitter/glitches. The layer is owned
	// by the caller (LCD loop); nil disables it (screenshot path, tests).
	var pb *MemSurface
	if len(persist) > 0 {
		pb = persist[0]
	}
	persisting := traces && pb != nil && hud.Persist && hud.ViewMode == 0 && f != nil && !f.IsEnv && len(f.C1) > 0
	traceTarget := sf
	if persisting {
		pb.FadeToBlack()
		traceTarget = pb
	}
	if traces {
		drawViews(sf, traceTarget, p, f, hud)
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
	hline(sf, 0, W-1, 0, strip)
	hline(sf, 0, W-1, 1, strip)

	// Trigger/ground markers, the MEASURE readouts and cursors are time-domain
	// concepts — only overlay them in Y-T, not on the X-Y/FFT plots.
	yt := traces && hud.ViewMode == 0 && !streamFullScreen(hud)
	if yt {
		drawMarkers(sf, p, hud)
	}
	drawHUD(sf, p, f, hud)
	if yt && hud.ShowMeas && !ui.Layout.HideMeas {
		drawMeasPanel(sf, p, f, hud)
	}
	if yt {
		drawCursors(sf, p, hud)
	}
	if !sheet {
		if hud.SRActive {
			drawSuperresHUD(sf, p, hud)
		}
		drawMaskHUD(sf, hud)
	}
	drawUI(sf, f, hud, p)
	switch {
	case hud.AutosetBusy:
		drawAutosetBanner(sf, p, hud.AutosetMsg)
	case hud.Notice != "":
		drawNotice(sf, p, hud.Notice)
	}
	if hud.Remote != "" {
		drawRemote(sf, p, hud.Remote)
	}
}

// drawViews draws the plot's content for the view: the transcript, the
// super-res review, X-Y, FFT, Bode, spectrogram, or the Y-T traces with
// their overlays.
// TRLC-LINKS: REQ-SDS-021
func drawViews(sf, traceTarget Surface, p plotArea, f *engine.Frame, hud HUD) {
	sc1, sc2 := hud.shownChans()
	// X-Y / FFT only make sense on per-sample (native/decimated) frames — an
	// envelope/roll frame has no paired samples or usable spectrum, so fall
	// through to the Y-T envelope rendering (matches the web, which gates these).
	srReview := hud.SRFocus == 3 && len(hud.SRMean) > 0
	switch {
	case streamFullScreen(hud):
		drawStream(sf, p, hud)
	case srReview:
		// Review the super-resolved stack in the SELECTED view, so FFT and X-Y
		// operate on the crunched (extra-bit, K× fine-grid) trace — not just Y-T.
		// Bode (3) / Spectrogram (4) are sweep/time-evolving views with no meaning
		// for a frozen stack, so they fall back to the Y-T super-res trace.
		switch hud.ViewMode {
		case 1:
			drawSuperresXY(sf, p, hud)
		case 2:
			drawSuperresFFT(sf, p, hud)
		default:
			drawSuperresTrace(traceTarget, p, hud)
		}
	case hud.ViewMode == 1 && f != nil && !f.IsEnv && len(f.C1) > 0:
		drawXY(sf, p, f, hud)
	case hud.ViewMode == 2 && f != nil && !f.IsEnv && len(f.C1) > 0:
		drawFFT(sf, p, f, hud)
	case hud.ViewMode == 3:
		drawBode(sf, p, hud)
	case hud.ViewMode == 4:
		drawSpectrogramMark(sf, p, hud.Spect, hud.UI.Layout.HideViewCaptions, hud.UI.Spec)
	case f != nil && len(f.C1) > 0:
		valid := frameValid(f)
		if f.IsEnv && f.EnvCols > 0 {
			if hud.TwoChan && sc2 {
				mn2, mx2 := f.EnvMin2, f.EnvMax2
				if hud.Inv2 { // INVS mirrors the band: min'=inv(max), max'=inv(min)
					mn2, mx2 = invertCodes(mx2), invertCodes(mn2)
				}
				drawEnvelope(sf, p, mn2, mx2, f.EnvCols, colC2, hud.Zoom2)
			}
			if sc1 {
				mn1, mx1 := f.EnvMin, f.EnvMax
				if hud.Inv1 {
					mn1, mx1 = invertCodes(mx1), invertCodes(mn1)
				}
				drawEnvelope(sf, p, mn1, mx1, f.EnvCols, colC1, hud.Zoom1)
			}
			break
		}
		win, xc, posFrac := traceWindow(f, hud, valid)
		c1 := f.C1[:valid]
		if hud.Cpl1 != analog.CplDC { // software coupling: AC removes DC, GND grounds
			c1 = analog.CoupleDisplay(c1, hud.Cpl1)
		}
		if hud.Inv1 { // display-level INVS: mirror the rendered trace only
			c1 = invertCodes(c1)
		}
		drawZoneMask(traceTarget, p, f, hud, win, xc, posFrac)
		if hud.TwoChan && sc2 && len(f.C2) >= valid {
			c2 := f.C2[:valid]
			if hud.Cpl2 != analog.CplDC {
				c2 = analog.CoupleDisplay(c2, hud.Cpl2)
			}
			if hud.Inv2 {
				c2 = invertCodes(c2)
			}
			drawTraceID(traceTarget, p, traceID{f.Seq, 2, hud.Cpl2, hud.Inv2}, c2, win, xc, f.Interp, colC2, posFrac, hud.Zoom2)
		}
		if sc1 {
			drawTraceID(traceTarget, p, traceID{f.Seq, 1, hud.Cpl1, hud.Inv1}, c1, win, xc, f.Interp, colC1, posFrac, hud.Zoom1)
		}
		if hud.MathMode != 0 {
			drawMath(traceTarget, p, f, hud, win, xc, posFrac)
		}
		drawRefs(traceTarget, p, hud)
		if hud.DecProto != 0 {
			drawDecode(sf, p, f, hud, win, xc, posFrac)
		}
	}
	if hud.StreamOn && !streamFullScreen(hud) && hud.Stream.State != streamview.Idle {
		drawStreamStrip(sf, p, hud)
	}
}

// traceWindow is the sample window the Y-T plot shows: win samples with the
// trigger crossing xc at posFrac of the screen; zoomed, win/zoom samples
// centred on the panned crossing.
// TRLC-LINKS: REQ-SDS-021
func traceWindow(f *engine.Frame, hud HUD, valid int) (win int, xc, posFrac float64) {
	win = f.WinCols
	if win <= 0 || win > valid {
		win = valid
	}
	xc = f.EdgeX
	if xc < 0 {
		// Match web window(): an uncentred frame (EdgeX=-1) is always a
		// flat/DC capture under the engine's publish policy — centre on
		// the record middle. NEVER fabricate an edge from a marginal/noise
		// crossing (that would jitter a flat line and diverge from web).
		xc = float64(valid) / 2
	}
	// Horizontal ZOOM: show win/zoom samples magnified across the screen,
	// panned by ZoomOff across the record (centred, so posFrac=0.5).
	posFrac = hud.TrigPosFrac
	if hud.Zoom > 1 {
		win /= hud.Zoom
		if win < 8 {
			win = 8
		}
		xc += hud.ZoomOff * float64(valid)
		posFrac = 0.5
	}
	return win, xc, posFrac
}

// TRLC-LINKS: REQ-SDS-021
func absI(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// shownChans is the per-channel display enable. Both off means "unset" (show
// both) unless the panel set the flags: then the user hid both channels and
// neither is drawn (bug hunt #2: CH1+CH2 off brought both traces back).
// TRLC-LINKS: REQ-SDS-021
func (h HUD) shownChans() (bool, bool) {
	if !h.ShowC1 && !h.ShowC2 && !h.ChanExplicit {
		return true, true
	}
	return h.ShowC1, h.ShowC2
}
