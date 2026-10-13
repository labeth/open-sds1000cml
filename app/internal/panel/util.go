// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import "fmt"

// stepNs steps a width/time value along a 1-2-5 nanosecond ladder from
// 10 ns to 10 s; a press (wrap) cycles past the top back to the bottom. The
// ladder stopped at 50 us, so a UART idle or a slow pulse could not be
// qualified from the panel (bench 2026-10-07).
// TRLC-LINKS: REQ-SDS-136
func stepNs(cur float64, dir int, wrap bool) float64 {
	var ladder []float64
	for d := 10.0; d <= 1e10*(1+1e-9); d *= 10 {
		ladder = append(ladder, d, 2*d, 5*d)
	}
	ladder = ladder[:len(ladder)-2] // ... 5 s, 10 s
	idx := 0
	for i, v := range ladder {
		if cur >= v*(1-1e-9) {
			idx = i
		}
	}
	return ladder[stepIndex(idx, dir, len(ladder), wrap)]
}

// orderNs keeps a min/max time window open: when an edit closes or inverts
// it, the other limit moves one ladder step away (maxEdited: the max was
// edited, so the min gives way). An inverted window never triggered, and
// the panel let W min step past W max (bug hunt #2).
// TRLC-LINKS: REQ-SDS-136
func orderNs(mn, mx float64, maxEdited bool) (float64, float64) {
	if mn < mx {
		return mn, mx
	}
	if maxEdited {
		if lower := stepNs(mx, -1, false); lower < mx {
			return lower, mx
		}
		return mx, stepNs(mx, 1, false) // max at the ladder floor
	}
	if higher := stepNs(mn, 1, false); higher > mn {
		return mn, higher
	}
	return stepNs(mn, -1, false), mn // min at the ladder top
}

// orderFrac keeps slope thresholds low < high in 5% steps the same way (a
// High that wrapped to 5% under Low left a slope trigger that never fired).
// TRLC-LINKS: REQ-SDS-136
func orderFrac(lo, hi float64, hiEdited bool) (float64, float64) {
	if lo < hi-1e-9 {
		return lo, hi
	}
	if hiEdited {
		if hi-0.05 >= 0.05-1e-9 {
			return hi - 0.05, hi
		}
		return hi, hi + 0.05
	}
	if lo+0.05 <= 0.95+1e-9 {
		return lo, lo + 0.05
	}
	return lo - 0.05, lo
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
func mod3(x int) int { return ((x % 3) + 3) % 3 }

// nextHoldoff steps the trigger-holdoff ladder (holdoffLadder: Off, then
// 1-2-5 from 100 µs to 10 s), clamped at the ends.
// TRLC-LINKS: REQ-SDS-136
func nextHoldoff(cur float64, dir int, wrap bool) float64 {
	opts := holdoffLadder
	if dir > 0 {
		for _, v := range opts {
			if v > cur*(1+1e-6) {
				return v
			}
		}
		if wrap { // a press past the top goes back to Off (bug hunt #2: it stuck)
			return opts[0]
		}
		return opts[len(opts)-1]
	}
	for i := len(opts) - 1; i >= 0; i-- {
		if opts[i] < cur*(1-1e-6) {
			return opts[i]
		}
	}
	if wrap {
		return opts[len(opts)-1]
	}
	return opts[0]
}

// stepIndex moves idx by dir within n entries: a press (wrap) cycles past
// the last back to the first; a knob stops at the ends.
// TRLC-LINKS: REQ-SDS-136
func stepIndex(idx, dir, n int, wrap bool) int {
	if wrap {
		return ((idx+dir)%n + n) % n
	}
	return clampInt(idx+dir, 0, n-1)
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
