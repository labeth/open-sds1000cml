// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import "fmt"

// TRLC-LINKS: REQ-SDS-136
func fmtEng(v float64, unit string) string {
	a := v
	if a < 0 {
		a = -a
	}
	// Rounding-aware prefix boundaries: %.3g rounds 999.9 up to "1e+03", which
	// would print e.g. "1e+03 ns" for a 1 µs value. 0.9995·scale as the boundary
	// promotes such a value to the next prefix ("1 us"). ASCII 'u' for micro —
	// the LCD font has no 'µ'.
	switch {
	case a >= 0.9995:
		return fmt.Sprintf("%.3g %s", v, unit)
	case a >= 0.9995e-3:
		return fmt.Sprintf("%.3g m%s", v*1e3, unit)
	case a >= 0.9995e-6:
		return fmt.Sprintf("%.3g u%s", v*1e6, unit)
	default:
		return fmt.Sprintf("%.3g n%s", v*1e9, unit)
	}
}
