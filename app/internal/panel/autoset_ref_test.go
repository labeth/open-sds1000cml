// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func (c *Controller) runAutoset(stop chan struct{}) {
	c.mu.Lock()
	ws, view := c.ui.ws, c.viewMode
	c.mu.Unlock()
	c.runAutosetFor(stop, ws, view)
}

// rawCenterErr reports how far a channel's signal MIDPOINT ((min+max)/2) sits from
// screen centre, in divisions (+ve = above centre), read straight off the ADC codes
// (128 = centre, 25 codes/div). Uses 1%-trimmed rails so a stray spike can't skew it,
// and the midpoint (not the mean) so duty cycle doesn't matter. Model-independent, so
// it corrects any offset-DAC calibration drift.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) rawCenterErr(ch int) float64 {
	errDiv, _ := c.rawCentre(ch)
	return errDiv
}

// offScreen reports whether a channel's trace still sits off-screen after
// centring, so the 3c guard must coarsen it onto a range where the offset works.
// It catches BOTH ways a DC-heavy signal escapes a sensitive range: a hard ADC
// rail (railing), and front-end-amp SATURATION — where the trace pins at the
// screen edge (raw-code midpoint ~251, i.e. > 2.5 div off centre) yet the codes
// never reach the ADC rail. rawCenterErr is the same model-independent raw-code
// midpoint read used by step 3b (+ve = above centre), so a legitimately centred
// full-screen signal reads ≈0 and never false-coarsens.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) offScreen(ch int) bool { return c.offScreenFor(ch, nil) }
