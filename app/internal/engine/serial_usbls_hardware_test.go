// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPlanHardwareUSBLS(t *testing.T) {
	p := SerialParams{Proto: serUSB, HaveThr: true, Threshold: 103, Baud: 1500000, ChA: 1, Inverted: true, Bytes: []int{0xff, 0x55, 0xaa, 0}}
	c := planHardwareUSBLS(p)
	if !c.Enabled || c.BitTicksQ8 != 21333 || c.Pattern != 0xff55aa00 || c.Length != 4 || c.Channel != 1 || !c.Inverted {
		t.Fatalf("plan %+v", c)
	}
	p.Baud = 12000000
	if c = planHardwareUSBLS(p); !c.Enabled || c.BitTicksQ8 != 2667 {
		t.Fatalf("FS plan %+v", c)
	}
	for _, mutate := range []func(*SerialParams){func(p *SerialParams) { p.Baud = 0 }, func(p *SerialParams) { p.Baud = 20000000 }, func(p *SerialParams) { p.HaveThr = false }, func(p *SerialParams) { p.Bytes = []int{256} }, func(p *SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} }, func(p *SerialParams) { p.ChA = 2 }} {
		q := p
		mutate(&q)
		if planHardwareUSBLS(q).Enabled {
			t.Fatalf("invalid plan %+v", q)
		}
	}
	p.Bytes = nil
	if c = planHardwareUSBLS(p); !c.Enabled || c.Length != 0 {
		t.Fatalf("any packet %+v", c)
	}
}
