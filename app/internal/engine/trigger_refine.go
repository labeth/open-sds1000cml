// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-dso-core/sample"
)

// placeEdgeTrigger checks a hardware edge trigger on the calibrated record
// and places it on its level crossing (the FPGA's index is word-granular).
// It reports false for a trigger with no calibrated crossing near its index
// - the raw codes' converter spread firing on noise at a small signal's rail
// - which the caller drops; those are counted, and their recent share is
// what the TRIG tab warns on. Envelope frames hold bucket extremes, not
// samples in time order: they are checked, not refined.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) placeEdgeTrigger(f *sample.Frame, disc []uint8, q []uint16, level float64, rising bool) bool {
	confirmed := core.ConfirmTriggerEdge(disc, q, f.Len(), f.TriggerAt, level, rising, f.Content == sample.PeakPairs)
	if !confirmed && uint32(f.AcqDecim) > 1 {
		// the comparators read the raw samples, with a re-arm band past the
		// converters' spread (planHysteresis), and decimated a crossing holds
		// for a sample of every converter (ADR-EDGE-TRIGGER-RAW): no
		// converter's offset fires them. A decimated record whose averages hide the crossing (an
		// edge faster than one of its samples, a glitch) keeps the FPGA's
		// index: it has no crossing to place the trigger on.
		e.noteNoiseTrigger(false)
		return true
	}
	e.noteNoiseTrigger(!confirmed)
	if confirmed && f.Content != sample.PeakPairs {
		f.TriggerAt = core.RefineTriggerEdge(disc, q, f.Len(), f.TriggerAt, level, rising)
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
