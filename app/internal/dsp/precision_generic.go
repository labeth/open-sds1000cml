//go:build !arm

// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

// TRLC-LINKS: REQ-SDS-037
func precisionWindow(window *[63]uint16) int64 {
	sum := int64(precisionTaps[31]) * int64(window[31])
	for j := 0; j < 31; j++ {
		sum += int64(precisionTaps[j]) * int64(uint32(window[j])+uint32(window[62-j]))
	}
	return sum
}
