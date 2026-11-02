// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// learnQualExtent keeps each channel's extent (decode.Extent) from a capture
// taken at scale sc. An end that moved under two codes is kept, so the plan
// does not change (and re-arm) on every capture's noise.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) learnQualExtent(f *Frame, sc [2]chScale) {
	e.learnQualExtentOf(f, sc, nil)
}
