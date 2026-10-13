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
// at 1.6 V, 10 V/div at 1.6 V). Such a frame is not a trigger: software may
// check a trigger the FPGA pointed out, never search data it did not point
// at (owner rule). An envelope (min, max pairs) confirms when the buckets
// beside the index reach both sides of the level.
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

// placeEdgeTrigger checks a hardware edge trigger on the calibrated record
// and places it on its level crossing (the FPGA's index is word-granular).
// It reports false for a trigger with no calibrated crossing near its index
// - the raw codes' converter spread firing on noise at a small signal's rail
// - which the caller drops; those are counted, and their recent share is
// what the TRIG tab warns on. Envelope frames hold bucket extremes, not
// samples in time order: they are checked, not refined.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) placeEdgeTrigger(f *Frame, disc []uint8, q []uint16, level float64, rising bool) bool {
	confirmed := confirmTriggerEdge(disc, q, f.Valid, f.EdgeX, level, rising, f.PeakDetect)
	e.noteNoiseTrigger(!confirmed)
	if confirmed && !f.PeakDetect {
		f.EdgeX = refineTriggerEdge(disc, q, f.Valid, f.EdgeX, level, rising)
		f.TrigPos = int(f.EdgeX)
	}
	return confirmed
}

// noteNoiseTrigger counts a hardware edge trigger with no calibrated edge at
// its index and keeps their recent share (an average over about the last
// twenty triggers).
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) noteNoiseTrigger(noise bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	x := 0.0
	if noise {
		x = 1
		e.stats.FalseTriggers++
	}
	e.stats.NoiseTriggers += (x - e.stats.NoiseTriggers) / 20
}

// clearNoiseTriggers forgets the noise-trigger share when the trigger, the
// timebase or the mode changes: the next triggers say anew.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) clearNoiseTriggers() {
	e.mu.Lock()
	e.stats.NoiseTriggers = 0
	e.mu.Unlock()
}
