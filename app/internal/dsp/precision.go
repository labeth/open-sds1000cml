// Package dsp implements the ARM part of acquisition signal conditioning.
// ENGMODEL-OWNER-UNIT: FU-APP-DSP
package dsp

// PrecisionGuard samples at either end have only the FPGA CIC filtering; the
// centered ARM FIR cannot process them without inventing out-of-record input.
const PrecisionGuard = 31

// ConditionQ8 compensates CIC3 passband droop and rejects out-of-band aliases
// before measurement/decoding. The qualified flat passband ends at 0.075 Fs.
// It preserves DC exactly and uses integer arithmetic on the ARM. scratch must
// be at least len(samples), and must not alias samples. Boundary samples remain
// available, but callers must exclude PrecisionGuard from filtered analysis.
// TRLC-LINKS: REQ-SDS-037
func ConditionQ8(samples, scratch []uint16) {
	if len(samples) < len(precisionTaps) {
		return
	}
	copy(scratch[:len(samples)], samples)
	for i := PrecisionGuard; i < len(samples)-PrecisionGuard; i++ {
		window := (*[63]uint16)(scratch[i-31 : i+32])
		sum := precisionWindow(window)
		v := (sum + (1 << 21)) >> 22
		if v < 0 {
			v = 0
		}
		if v > 65535 {
			v = 65535
		}
		samples[i] = uint16(v)
	}
}
