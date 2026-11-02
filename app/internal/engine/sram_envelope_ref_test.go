// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// envelopeAllowed reports whether a live frame may be read as a display
// envelope (ADR-IMAGE-REGROUP-DECIMATION).
// TRLC-LINKS: REQ-SDS-010
func (e *Engine) envelopeAllowed() bool {
	return e.envelopeBlock() == ""
}
