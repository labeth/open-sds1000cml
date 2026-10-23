// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "unsafe"

// copy64 copies n bytes, a multiple of 64, between word-aligned buffers,
// preloading ahead of the source.
//
// TRLC-LINKS: REQ-SDS-021
//
//go:noescape
func copy64(dst, src *byte, n int)

// copyPage copies src into dst (len(dst) >= len(src)), the bulk through
// copy64 when both are word-aligned.
// TRLC-LINKS: REQ-SDS-021
func copyPage(dst, src []byte) {
	n := len(src) &^ 63
	if n == 0 || len(dst) < len(src) || (uintptr(unsafe.Pointer(&dst[0]))|uintptr(unsafe.Pointer(&src[0])))&3 != 0 {
		copy(dst, src)
		return
	}
	copy64(&dst[0], &src[0], n)
	copy(dst[n:], src[n:])
}
