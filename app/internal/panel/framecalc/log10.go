// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import "math"

// Log10 is math.Log10 to within a few units in the last place, for the
// levels of a spectrum's every bin: math.Log10 is software on the scope
// (frexp, its special cases, a division and a polynomial), about a
// microsecond a bin, the largest part of a spectrum's levels after the
// transform. A positive normal x splits into 2^e*m, m within 1/256 above
// a table point c, and ln(m/c) is a short series in m/c-1. Zero, negative,
// subnormal, infinite and NaN x take math.Log10.
// TRLC-LINKS: REQ-SDS-070
func Log10(x float64) float64 {
	b := math.Float64bits(x)
	e := int(b>>52) - 1023
	if e <= -1023 || e >= 1024 { // sign set, zero or subnormal, Inf or NaN
		return math.Log10(x)
	}
	i := int(b>>44) & 0xff
	m := math.Float64frombits(b&(1<<52-1) | 1023<<52) // in [1, 2)
	r := m*log10Inv[i] - 1                            // |r| < 1/256
	// ln(1+r) to r^5: the next term is under 1e-15 of ln 2
	p := r * (1 + r*(-0.5+r*(1.0/3+r*(-0.25+r*0.2))))
	return (float64(e)*math.Ln2 + log10Ln[i] + p) * (1 / math.Ln10)
}

// log10Inv[i] is 1/c and log10Ln[i] ln c for c = 1 + i/256.
var log10Inv, log10Ln = func() (inv, ln [256]float64) {
	for i := range inv {
		c := 1 + float64(i)/256
		inv[i], ln[i] = 1/c, math.Log(c)
	}
	return
}()
