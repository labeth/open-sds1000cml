// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import "math"

// hypotFinite is math.Hypot's own arithmetic on this architecture (pure Go
// here) without its infinity and NaN checks: a spectrum's bins are finite,
// and the checks were a third of the call, once a bin a frame.
// TRLC-LINKS: REQ-SDS-070
func hypotFinite(p, q float64) float64 {
	p, q = math.Abs(p), math.Abs(q)
	if p < q {
		p, q = q, p
	}
	if p == 0 {
		return 0
	}
	q = q / p
	return p * math.Sqrt(1+q*q)
}
