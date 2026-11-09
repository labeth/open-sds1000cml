// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import "github.com/labeth/open-dso-core/hal"

// SetSession attaches the board's own stream sessions (the decoded
// streams its fabric runs: hal.Session), which the core's HAL cycle serves
// and steps. nil: none.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (b *Board) SetSession(s hal.Session) { b.session = s }

// Session is the board's stream sessions; false when none is attached.
// ANY.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func (b *Board) Session() (hal.Session, bool) { return b.session, b.session != nil }

var _ hal.SessionBoard = (*Board)(nil)

// LoadedImage is the image in use ("": the general one). OWNER.
// TRLC-LINKS: REQ-SDS-005
func (b *Board) LoadedImage() string { return b.imageLoaded }
