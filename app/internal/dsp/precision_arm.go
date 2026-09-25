// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

// precisionWindow uses signed multiply-accumulate long with the unchanged
// Q22 taps. The unsigned sample pair sum fits a signed 32-bit operand.
//
//go:noescape
// TRLC-LINKS: REQ-SDS-037
func precisionWindow(window *[63]uint16) int64
