// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
)

// TRLC-LINKS: REQ-SDS-013
func TestPlanHardwareSENT(t *testing.T) {
	p := SerialParams{Proto: serSENT, HaveThr: true, Threshold: 103, TickNs: 3000, ChA: 1, Inverted: true, Bytes: []int{5, 10}}
	c := planHardwareSENT(p)
	if !c.Enabled || c.TickTicks != 375 || c.Nibbles != 8 || c.Pattern != 0x050a || c.Channel != 1 || !c.Inverted {
		t.Fatalf("plan %+v", c)
	}
	for _, v := range []float64{0, math.NaN(), math.Inf(1), 24} {
		p.TickNs = v
		if planHardwareSENT(p).Enabled {
			t.Fatal("invalid/auto tick accepted")
		}
	}
	p.TickNs = 3000
	p.Bytes = []int{16}
	if planHardwareSENT(p).Enabled {
		t.Fatal("non-nibble accepted")
	}
}
