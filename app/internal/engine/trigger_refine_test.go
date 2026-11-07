package engine

import (
	"math"
	"testing"

	"github.com/labeth/open-dso-core/sample"
)

// Software may check a trigger the FPGA pointed out (owner rule): a hardware
// edge trigger with no calibrated crossing near its index is reported for
// dropping and counted; a real edge is placed on its crossing. The share of
// dropped triggers is what the TRIG tab warns on, and a change of the
// trigger forgets it.
// TRLC-LINKS: REQ-SDS-011
func TestUnconfirmedEdgeTriggersAreCounted(t *testing.T) {
	e := &Engine{}
	flat := make([]uint8, 200)
	edge := make([]uint8, 200)
	for i := range flat {
		flat[i], edge[i] = 128, 128
		if i >= 100 {
			edge[i] = 144
		}
	}
	f := &sample.Frame{TriggerAt: 98, Status: sample.Status{Triggered: true}, Traces: []sample.Trace{{Codes8: flat, Valid: 200}, {Ch: 1, Valid: 200}}}
	if e.placeEdgeTrigger(f, flat, nil, 136, true) || f.TriggerAt != 98 || e.stats.FalseTriggers != 1 {
		t.Fatalf("a noise trigger: point %.1f, %d counted", f.TriggerAt, e.stats.FalseTriggers)
	}
	g := &sample.Frame{TriggerAt: 96, Status: sample.Status{Triggered: true}, Traces: []sample.Trace{{Codes8: edge, Valid: 200}, {Ch: 1, Valid: 200}}}
	if !e.placeEdgeTrigger(g, edge, nil, 136, true) || math.Abs(g.TriggerAt-99.5) > 0.01 || e.stats.FalseTriggers != 1 {
		t.Fatalf("a real edge: point %.2f, %d noise", g.TriggerAt, e.stats.FalseTriggers)
	}
	for range 30 {
		e.placeEdgeTrigger(&sample.Frame{TriggerAt: 98, Status: sample.Status{Triggered: true}, Traces: []sample.Trace{{Codes8: flat, Valid: 200}, {Ch: 1, Valid: 200}}}, flat, nil, 136, true)
	}
	if e.stats.NoiseTriggers < 0.25 {
		t.Fatalf("31 of 32 on noise: share %.2f", e.stats.NoiseTriggers)
	}
	e.SetTrigSlope(false)
	if e.stats.NoiseTriggers != 0 {
		t.Fatalf("a new trigger slope kept the share %.2f", e.stats.NoiseTriggers)
	}
}
