package panel

import (
	"fmt"
	"strings"

	"open-sds/app/internal/engine"
)

// On-screen menu pages (spec 08 §6 menu buttons). The five softkeys F1..F5 line
// up physically with the five menu slots down the right edge of the LCD.
const (
	pgNone = iota
	pgTrig
	pgAcq
	pgDisp
	pgHoriz
	pgChan   // per-channel coupling + probe (reached by re-pressing DISPLAY)
	pgCursor // on-screen cursors (reached by re-pressing HORIZONTAL / CURSORS key)
	pgRef    // reference waveforms REF A/B (reached by re-pressing ACQUIRE)
	pgMain   // MAIN menu (MENU key): navigates to every sub-page
)

// MenuItem is one softkey slot: a label and its current value.
type MenuItem struct{ Label, Value string }

// MenuView is the render snapshot of the on-screen menu (read by the LCD
// renderer on a different goroutine; taken under the controller mutex).
type MenuView struct {
	Open           bool
	Title          string
	Items          []MenuItem // up to 5, aligned F1(top)..F5(bottom)
	Sel            int        // highlighted slot
	ShowC1, ShowC2 bool       // per-channel display enable (DISPLAY menu / CH keys)
	ShowMeas       bool       // on-device MEASURE panel toggle (DISPLAY menu)
	ViewMode       int        // 0 = Y-T, 1 = X-Y, 2 = FFT (DISPLAY menu "View")
	MathMode       int        // 0 = off, 1 = C1+C2, 2 = C1-C2, 3 = C1×C2
	AutosetBusy    bool       // an autoset sweep is running (LCD shows a hint)
	AutosetMsg     string     // banner text while AutosetBusy

	CurOn   bool       // cursors visible
	CurType int        // 0 = X (time), 1 = Y (volts)
	CurSel  int        // active cursor (0 = A, 1 = B)
	CurX    [2]float64 // X cursor screen fractions
	CurY    [2]float64 // Y cursor screen fractions
}

// Menu / softkey / channel button codes (spec 08 §6.1/§6.2/§6.4/§6.5).
var (
	btnF1 = bcode(1, 5)
	btnF2 = bcode(1, 13)
	btnF3 = bcode(2, 5)
	btnF4 = bcode(2, 13)
	btnF5 = bcode(3, 5)

	btnTrigMenu  = bcode(0, 10)
	btnAcquire   = bcode(0, 11)
	btnDisplay   = bcode(1, 3)
	btnHorizMenu = bcode(1, 12)
	btnMenuOnOff = bcode(0, 13)
	btnMeasure   = bcode(1, 4)  // spec 08 §7 — was unwired (dead button)
	btnCursors   = bcode(0, 12) // spec 08 §7 — was unwired (dead button)
	btnCh1       = bcode(2, 4)
	btnCh2       = bcode(3, 4)
)

var softkeys = []int{btnF1, btnF2, btnF3, btnF4, btnF5}

// menuButton handles a menu-related button; returns true if it consumed it.
func (c *Controller) menuButton(code int) bool {
	switch code {
	case btnTrigMenu:
		c.openMenu(pgTrig)
		return true
	case btnAcquire:
		// ACQUIRE cycles between the acquire page and the reference (REF A/B) page.
		c.mu.Lock()
		next := pgAcq
		if c.menuPage == pgAcq {
			next = pgRef
		}
		c.mu.Unlock()
		c.openMenu(next)
		return true
	case btnDisplay:
		// DISPLAY cycles between the channel-display page and the per-channel
		// coupling/probe page, so both are reachable from one physical button.
		c.mu.Lock()
		next := pgDisp
		if c.menuPage == pgDisp {
			next = pgChan
		}
		c.mu.Unlock()
		c.openMenu(next)
		return true
	case btnHorizMenu:
		// HORIZONTAL cycles between the timebase page and the cursor page.
		c.mu.Lock()
		next := pgHoriz
		if c.menuPage == pgHoriz {
			next = pgCursor
		}
		c.mu.Unlock()
		c.openMenu(next)
		return true
	case btnMenuOnOff:
		// MENU opens the MAIN menu (a navigable list of every sub-page); pressing
		// it again closes the menu overlay.
		c.mu.Lock()
		closed := c.menuPage == pgNone
		c.mu.Unlock()
		if closed {
			c.openMenu(pgMain)
		} else {
			c.mu.Lock()
			c.menuPage = pgNone
			c.mu.Unlock()
			c.pushLEDs()
		}
		return true
	case btnMeasure:
		// Dedicated MEASURE key: toggle the on-screen measurement panel.
		c.mu.Lock()
		c.showMeas = !c.showMeas
		c.mu.Unlock()
		c.pushLEDs()
		return true
	case btnCursors:
		c.openMenu(pgCursor) // dedicated CURSORS key -> straight to the cursor page
		return true
	case btnCh1:
		c.mu.Lock()
		c.chDisp[0] = !c.chDisp[0]
		c.mu.Unlock()
		return true
	case btnCh2:
		c.mu.Lock()
		c.chDisp[1] = !c.chDisp[1]
		c.mu.Unlock()
		return true
	}
	for i, sk := range softkeys {
		if code == sk {
			c.mu.Lock()
			open := c.menuPage != pgNone
			c.mu.Unlock()
			if open {
				c.menuCycle(i, +1)
				return true
			}
			return false // softkey with no menu open: nothing to do
		}
	}
	return false
}

