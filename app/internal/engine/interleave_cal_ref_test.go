// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

// supportsScale reports whether both channels sit at a measured scale. Offset
// correction no longer needs it; it remains for range bookkeeping.
// TRLC-LINKS: REQ-SDS-016
func (c *InterleaveCalibration) supportsScale(scale [2]float64) bool {
	for ch := range 2 {
		matched := scale[ch] == c.Vdiv[ch]
		for _, v := range c.VerifiedVdiv[ch] {
			if scale[ch] == v {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
