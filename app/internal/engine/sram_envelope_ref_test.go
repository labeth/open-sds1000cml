// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "open-sds/app/internal/sramcapture"

// envelopeAllowed reports whether a live frame may be read as a display
// envelope (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeAllowed(cfg sramcapture.Config, tp trigParams) bool {
	return e.envelopeBlock(&cfg, tp) == ""
}
