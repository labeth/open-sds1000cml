// ENGMODEL-OWNER-UNIT: FU-APP-DSP

package dsp

// roundQ8Block rounds n Q8 samples at src to codes at dst.
//
// TRLC-LINKS: REQ-SDS-037
//
//go:noescape
func roundQ8Block(src *uint16, dst *uint8, n int)

// splitQ8Block splits n sample pairs at b into q1, q2 and their codes c1, c2.
//
// TRLC-LINKS: REQ-SDS-037
//
//go:noescape
func splitQ8Block(b *byte, q1, q2 *uint16, c1, c2 *uint8, n int)
