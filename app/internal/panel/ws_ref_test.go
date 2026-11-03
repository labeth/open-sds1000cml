// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import "time"

// SetClock replaces the controller's clock (tests drive notices and the
// rail label with it instead of sleeping).
// TRLC-LINKS: REQ-SDS-211
func (c *Controller) SetClock(now func() time.Time) {
	c.mu.Lock()
	c.clock = now
	c.lastInput = time.Time{} // the idle time starts again on the new clock
	c.mu.Unlock()
}
