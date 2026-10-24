//go:build !arm

// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

import "unsafe"

// TRLC-LINKS: REQ-SDS-037
func precisionWindow(window *[63]uint16) int64 {
	// Two sums, so the multiplies need not wait on each other; integers, so
	// the total is the same.
	a := int64(precisionTaps[31]) * int64(window[31])
	var b int64
	for j := 0; j < 30; j += 2 {
		a += int64(precisionTaps[j]) * int64(uint32(window[j])+uint32(window[62-j]))
		b += int64(precisionTaps[j+1]) * int64(uint32(window[j+1])+uint32(window[61-j]))
	}
	a += int64(precisionTaps[30]) * int64(uint32(window[30])+uint32(window[32]))
	return a + b
}

// TRLC-LINKS: REQ-SDS-037
func precisionBlock(src, dst *uint16, n int) {
	in := unsafe.Slice(src, n+62)
	out := unsafe.Slice(dst, n)
	for k := range out {
		v := (precisionWindow((*[63]uint16)(in[k:k+63])) + (1 << 21)) >> 22
		out[k] = uint16(min(max(v, 0), 65535))
	}
}
