// ENGMODEL-OWNER-UNIT: FU-APP-CAL

package cal

// descramble applies the three byte-involutions IN ORDER (spec 10 §2.2):
// reverse, NOT the back half, NOT the triangular indices. The transform is
// NOT self-inverse — Scramble applies them in reverse order.
// TRLC-LINKS: REQ-SDS-016
func descramble(buf []byte) {
	reverse(buf)
	notBackHalf(buf)
	notTriangular(buf)
}

// Scramble is the write-side inverse (the tests build files with it; the instrument only reads them).
// TRLC-LINKS: REQ-SDS-016
func Scramble(buf []byte) {
	notTriangular(buf)
	notBackHalf(buf)
	reverse(buf)
}

// TRLC-LINKS: REQ-SDS-016
func reverse(buf []byte) {
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
}

// TRLC-LINKS: REQ-SDS-016
func notBackHalf(buf []byte) {
	n := len(buf)
	for i := n - n/2; i < n; i++ {
		buf[i] = ^buf[i]
	}
}
