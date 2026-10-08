// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPlanHardwareMIL1553(t *testing.T) {
	p := SerialParams{Proto: serMIL1553, HaveThr: true, Threshold: 103, Baud: 1000000, ChA: 1, Inverted: true, Bytes: []int{0xa55a}}
	c := planHardwareMIL1553(p)
	if !c.Enabled || c.BitTicks != 125 || c.Pattern != 0xa55a || c.MatchAny || c.Channel != 1 || !c.Inverted {
		t.Fatalf("plan %+v", c)
	}
	for _, mutate := range []func(*SerialParams){func(p *SerialParams) { p.Baud = 0 }, func(p *SerialParams) { p.Baud = 20000000 }, func(p *SerialParams) { p.HaveThr = false }, func(p *SerialParams) { p.Bytes = []int{0x10000} }, func(p *SerialParams) { p.Bytes = []int{1, 2} }, func(p *SerialParams) { p.ChA = 2 }} {
		q := p
		mutate(&q)
		if planHardwareMIL1553(q).Enabled {
			t.Fatalf("invalid plan %+v", q)
		}
	}
	p.Bytes = nil
	c = planHardwareMIL1553(p)
	if !c.Enabled || !c.MatchAny {
		t.Fatalf("any word %+v", c)
	}
}
