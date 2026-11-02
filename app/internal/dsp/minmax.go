// ENGMODEL-OWNER-UNIT: FU-APP-DSP

package dsp

// minMaxBulk is the shortest run MinMax8 hands to the block: a shorter one
// is not worth the vector unit's result moving back (about 20 cycles on the
// Cortex-A8).
const minMaxBulk = 64

// MinMax8 is the least and the largest code of s (0, 0 for none): a record's
// extremes and a drawn column's, in 32-code blocks on the vector unit where
// the platform has one. Each sample's compare-and-branch is what a scan of a
// whole record cost.
// TRLC-LINKS: REQ-SDS-017
func MinMax8(s []uint8) (lo, hi uint8) {
	if len(s) == 0 {
		return 0, 0
	}
	lo, hi = s[0], s[0]
	if n := len(s) &^ 31; n >= minMaxBulk {
		lo, hi = minMax8Block(&s[0], n)
		s = s[n:]
	}
	for _, v := range s {
		if v < lo {
			lo = v
		} else if v > hi {
			hi = v
		}
	}
	return lo, hi
}