func (c *Controller) openMenu(pg int) {
	c.mu.Lock()
	c.menuPage, c.menuSel = pg, 0
	c.mu.Unlock()
	c.pushLEDs()
}

// menuCycle changes the item in slot `slot` by `dir` (F-key press → +1; the
// ADJUST knob → ±1). It highlights the slot so ADJUST then tracks it.
// pageSlots is how many softkey slots a page actually populates — presses on
// the rest are inert (no highlight moves onto a blank slot).
func pageSlots(pg int) int {
	switch pg {
	case pgHoriz:
		return 2
	case pgAcq:
		return 4
	case pgChan:
		return 4
	default: // pgTrig, pgDisp, pgCursor, pgRef
		return 5
	}
}

func (c *Controller) menuCycle(slot, dir int) {
	c.mu.Lock()
	pg := c.menuPage
	if slot >= pageSlots(pg) { // empty slot: don't move the highlight or act
		c.mu.Unlock()
		return
	}
	c.menuSel = slot
	c.mu.Unlock()
	st := c.eng.Snapshot()
	switch pg {
	case pgMain: // MAIN menu: each softkey navigates to a sub-page
		switch slot {
		case 0:
			c.openMenu(pgTrig)
		case 1:
			c.openMenu(pgAcq)
		case 2:
			c.openMenu(pgDisp)
		case 3:
			c.openMenu(pgHoriz)
		case 4:
			c.openMenu(pgCursor)
		}
		return
	case pgTrig:
		switch slot {
		case 0:
			c.eng.SetNorm(!st.Norm)
			c.norm = !st.Norm
		case 1:
			c.eng.SetTrigSlope(!st.TrigRising)
		case 2:
			c.eng.SetTrigSource(1 - st.TrigSource)
		case 3:
			c.eng.SetTrigType(mod4(st.TrigType + dir))
		case 4:
			c.eng.SetHoldoff(nextHoldoff(st.HoldoffS, dir))
		}
	case pgAcq:
		switch slot {
		case 0:
			c.eng.SetAcqMode(mod4(st.AcqMode + dir))
		case 1:
			c.menuCount(st, dir)
		case 2:
			c.eng.SetETS(!st.ETS)
		case 3: // memory depth (fps <-> data knob)
			c.eng.SetMemDepth(nextOpt([]int{2000, 6000, 14000, 20480}, st.MemDepth, dir))
		}
	case pgDisp:
		switch slot {
		case 0:
			c.mu.Lock()
			c.chDisp[0] = !c.chDisp[0]
			c.mu.Unlock()
		case 1:
			c.mu.Lock()
			c.chDisp[1] = !c.chDisp[1]
			c.mu.Unlock()
		case 2:
			c.mu.Lock()
			c.showMeas = !c.showMeas
			c.mu.Unlock()
		case 3: // View: Y-T → X-Y → FFT
			c.mu.Lock()
			c.viewMode = mod3(c.viewMode + dir)
			c.mu.Unlock()
		case 4: // Math: off -> C1+C2 -> C1-C2 -> C2-C1 -> C1xC2
			c.mu.Lock()
			c.mathMode = mod5(c.mathMode + dir)
			c.mu.Unlock()
		}
	case pgHoriz:
		switch slot {
		case 0: // Time/div: step the ladder
			c.resync()
			c.tdivIdx = clampInt(c.tdivIdx+dir, 0, len(c.tdivs)-1)
			c.eng.SetTdiv(c.tdivs[c.tdivIdx])
		case 1: // Trigger position: step the horizontal fraction
			c.eng.SetTrigPosFrac(clampF(c.trigPos()+float64(dir)*0.05, 0.02, 1))
		}
	case pgChan:
		if c.fe != nil {
			switch slot {
			case 0: // CH1 coupling: cycle DC → AC → GND
				_ = c.fe.SetCoupling(0, mod3(c.fe.Coupling(0)+dir))
			case 1: // CH2 coupling
				_ = c.fe.SetCoupling(1, mod3(c.fe.Coupling(1)+dir))
			case 2: // CH1 probe: cycle ×1 → ×10 → ×100
				c.fe.SetProbe(0, nextProbe(c.fe.ProbeFactor(0), dir))
			case 3: // CH2 probe
				c.fe.SetProbe(1, nextProbe(c.fe.ProbeFactor(1), dir))
			}
		}
	case pgRef:
		switch slot {
		case 0: // Save A: snapshot the current frame
			c.captureRef(0)
		case 1: // Save B
			c.captureRef(1)
		case 2: // Show/hide A
			c.mu.Lock()
			if c.refs[0].has {
				c.refs[0].show = !c.refs[0].show
			}
			c.mu.Unlock()
		case 3: // Show/hide B
			c.mu.Lock()
			if c.refs[1].has {
				c.refs[1].show = !c.refs[1].show
			}
			c.mu.Unlock()
		case 4: // Clear both
			c.mu.Lock()
			c.refs = [2]refWave{}
			c.mu.Unlock()
		}
	case pgCursor:
		switch slot {
		case 0:
			c.mu.Lock()
			c.curOn = !c.curOn
			c.mu.Unlock()
		case 1:
			c.mu.Lock()
			c.curType = 1 - c.curType
			c.mu.Unlock()
		case 2:
			c.mu.Lock()
			c.curSel = 1 - c.curSel
			c.mu.Unlock()
		case 3: // F4 nudges the active cursor − (ADJUST moves it continuously)
			c.moveCursor(-1)
		case 4: // F5 nudges +
			c.moveCursor(+1)
		}
	}
	c.pushLEDs()
}

