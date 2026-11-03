// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

// SoundPaused reports the pages' SOUND leases refused (the LCD holds the
// acquisition until a page takes the scope).
// TRLC-LINKS: REQ-SDS-277
func (c *Controller) SoundPaused() bool { return c.snd.remotePaused() }

// remotePaused reports whether the pages' leases are refused now.
// TRLC-LINKS: REQ-SDS-277
func (s *soundSvc) remotePaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.remoteOKLocked()
}
