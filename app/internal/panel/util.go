// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import "slices"

// stepNs steps a width/time value along a 1-2-5 nanosecond ladder from
// 10 ns to 10 s; a press (wrap) cycles past the top back to the bottom. It
// reaches 10 s so a UART idle or a slow pulse can be qualified from the panel.
// TRLC-LINKS: REQ-SDS-136
func stepNs(cur float64, dir int, wrap bool) float64 {
	ladder := nsLadder
	idx := 0
	for i, v := range ladder {
		if cur >= v*(1-1e-9) {
			idx = i
		}
	}
	return ladder[stepIndex(idx, dir, len(ladder), wrap)]
}

// nsLadder is stepNs's ladder, built once.
var nsLadder = func() []float64 {
	var ladder []float64
	for d := 10.0; d <= 1e10*(1+1e-9); d *= 10 {
		ladder = append(ladder, d, 2*d, 5*d)
	}
	return ladder[:len(ladder)-2] // ... 5 s, 10 s
}()

// orderNs keeps a min/max time window open: when an edit closes or inverts
// it, the other limit moves one ladder step away (maxEdited: the max was
// edited, so the min gives way). An inverted window never triggers, so W min
// must not step past W max.
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

// b2ic is 1 for true, 0 for false.
// TRLC-LINKS: REQ-SDS-136
func b2ic(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mod5 is x modulo 5, never negative.
// TRLC-LINKS: REQ-SDS-136
func mod5(x int) int { return ((x % 5) + 5) % 5 }

// modViews wraps a display view number into the views there are.
// TRLC-LINKS: REQ-SDS-273
func modViews(x int) int { return ((x % numViews) + numViews) % numViews }

// mod3 is x modulo 3, never negative.
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
		if wrap { // a press past the top goes back to Off, not stuck at the top
			return opts[0]
		}
		return opts[len(opts)-1]
	}
	for _, opt := range slices.Backward(opts) {
		if opt < cur*(1-1e-6) {
			return opt
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

// choose is yes when cond holds, else no (Go has no conditional
// expression; both are evaluated).
// TRLC-LINKS: REQ-SDS-211
func choose[T any](cond bool, yes, no T) T {
	if cond {
		return yes
	}
	return no
}
