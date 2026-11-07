// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
)

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestPlanHardwareUSBLS(t *testing.T) {
	p := core.SerialParams{Proto: core.SerUSB, HaveThr: true, Threshold: 103, Baud: 1500000, ChA: 1, Inverted: true, Bytes: []int{0xff, 0x55, 0xaa, 0}}
	c := PlanHardwareUSBLS(&p)
	if !c.Enabled || c.BitTicksQ8 != 21333 || c.Pattern != 0xff55aa00 || c.Length != 4 || c.Channel != 1 || !c.Inverted {
		t.Fatalf("plan %+v", c)
	}
	p.Baud = 12000000
	if c = PlanHardwareUSBLS(&p); !c.Enabled || c.BitTicksQ8 != 2667 {
		t.Fatalf("FS plan %+v", c)
	}
	for _, mutate := range []func(*core.SerialParams){func(p *core.SerialParams) { p.Baud = 0 }, func(p *core.SerialParams) { p.Baud = 20000000 }, func(p *core.SerialParams) { p.HaveThr = false }, func(p *core.SerialParams) { p.Bytes = []int{256} }, func(p *core.SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} }, func(p *core.SerialParams) { p.ChA = 2 }} {
		q := p
		mutate(&q)
		if PlanHardwareUSBLS(&q).Enabled {
			t.Fatalf("invalid plan %+v", q)
		}
	}
	p.Bytes = nil
	if c = PlanHardwareUSBLS(&p); !c.Enabled || c.Length != 0 {
		t.Fatalf("any packet %+v", c)
	}
}
