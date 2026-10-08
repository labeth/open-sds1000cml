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
