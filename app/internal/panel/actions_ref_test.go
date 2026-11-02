// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

// snapProto reads the decode protocol.
// TRLC-LINKS: REQ-SDS-214
func (c *Controller) snapProto() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decProto
}