func (c *Controller) menuCount(st engine.Stats, dir int) {
	switch st.AcqMode {
	case 1: // Average
		c.eng.SetAvgCount(nextOpt([]int{4, 16, 32, 64, 128, 256}, st.AvgCount, dir))
	case 2: // ERes
		c.eng.SetEresLen(nextOpt([]int{1, 3, 7, 15, 31, 63}, st.EresLen, dir))
	}
}

// menuAdjust is the ADJUST knob acting on the highlighted item (spec 08 §6.3).
// On the cursor page the knob moves the active cursor rather than cycling a
// softkey, so positioning feels continuous.
func (c *Controller) menuAdjust(dir int) {
	c.mu.Lock()
	pg, sel, curOn := c.menuPage, c.menuSel, c.curOn
	c.mu.Unlock()
	if pg == pgCursor && curOn {
		c.moveCursor(dir)
		return
	}
	if pg != pgNone {
		c.menuCycle(sel, dir)
	}
}

// moveCursor nudges the selected cursor of the active type by ~1 % of screen.
func (c *Controller) moveCursor(dir int) {
	c.mu.Lock()
	step := 0.01 * float64(dir)
	if c.curType == 0 {
		c.curX[c.curSel] = clampF(c.curX[c.curSel]+step, 0, 1)
	} else {
		c.curY[c.curSel] = clampF(c.curY[c.curSel]+step, 0, 1)
	}
	c.mu.Unlock()
	c.pushLEDs()
}

func (c *Controller) trigPos() float64 {
	f := c.eng.Snapshot().TrigPosFrac
	if f <= 0 {
		f = 0.5
	}
	return f
}

