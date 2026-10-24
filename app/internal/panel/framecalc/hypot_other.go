//go:build !arm

// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package framecalc

import "math"

// hypotFinite is math.Hypot (in assembly on some architectures).
// TRLC-LINKS: REQ-SDS-070
func hypotFinite(p, q float64) float64 { return math.Hypot(p, q) }
