// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

// Transfer bins (a, b) pairs into a w x h histogram over the given volt
// ranges (row 0 the top): the XY transfer curve's density.
// TRLC-LINKS: REQ-SDS-271
func Transfer(a, b []float64, w, h int, aLo, aHi, bLo, bHi float64) []uint16 {
	out := make([]uint16, w*h)
	if w <= 0 || h <= 0 || !(aHi > aLo) || !(bHi > bLo) {
		return out
	}
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		x := int((a[i] - aLo) / (aHi - aLo) * float64(w))
		y := h - 1 - int((b[i]-bLo)/(bHi-bLo)*float64(h))
		if x < 0 || x >= w || y < 0 || y >= h {
			continue
		}
		if c := &out[y*w+x]; *c < 65535 {
			*c++
		}
	}
	return out
}