// MenuView is the render snapshot; safe to call from the render goroutine.
func (c *Controller) MenuView() MenuView {
	c.mu.Lock()
	pg, sel, c1, c2, meas := c.menuPage, c.menuSel, c.chDisp[0], c.chDisp[1], c.showMeas
	view, mth, busy, amsg := c.viewMode, c.mathMode, c.autosetBusy, c.autosetMsg
	rA, rAs, rB, rBs := c.refs[0].has, c.refs[0].show, c.refs[1].has, c.refs[1].show
	curOn, curType, curSel, curX, curY := c.curOn, c.curType, c.curSel, c.curX, c.curY
	c.mu.Unlock()
	v := MenuView{Open: pg != pgNone, Sel: sel, ShowC1: c1, ShowC2: c2, ShowMeas: meas,
		ViewMode: view, MathMode: mth, AutosetBusy: busy, AutosetMsg: amsg,
		CurOn: curOn, CurType: curType, CurSel: curSel, CurX: curX, CurY: curY}
	if pg == pgNone {
		return v
	}
	st := c.eng.Snapshot()
	onoff := func(b bool) string {
		if b {
			return "On"
		}
		return "Off"
	}
	switch pg {
	case pgMain:
		v.Title = "MENU"
		v.Items = []MenuItem{
			{"Trigger", ">"},
			{"Acquire", ">"},
			{"Display", ">"},
			{"Horizontal", ">"},
			{"Cursors", ">"},
		}
	case pgTrig:
		slope := "Rise" // LCD font is ASCII — no arrow glyphs
		if !st.TrigRising {
			slope = "Fall"
		}
		src := "CH1"
		if st.TrigSource == 1 {
			src = "CH2"
		}
		types := []string{"Edge", "Pulse", "Slope", "Video"}
		ho := "Off"
		if st.HoldoffS > 0 {
			ho = fmtEng(st.HoldoffS, "s")
		}
		v.Title = "TRIGGER"
		v.Items = []MenuItem{
			{"Mode", ternary(st.Norm, "NORM", "AUTO")},
			{"Slope", slope},
			{"Source", src},
			{"Type", types[st.TrigType&3]},
			{"Holdoff", ho},
		}
	case pgAcq:
		modes := []string{"Normal", "Average", "ERes", "Peak"}
		cnt := "-"
		if st.AcqMode == 1 {
			cnt = fmt.Sprint(st.AvgCount)
		} else if st.AcqMode == 2 {
			cnt = fmt.Sprint(st.EresLen)
		}
		v.Title = "ACQUIRE"
		v.Items = []MenuItem{
			{"Acquire", modes[st.AcqMode&3]},
			{"Count", cnt},
			{"ETS", onoff(st.ETS)},
			{"Mem", depthLabel(st.MemDepth)},
			{"", ""},
		}
	case pgDisp:
		v.Title = "DISPLAY"
		v.Items = []MenuItem{
			{"CH1", onoff(c1)},
			{"CH2", onoff(c2)},
			{"Measure", onoff(meas)},
			{"View", []string{"Y-T", "X-Y", "FFT"}[view%3]},
			{"Math", []string{"Off", "C1+C2", "C1-C2", "C2-C1", "C1xC2"}[mth%5]}, // ASCII font: no '×'
		}
	case pgHoriz:
		v.Title = "HORIZ"
		v.Items = []MenuItem{
			{"Time/div", fmtEng(st.DisplayedS, "s")},
			{"Trig Pos", fmt.Sprintf("%d%%", int(c.trigPos()*100+0.5))},
			{"", ""}, {"", ""}, {"", ""},
		}
	case pgChan:
		cpl0, cpl1, p0, p1 := "DC", "DC", "1x", "1x"
		if c.fe != nil {
			cpl0, cpl1 = cplName(c.fe.Coupling(0)), cplName(c.fe.Coupling(1))
			p0, p1 = probeName(c.fe.ProbeFactor(0)), probeName(c.fe.ProbeFactor(1))
		}
		v.Title = "CHANNEL"
		v.Items = []MenuItem{
			{"C1 Coupling", cpl0},
			{"C2 Coupling", cpl1},
			{"C1 Probe", p0},
			{"C2 Probe", p1},
			{"", ""},
		}
	case pgRef:
		saved := func(has bool) string {
			if has {
				return "saved"
			}
			return "empty"
		}
		show := func(has, s bool) string {
			if !has {
				return "-"
			}
			return onoff(s)
		}
		v.Title = "REF A/B"
		v.Items = []MenuItem{
			{"Save A", saved(rA)},
			{"Save B", saved(rB)},
			{"Show A", show(rA, rAs)},
			{"Show B", show(rB, rBs)},
			{"Clear", ""},
		}
	case pgCursor:
		typ, sel := "Time", "A"
		if curType == 1 {
			typ = "Volts"
		}
		if curSel == 1 {
			sel = "B"
		}
		v.Title = "CURSOR"
		v.Items = []MenuItem{
			{"Cursors", onoff(curOn)},
			{"Type", typ},
			{"Active", sel},
			{"Move +/-", "ADJUST"},
			{"", ""},
		}
	}
	return v
}

// ---- panel event INJECTION (spec 08 §6 test surface) ----
//
// InjectButton / InjectKnob drive the exact same dispatch the SIGIO decode does,
// so every front-panel action is reachable over the API; only the physical
// matrix decode itself still needs a real press to validate.

