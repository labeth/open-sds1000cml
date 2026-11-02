// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

// srSeedAndStart grabs the current frame, builds a fresh stack sized for the
// current srK/srCh, seeds it as the locked reference, resumes RUN and (re)starts
// the stacker goroutine. Any existing stacker is stopped first (its old Stack is
// a captured local, so no race with the new one). Returns false if there is no
// usable frame or the reference is unusable.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSeedAndStart() bool {
	c.mu.Lock()
	gen := c.srSeedGen
	c.mu.Unlock()
	return c.srSeedAndStartGen(gen)
}
