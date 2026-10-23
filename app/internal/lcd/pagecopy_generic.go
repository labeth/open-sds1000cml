//go:build !arm

// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

// TRLC-LINKS: REQ-SDS-021
func copyPage(dst, src []byte) {
	copy(dst, src)
}
