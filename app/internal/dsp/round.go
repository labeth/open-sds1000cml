// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

// RoundQ8 writes each Q8 sample of src rounded to its code into dst: (v +
// 128) >> 8, at most 255, the codes a conditioned record is drawn and
// measured from. dst must hold len(src).
// TRLC-LINKS: REQ-SDS-037
func RoundQ8(dst []uint8, src []uint16) {
	if len(src) == 0 {
		return
	}
	_ = dst[len(src)-1]
	roundQ8Block(&src[0], &dst[0], len(src))
}

// SplitQ8 takes len(q1) Q8 sample pairs from b (little-endian: channel 1's,
// then channel 2's, four bytes a pair, as a precision record's words come)
// into q1 and q2 and their codes (RoundQ8) into c1 and c2. q2, c1 and c2
// must hold len(q1), b 4*len(q1) bytes.
// TRLC-LINKS: REQ-SDS-037
func SplitQ8(q1, q2 []uint16, c1, c2 []uint8, b []byte) {
	n := len(q1)
	if n == 0 {
		return
	}
	_, _, _, _ = q2[n-1], c1[n-1], c2[n-1], b[4*n-1]
	splitQ8Block(&b[0], &q1[0], &q2[0], &c1[0], &c2[0], n)
}
