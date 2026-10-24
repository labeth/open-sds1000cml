// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

// precisionWindow uses signed multiply-accumulate long with the unchanged
// Q22 taps. The unsigned sample pair sum fits a signed 32-bit operand.
//
// TRLC-LINKS: REQ-SDS-037
//
//go:noescape
func precisionWindow(window *[63]uint16) int64

// precisionBlock filters n samples from the windows at src into dst.
//
// TRLC-LINKS: REQ-SDS-037
//
//go:noescape
func precisionBlock(src, dst *uint16, n int)
