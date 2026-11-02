//go:build !arm

// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

import "unsafe"

// TRLC-LINKS: REQ-SDS-037
func roundQ8Block(src *uint16, dst *uint8, n int) {
	in, out := unsafe.Slice(src, n), unsafe.Slice(dst, n)
	for i, v := range in {
		out[i] = uint8(min((uint32(v)+128)>>8, 255))
	}
}

// TRLC-LINKS: REQ-SDS-037
func splitQ8Block(b *byte, q1, q2 *uint16, c1, c2 *uint8, n int) {
	in := unsafe.Slice(b, 4*n)
	a, c := unsafe.Slice(q1, n), unsafe.Slice(q2, n)
	ca, cc := unsafe.Slice(c1, n), unsafe.Slice(c2, n)
	for k := range a {
		x, y := uint16(in[4*k])|uint16(in[4*k+1])<<8, uint16(in[4*k+2])|uint16(in[4*k+3])<<8
		a[k], c[k] = x, y
		ca[k], cc[k] = uint8(min((uint32(x)+128)>>8, 255)), uint8(min((uint32(y)+128)>>8, 255))
	}
}
