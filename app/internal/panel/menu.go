package panel

import (
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
	pgChan     // per-channel coupling + probe (reached by re-pressing DISPLAY)
	pgCursor   // on-screen cursors (reached by re-pressing HORIZONTAL / CURSORS key)
	pgRef      // reference waveforms REF A/B (reached by re-pressing ACQUIRE)
	pgMain     // MAIN menu (MENU key): navigates to every sub-page
	pgTrigQ    // trigger-qualifier params (reached by re-pressing TRIGGER)
	pgDecode   // protocol decode (MAIN ▸ Decode)
	pgSuperres // super-res stack-and-crunch config (opened by UTILITY while active)
	pgMask     // mask testing (reached by re-pressing ACQUIRE past REF)
)

// MenuItem is one softkey slot: a label and its current value.
type MenuItem struct{ Label, Value string }

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
	btnMath      = bcode(2, 12) // spec 08 §6.4 — was unwired (dead button)
	btnRef       = bcode(3, 12) // spec 08 §6.4 — was unwired (dead button)
	btnCh1       = bcode(2, 4)
	btnCh2       = bcode(3, 4)
)

var softkeys = []int{btnF1, btnF2, btnF3, btnF4, btnF5}

// menuButton handles a menu-related button; returns true if it consumed it.
func (c *Controller) menuButton(code int) bool {
	switch code {
	case btnTrigMenu:
		// TRIGGER cycles between the trigger page and its qualifier-params page.
		c.mu.Lock()
		next := pgTrig
		if c.menuPage == pgTrig {
			next = pgTrigQ
		}
		c.mu.Unlock()
		c.openMenu(next)
		return true
	case btnAcquire:
		// ACQUIRE cycles acquire -> reference -> mask pages, so all three
		// acquisition-side surfaces hang off one physical button.
		c.mu.Lock()
		next := pgAcq
		if c.menuPage == pgAcq {
			next = pgRef
		} else if c.menuPage == pgRef {
			next = pgMask
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
	case btnRef:
		c.openMenu(pgRef) // dedicated REF key -> straight to the REF A/B page
		return true
	case btnMath:
		// Dedicated MATH key: cycle the math trace (off->C1+C2->C1-C2->C2-C1->C1xC2).
		c.mu.Lock()
		c.mathMode = mod5(c.mathMode + 1)
		c.mu.Unlock()
		c.pushLEDs()
		return true
	case btnCh1:
		c.mu.Lock()
		c.chDisp[0] = !c.chDisp[0]
		c.mu.Unlock()
		c.pushLEDs() // CH1 lamp follows the toggle immediately
		return true
	case btnCh2:
		c.mu.Lock()
		c.chDisp[1] = !c.chDisp[1]
		c.mu.Unlock()
		c.pushLEDs() // CH2 lamp follows the toggle immediately
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
// pageSlots may read c.decProto, so callers must hold c.mu.
func (c *Controller) pageSlots(pg int) int {
	switch pg {
	case pgHoriz:
		return 3
	case pgAcq:
		return 4
	case pgChan:
		return 5
	case pgSuperres: // Channel / Grid×K / Stop-on / Target / Reset
		return 5
	case pgMask: // Mode / Build / Frames / Tol / Reset
		return 5
	case pgDecode: // varies by protocol — don't let the highlight land on a blank slot
		switch c.decProto {
		case 0: // Off — only the Proto selector
			return 1
		case 1: // Auto — Proto, Format
			return 2
		case 4: // SPI — Proto, CLK, DATA, Mode, Format
			return 5
		default: // UART, I2C — Proto, param, param, Format
			return 4
		}
	default: // pgTrig, pgDisp, pgCursor, pgRef
		return 5
	}
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

// ---- panel event INJECTION (spec 08 §6 test surface) ----
//
// InjectButton / InjectKnob drive the exact same dispatch the SIGIO decode does,
// so every front-panel action is reachable over the API; only the physical
// matrix decode itself still needs a real press to validate.
