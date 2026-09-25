// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "fmt"

// stepNs steps a width/time value along a 1-2-5 nanosecond ladder.
// TRLC-LINKS: REQ-SDS-136
func stepNs(cur float64, dir int) float64 {
	ladder := []float64{10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 50000}
	idx := 0
	for i, v := range ladder {
		if cur >= v*(1-1e-9) {
			idx = i
		}
	}
	return ladder[clampInt(idx+dir, 0, len(ladder)-1)]
}

// TRLC-LINKS: REQ-SDS-136
func depthLabel(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}

// TRLC-LINKS: REQ-SDS-136
func b2ic(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TRLC-LINKS: REQ-SDS-136
func mod5(x int) int { return ((x % 5) + 5) % 5 }

// TRLC-LINKS: REQ-SDS-136
func mod5b(x int) int { return ((x % 5) + 5) % 5 } // view-mode cycle (Y-T/X-Y/FFT/Bode/Spgm)

// TRLC-LINKS: REQ-SDS-136
func mod4(x int) int { return ((x % 4) + 4) % 4 }

// TRLC-LINKS: REQ-SDS-136
func mod3(x int) int { return ((x % 3) + 3) % 3 }

// nextHoldoff steps the trigger-holdoff ladder (Off → 100 µs → 1 ms → 10 ms →
// 100 ms → 1 s), clamped at the ends.
// TRLC-LINKS: REQ-SDS-136
func nextHoldoff(cur float64, dir int) float64 {
	opts := []float64{0, 100e-6, 1e-3, 10e-3, 100e-3, 1}
	if dir > 0 {
		for _, v := range opts {
			if v > cur*(1+1e-6) {
				return v
			}
		}
		return opts[len(opts)-1]
	}
	for i := len(opts) - 1; i >= 0; i-- {
		if opts[i] < cur*(1-1e-6) {
			return opts[i]
		}
	}
	return opts[0]
}

// cplName / probeName format the pgChan values; both mirror the analog layer's
// coupling constants (DC=0, AC=1, GND=2) and probe ladder (×1/×10/×100).
// TRLC-LINKS: REQ-SDS-136
func cplName(mode int) string {
	switch mode {
	case 1:
		return "AC"
	case 2:
		return "GND"
	default:
		return "DC"
	}
}

// TRLC-LINKS: REQ-SDS-136
func probeName(x float64) string {
	if x >= 100 {
		return "100x"
	}
	if x >= 10 {
		return "10x"
	}
	return "1x"
}

// nextProbe steps the ×1/×10/×100 ladder in the given direction (clamped).
// TRLC-LINKS: REQ-SDS-136
func nextProbe(cur float64, dir int) float64 {
	opts := []float64{1, 10, 100}
	idx := 0
	for i, v := range opts {
		if cur >= v {
			idx = i
		}
	}
	return opts[clampInt(idx+dir, 0, len(opts)-1)]
}

// TRLC-LINKS: REQ-SDS-136
func ternary(b bool, t, f string) string {
	if b {
		return t
	}
	return f
}

// TRLC-LINKS: REQ-SDS-136
func nextOpt(opts []int, cur, dir int) int {
	idx := 0
	for i, v := range opts {
		if v == cur {
			idx = i
			break
		}
	}
	return opts[clampInt(idx+dir, 0, len(opts)-1)]
}

// TRLC-LINKS: REQ-SDS-136
func clampF(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

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
