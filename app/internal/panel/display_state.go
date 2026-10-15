// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "open-sds/app/internal/engine"

// Display-state accessors for the SCPI handler (scpi.Display): the panel
// controller owns the X-Y view, persistence and menu state that the SCPI
// display commands (XYDS/PESU/MENU) set and query, so wiring both surfaces to
// these methods keeps SCPI and the LCD showing the same truth. All state is
// guarded by the controller mutex — safe from the VXI-11 goroutine.

// ViewXY reports whether the device display is in the X-Y view (the DISPLAY
// menu "View" slot).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) ViewXY() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viewMode == 1
}

// SetViewXY enters (true) or leaves (false) the X-Y view. Leaving only
// returns to Y-T when X-Y is the current view — "X-Y display off" must not
// clobber an unrelated view (FFT/Bode/Spectrogram).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetViewXY(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.viewMode = viewXY
	} else if c.viewMode == viewXY {
		c.viewMode = viewYT
	}
	c.ui.views[c.ui.ws] = c.viewMode // the workspace remembers it
}

// PersistOn reports the display-persistence (afterglow) state.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) PersistOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.persist
}

// SetPersist sets the display persistence (the CHANNEL menu toggle).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetPersist(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persist = on
}

// MenuOpen reports whether a menu is open: a settings sheet or the byte
// picker (SCPI MENU?).
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func (c *Controller) MenuOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ui.open || c.ui.wpick
}

// SetMenuOpen is the SCPI MENU ON/OFF verb, as Menu On/Off does it: ON opens
// this workspace's sheet (where it was left), OFF closes the sheet or the
// picker.
// TRLC-LINKS: REQ-SDS-136, REQ-SDS-212
func (c *Controller) SetMenuOpen(on bool) {
	c.mu.Lock()
	switch {
	case on && !c.ui.open && !c.ui.wpick:
		c.openWsSheetLocked()
	case !on:
		c.closePickLocked()
		c.closeSheetLocked()
	}
	c.mu.Unlock()
	c.afterInput()
}

// TraceOn reports whether a channel's trace is shown (the CH1/CH2 keys).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) TraceOn(ch int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chDisp[ch&1]
}

// SetTraceOn shows or hides a channel's trace (SCPI Cn:TRA), as its key does,
// lamp included.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetTraceOn(ch int, on bool) {
	c.mu.Lock()
	c.chDisp[ch&1] = on
	c.mu.Unlock()
	c.pushLEDs()
}

// adoptQual takes the engine's qualifier parameters into the page's shadows
// (c.mu held), so a step starts from the values in effect. A zero report
// (an engine that does not publish them) keeps the shadows.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) adoptQual(st engine.Stats) {
	q := st.TrigQual
	if q.PulseLvl <= 0 {
		return
	}
	c.videoStd, c.videoLine, c.videoNeg = q.VideoStd, q.VideoLine, q.VideoNeg
	c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond = q.PulseLvl, q.PulseMinNs, q.PulseMaxNs, q.PulseCond
	c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond = q.SlopeLo, q.SlopeHi, q.SlopeMinNs, q.SlopeMaxNs, q.SlopeCond
}

// CursorsOn reports whether the manual cursors are shown (SCPI CRMS?).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) CursorsOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.curOn
}

// SetCursorsOn shows or hides the manual cursors (SCPI CRMS MANUAL/OFF).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetCursorsOn(on bool) {
	c.mu.Lock()
	c.curOn = on
	c.mu.Unlock()
	c.pushLEDs()
}

// MeasOn reports whether the measurement panel is shown.
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) MeasOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.showMeas
}

// SetMeasOn shows or hides the measurement panel (SCPI PACU adds a
// measurement to the display).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) SetMeasOn(on bool) {
	c.mu.Lock()
	c.showMeas = on
	c.mu.Unlock()
	c.pushLEDs()
}

// CursorState reports the manual cursors for SCPI CRVA?: on, the type
// (0 time / 1 volts) and both cursors' screen fractions where the screen
// shows their signal times and levels now (x left->right, y top->bottom;
// beyond 0..1 off the screen).
// TRLC-LINKS: REQ-SDS-136
func (c *Controller) CursorState() (on bool, typ int, x, y [2]float64) {
	s := c.snap()
	return s.curOn, s.curType, s.curX, s.curY
}