func nameCode(name string) (int, bool) {
	switch strings.ToLower(name) {
	case "run", "runstop":
		return btnRunStop, true
	case "single":
		return btnSingle, true
	case "auto":
		return btnAuto, true
	case "f1":
		return btnF1, true
	case "f2":
		return btnF2, true
	case "f3":
		return btnF3, true
	case "f4":
		return btnF4, true
	case "f5":
		return btnF5, true
	case "trigmenu", "trigger":
		return btnTrigMenu, true
	case "acquire", "acq":
		return btnAcquire, true
	case "display", "disp":
		return btnDisplay, true
	case "horizmenu", "horiz":
		return btnHorizMenu, true
	case "menu":
		return btnMenuOnOff, true
	case "measure", "meas":
		return btnMeasure, true
	case "cursors", "cursor":
		return btnCursors, true
	case "ch1":
		return btnCh1, true
	case "ch2":
		return btnCh2, true
	}
	return 0, false
}

var knobNames = map[string]bool{
	"tdiv": true, "ch1vdiv": true, "ch2vdiv": true, "ch1pos": true,
	"ch2pos": true, "triglevel": true, "adjust": true, "horizpos": true,
}

// InjectButton runs a named button press on the panel goroutine (non-blocking).
func (c *Controller) InjectButton(name string) bool {
	code, ok := nameCode(name)
	if !ok {
		return false
	}
	select {
	case c.inject <- func() { c.button(code) }:
		return true
	default:
		return false
	}
}

// InjectKnob runs a named knob step (dir ±1, steps≥1) on the panel goroutine.
func (c *Controller) InjectKnob(name string, dir, steps int) bool {
	if !knobNames[name] {
		return false
	}
	if steps < 1 {
		steps = 1
	}
	if dir >= 0 {
		dir = 1
	} else {
		dir = -1
	}
	select {
	case c.inject <- func() { c.resync(); c.dispatch(name, dir, steps) }:
		return true
	default:
		return false
	}
}

func depthLabel(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

func mod5(x int) int { return ((x % 5) + 5) % 5 }
func mod4(x int) int { return ((x % 4) + 4) % 4 }
func mod3(x int) int { return ((x % 3) + 3) % 3 }

// nextHoldoff steps the trigger-holdoff ladder (Off → 100 µs → 1 ms → 10 ms →
// 100 ms → 1 s), clamped at the ends.
func nextHoldoff(cur float64, dir int) float64 {
	opts := []float64{0, 100e-6, 1e-3, 10e-3, 100e-3, 1}
	idx := 0
	for i, v := range opts {
		if cur >= v*(1-1e-6) {
			idx = i
		}
	}
	return opts[clampInt(idx+dir, 0, len(opts)-1)]
}

// cplName / probeName format the pgChan values; both mirror the analog layer's
// coupling constants (DC=0, AC=1, GND=2) and probe ladder (×1/×10/×100).
func cplName(mode int) string {
	switch mode {
	case 1:
		return "AC"
	case 2:
		return "GND"
	default:
		return "DC"
	}
}
func probeName(x float64) string {
	if x >= 100 {
		return "100x"
	}
	if x >= 10 {
		return "10x"
	}
	return "1x"
}

// nextProbe steps the ×1/×10/×100 ladder in the given direction (clamped).
func nextProbe(cur float64, dir int) float64 {
	opts := []float64{1, 10, 100}
	idx := 0
	for i, v := range opts {
		if cur >= v {
			idx = i
		}
	}
	return opts[clampInt(idx+dir, 0, len(opts)-1)]
}
func ternary(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}
func nextOpt(opts []int, cur, dir int) int {
	idx := 0
	for i, v := range opts {
		if v == cur {
			idx = i
			break
		}
	}
	return opts[clampInt(idx+dir, 0, len(opts)-1)]
}
func clampF(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
func fmtEng(v float64, unit string) string {
	a := v
	if a < 0 {
		a = -a
	}
	switch {
	case a >= 1:
		return fmt.Sprintf("%.3g %s", v, unit)
	case a >= 1e-3:
		return fmt.Sprintf("%.3g m%s", v*1e3, unit)
	case a >= 1e-6:
		return fmt.Sprintf("%.3g u%s", v*1e6, unit) // ASCII 'u' — the LCD font has no 'µ'
	default:
		return fmt.Sprintf("%.3g n%s", v*1e9, unit)
	}
}
