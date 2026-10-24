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
	src := scratch[:len(samples)]
	copy(src, samples)
	// One call for the record's interior: a Go call a sample cost the scope
	// an eighth of the filter's time.
	precisionBlock(&src[0], &samples[PrecisionGuard], len(samples)-2*PrecisionGuard)
}
