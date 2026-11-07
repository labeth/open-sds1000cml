// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
)

// TRLC-LINKS: REQ-SDS-013
func TestPlanHardwareSPI(t *testing.T) {
	p := core.SerialParams{Proto: core.SerSPI, HaveThr: true, Threshold: 128, SPIClockHz: 200000, ChA: 1, ChB: 0, MSB: true, CPOL: true, CPHA: true, Inverted: true, Bytes: []int{0x48, 0xa5}}
	c := PlanHardwareSPI(&p, false)
	if !c.Enabled || c.GapTicks != 938 || c.Pattern != 0x48a5 || c.Length != 2 || c.ClockChannel != 1 || !c.CPOL || !c.CPHA || !c.MSB || !c.Inverted {
		t.Fatalf("plan: %+v", c)
	}
	p.SPIClockHz = 0
	if PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("inferred clock advertised as hardware")
	}
	p.SPIClockHz = 200000
	p.ChB = 1
	if PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("one channel used twice")
	}
	p.ChB = 0
	p.Bytes = []int{256}
	if PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("wide value truncated")
	}
}

// The receiver on the samples plans to a 125 MHz clock with a gap of whole
// receiver clocks past one period and the data line's delay by the channels'
// sampling phases.
// TRLC-LINKS: REQ-SDS-229
func TestPlanHardwareSampleSPI(t *testing.T) {
	p := core.SerialParams{Proto: core.SerSPI, HaveThr: true, Threshold: 128, SPIClockHz: 104000000, ChA: 1, ChB: 0, MSB: true, Bytes: []int{0x4f, 0x50}}
	if PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("104 MHz planned on the level receiver")
	}
	c := PlanHardwareSPI(&p, true)
	if !c.Enabled || c.GapTicks != 4 || c.Delay != 23 || c.Pattern != 0x4f50 || c.Length != 2 || c.ClockChannel != 1 {
		t.Fatalf("plan: %+v", c)
	}
	p.ChA, p.ChB, p.SPIClockHz = 0, 1, 200000
	if c = PlanHardwareSPI(&p, true); !c.Enabled || c.GapTicks != 938 || c.Delay != -23 {
		t.Fatalf("plan: %+v", c)
	}
	p.SPIClockHz = 130000000
	if PlanHardwareSPI(&p, true).Enabled {
		t.Fatal("a clock past the receiver clock planned")
	}
}

// A data delay moves the receiver on the samples' read point past the
// channels' phase, within its register; the level receiver, reading at the
// edge to a tick of 8 ns, keeps every delay the row takes at its clocks
// (to about 20 MHz: half a period of 24 ns or more).
// TRLC-LINKS: REQ-SDS-229, REQ-SDS-230
func TestPlanHardwareSPIDelay(t *testing.T) {
	p := core.SerialParams{Proto: core.SerSPI, HaveThr: true, Threshold: 128, SPIClockHz: 104000000, ChA: 0, ChB: 1, MSB: true, SPIDelayNs: 7}
	if c := PlanHardwareSPI(&p, true); !c.Enabled || c.Delay != -23+112 {
		t.Fatalf("7 ns, clock on C1: %+v", c)
	}
	p.ChA, p.ChB = 1, 0
	if c := PlanHardwareSPI(&p, true); c.Delay != 127 || SPIDelayFPGA(7, 1) != float64(127-23)/16 {
		t.Fatalf("7 ns, clock on C2: delay %d, %.3f ns", c.Delay, SPIDelayFPGA(7, 1))
	}
	p.SPIClockHz = 1000000
	if !PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("7 ns at 1 MHz refused by the level receiver")
	}
	p.SPIClockHz, p.SPIDelayNs = 20000000, 9
	if !PlanHardwareSPI(&p, false).Enabled {
		t.Fatal("9 ns at 20 MHz refused by the level receiver")
	}
}
