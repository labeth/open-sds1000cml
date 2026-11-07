// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
)

// TRLC-LINKS: REQ-SDS-013
func TestHardwareI2CPlanning(t *testing.T) {
	p := core.SerialParams{Proto: core.SerI2C, ChA: 1, ChB: 0, HaveThr: true, Threshold: 103, Inverted: true, Addr: 0x24, RW: 0, Bytes: []int{0x55, 0xaa}}
	c := PlanHardwareI2C(&p, false)
	if !c.Enabled || c.ClockChannel != 1 || c.Address != 0x24 || c.Pattern != 0x55aa || c.Length != 2 || !c.Inverted {
		t.Fatalf("bad plan: %+v", c)
	}
	for _, change := range []func(*core.SerialParams){
		func(p *core.SerialParams) { p.ChB = p.ChA }, func(p *core.SerialParams) { p.HaveThr = false },
		func(p *core.SerialParams) { p.Addr = 128 }, func(p *core.SerialParams) { p.RW = 3 },
		func(p *core.SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} },
	} {
		q := p
		change(&q)
		if PlanHardwareI2C(&q, false).Enabled {
			t.Fatalf("unsupported plan: %+v", q)
		}
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareUARTPlanning(t *testing.T) {
	p := core.SerialParams{Proto: core.SerUART, Baud: 115200, ChA: 1, Inverted: true, HaveThr: true, Threshold: 103, Bytes: []int{0x48, 0x69}}
	c := PlanHardwareUART(&p, false)
	if !c.Enabled || c.BitTicks != 1085 || c.Pattern != 0x4869 || c.Length != 2 || c.Channel != 1 || !c.Inverted {
		t.Fatalf("wrong hardware plan: %+v", c)
	}
	for _, change := range []func(*core.SerialParams){
		func(p *core.SerialParams) { p.HaveThr = false },
		func(p *core.SerialParams) { p.Baud = 0 },
		func(p *core.SerialParams) { p.Baud = 1 },
		func(p *core.SerialParams) { p.Bits = 7 },
		func(p *core.SerialParams) { p.Parity = "even" },
		func(p *core.SerialParams) { p.Proto = core.SerI2C },
		func(p *core.SerialParams) { p.Bytes = []int{1, 2, 3, 4, 5} },
		func(p *core.SerialParams) { p.Bytes = []int{256} },
		func(p *core.SerialParams) { p.HaveThr = true; p.Threshold = -1 },
	} {
		q := p
		change(&q)
		if PlanHardwareUART(&q, false).Enabled {
			t.Fatalf("unsupported config did not fall back: %+v", q)
		}
	}
}
