package sound

// RateHz is the stream's sample rate a channel at decimation 2^log2.
// TRLC-LINKS: REQ-SDS-265
func RateHz(log2 int) float64 { return 500e6 / float64(uint64(1)<<uint(log2)) }

// Volts converts Q8.8 codes to volts at the tip: v = (code-128)*vpc - off,
// vpc the volts a code (probe included) and off the offset (volts at the tip).
// TRLC-LINKS: REQ-SDS-265, REQ-SDS-266
func Volts(q []uint16, vpc, off float64, dst []float64) []float64 {
	dst = dst[:0]
	k := vpc / 256
	for _, v := range q {
		dst = append(dst, (float64(v)-32768)*k-off)
	}
	return dst
}
