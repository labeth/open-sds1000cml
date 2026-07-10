package panel

// Display-state accessors for the SCPI handler (scpi.Display): the panel
// controller owns the X-Y view, persistence and menu state that the SCPI
// display commands (XYDS/PESU/MENU) set and query, so wiring both surfaces to
// these methods keeps SCPI and the LCD showing the same truth. All state is
// guarded by the controller mutex — safe from the VXI-11 goroutine.

// ViewXY reports whether the device display is in the X-Y view (the DISPLAY
// menu "View" slot).
func (c *Controller) ViewXY() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.viewMode == 1
}

// SetViewXY enters (true) or leaves (false) the X-Y view. Leaving only
// returns to Y-T when X-Y is the current view — "X-Y display off" must not
// clobber an unrelated view (FFT/Bode/Spectrogram).
func (c *Controller) SetViewXY(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.viewMode = 1
	} else if c.viewMode == 1 {
		c.viewMode = 0
	}
}

// PersistOn reports the display-persistence (afterglow) state.
func (c *Controller) PersistOn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.persist
}

// SetPersist sets the display persistence (the CHANNEL menu toggle).
func (c *Controller) SetPersist(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persist = on
}

// MenuOpen reports whether the on-screen softkey menu is visible.
func (c *Controller) MenuOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.menuPage != pgNone
}

// SetMenuOpen shows (the MAIN page) or hides the on-screen softkey menu —
// the SCPI MENU ON/OFF verb. pushLEDs afterwards: closing e.g. the DISPLAY
// page must also drop that key's lamp (same discipline as openMenu).
func (c *Controller) SetMenuOpen(on bool) {
	c.mu.Lock()
	if on {
		if c.menuPage == pgNone {
			c.menuPage, c.menuSel = pgMain, 0
		}
	} else {
		c.menuPage = pgNone
	}
	c.mu.Unlock()
	c.pushLEDs()
}
