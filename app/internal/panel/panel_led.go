package panel

// SyncLEDs refreshes the acquisition lamps (RUN/STOP, SINGLE) from the engine and
// re-latches only when the state actually changed — so the lamps follow a single-shot
// self-stopping, or the web/SCPI toggling run, without a front-panel key press.
func (c *Controller) SyncLEDs() {
	st := c.eng.Snapshot()
	c.mu.Lock()
	changed := c.running != st.Running || c.single != st.Single || c.norm != st.Norm
	c.running, c.norm, c.single = st.Running, st.Norm, st.Single
	c.mu.Unlock()
	if changed {
		c.pushLEDs()
	}
}

func (c *Controller) pushLEDs() {
	c.mu.Lock()
	c1, c2, pg, meas := c.chDisp[0], c.chDisp[1], c.menuPage, c.showMeas
	running, single, math := c.running, c.single, c.mathMode
	srActive := c.srActive
	c.mu.Unlock()
	var word uint16
	if c1 {
		word |= ledCH1
	}
	if c2 {
		word |= ledCH2
	}
	if math != 0 {
		word |= ledMath // MATH lamp tracks the math function being active
	}
	if running {
		word |= ledRun
	} else {
		word |= ledStop
	}
	if single { // SINGLE lamp: a single-shot is armed (NOT the NORM trigger mode)
		word |= ledSingle
	}
	if srActive { // UTILITY lamp lit while super-res is stacking (toggle like SINGLE)
		word |= ledUtility
	}
	if meas {
		word |= ledMeasure // MEASURE key lamp tracks the panel toggle
	}
	switch pg { // light the lamp of the button whose page is showing (spec 08 §6.4/§8.2)
	case pgAcq:
		word |= ledAcquire
	case pgRef: // dedicated REF key / ACQUIRE's second page — its own lamp
		word |= ledRef
	case pgDisp, pgChan: // CHANNEL is DISPLAY's second page (no lamp of its own)
		word |= ledDisplay
	case pgCursor: // dedicated CURSORS key / HORIZONTAL's second page
		word |= ledCursors
	}
	c.eng.SetLEDs(word)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
