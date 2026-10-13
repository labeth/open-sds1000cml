package engine

import (
	"math"
	"testing"
)

// TRLC-LINKS: REQ-SDS-011
func TestRefineTriggerEdgeFindsExactCrossing(t *testing.T) {
	// A 30 ns ramp from code 87 to 169 sampled every 2 ns; the 128 crossing is
	// at a known fractional index whatever word the hardware reported.
	for _, shift := range []float64{0, 0.25, 0.5, 0.75} {
		n := 200
		sig := make([]uint8, n)
		q := make([]uint16, n)
		for i := range sig {
			x := float64(i) - 100 - shift
			v := 128 + 82*x/15
			v = math.Max(87, math.Min(169, v))
			sig[i] = uint8(math.Round(v))
			q[i] = uint16(math.Round(v * 256))
		}
		want := 100 + shift
		for _, hw := range []float64{98, 100, 102} {
			got := refineTriggerEdge(sig, q, n, hw, 128, true)
			if math.Abs(got-want) > 0.02 {
				t.Fatalf("shift %.2f hw %.0f: got %.3f want %.3f", shift, hw, got, want)
			}
		}
		if got := refineTriggerEdge(sig, q, n, 100, 128, false); got != 100 {
			t.Fatalf("falling search on a rising edge moved to %.3f", got)
		}
	}
	if got := refineTriggerEdge(nil, nil, 0, -1, 128, true); got != -1 {
		t.Fatalf("no record: %v", got)
	}
}

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
	f := &Frame{C1: flat, Valid: 200, EdgeX: 98, TrigPos: 98, Trigd: true}
	if e.placeEdgeTrigger(f, flat, nil, 136, true) || f.EdgeX != 98 || e.stats.FalseTriggers != 1 {
		t.Fatalf("a noise trigger: point %.1f, %d counted", f.EdgeX, e.stats.FalseTriggers)
	}
	g := &Frame{C1: edge, Valid: 200, EdgeX: 96, Trigd: true}
	if !e.placeEdgeTrigger(g, edge, nil, 136, true) || math.Abs(g.EdgeX-99.5) > 0.01 || e.stats.FalseTriggers != 1 {
		t.Fatalf("a real edge: point %.2f, %d noise", g.EdgeX, e.stats.FalseTriggers)
	}
	for i := 0; i < 30; i++ {
		e.placeEdgeTrigger(&Frame{C1: flat, Valid: 200, EdgeX: 98, Trigd: true}, flat, nil, 136, true)
	}
	if e.stats.NoiseTriggers < 0.25 {
		t.Fatalf("31 of 32 on noise: share %.2f", e.stats.NoiseTriggers)
	}
	e.SetTrigSlope(false)
	if e.stats.NoiseTriggers != 0 {
		t.Fatalf("a new trigger slope kept the share %.2f", e.stats.NoiseTriggers)
	}
}
