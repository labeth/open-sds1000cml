// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "math"

// triggerRefineSpan is how far either side of the hardware trigger index the
// exact crossing is sought. The FPGA reports the trigger per SRAM word (two
// samples) after its hysteresis comparator fires, so the true level crossing
// lies within a few samples.
const triggerRefineSpan = 12

// refineTriggerEdge returns the sub-sample position where the trigger channel
// crosses level in the trigger's direction, nearest the hardware index edgeX,
// by linear interpolation between the bracketing samples (Q8.8 values when q
// is present, codes otherwise). level is in 8-bit codes. It returns edgeX
// unchanged when no crossing lies within triggerRefineSpan. Word-level
// placement left the displayed edge wandering 4 ns peak to peak, 0.8
// divisions at 5 ns/div (bench 2026-10-06).
// TRLC-LINKS: REQ-SDS-011
func refineTriggerEdge(sig []uint8, q []uint16, valid int, edgeX, level float64, rising bool) float64 {
	if valid < 2 || !(edgeX >= 0) || math.IsInf(edgeX, 0) {
		return edgeX
	}
	at := func(i int) float64 {
		if len(q) >= valid {
			return float64(q[i]) / 256
		}
		return float64(sig[i])
	}
	centre := int(edgeX)
	lo, hi := max(centre-triggerRefineSpan, 0), min(centre+triggerRefineSpan, valid-1)
	best, bestDist := edgeX, math.Inf(1)
	for i := lo; i < hi; i++ {
		a, b := at(i), at(i+1)
		crossed := a < level && b >= level
		if !rising {
			crossed = a > level && b <= level
		}
		if !crossed || a == b {
			continue
		}
		x := float64(i) + (level-a)/(b-a)
		if d := math.Abs(x - edgeX); d < bestDist {
			best, bestDist = x, d
		}
	}
	return best
}

// confirmTriggerEdge reports whether the calibrated record really crosses
// level in the trigger's direction at the hardware trigger index edgeX. The
// fabric compares raw interleaved codes, whose five converters sit up to ~11
// codes apart: a level within that spread of a flat baseline fires on one
// converter's noise, with no edge in the signal (bench 2026-10-07: 5 V/div
// at 1.6 V, 10 V/div at 1.6 V). Such a frame is not a trigger. An envelope
// (min, max pairs) confirms when the buckets beside the index reach both
// sides of the level.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-016
func confirmTriggerEdge(sig []uint8, q []uint16, valid int, edgeX, level float64, rising, envelope bool) bool {
	if valid < 2 || !(edgeX >= 0) || edgeX >= float64(valid) {
		return false
	}
	at := func(i int) float64 {
		if len(q) >= valid {
			return float64(q[i]) / 256
		}
		return float64(sig[i])
	}
	centre := int(edgeX)
	if envelope {
		below, above := false, false
		for i := max(centre-4, 0); i <= min(centre+4, valid-1); i++ {
			v := at(i)
			below = below || v < level
			above = above || v >= level
		}
		return below && above
	}
	for i := max(centre-triggerRefineSpan, 0); i < min(centre+triggerRefineSpan, valid-1); i++ {
		a, b := at(i), at(i+1)
		if (rising && a < level && b >= level) || (!rising && a > level && b <= level) {
			return true
		}
	}
	return false
}
