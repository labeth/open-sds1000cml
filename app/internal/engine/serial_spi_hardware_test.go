// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// TRLC-LINKS: REQ-SDS-013
func TestPlanHardwareSPI(t *testing.T) {
	p := SerialParams{Proto: serSPI, HaveThr: true, Threshold: 128, SPIClockHz: 200000, ChA: 1, ChB: 0, MSB: true, CPOL: true, CPHA: true, Inverted: true, Bytes: []int{0x48, 0xa5}}
	c := planHardwareSPI(p, false)
	if !c.Enabled || c.GapTicks != 938 || c.Pattern != 0x48a5 || c.Length != 2 || c.ClockChannel != 1 || !c.CPOL || !c.CPHA || !c.MSB || !c.Inverted {
		t.Fatalf("plan: %+v", c)
	}
	p.SPIClockHz = 0
	if planHardwareSPI(p, false).Enabled {
		t.Fatal("inferred clock advertised as hardware")
	}
	p.SPIClockHz = 200000
	p.ChB = 1
	if planHardwareSPI(p, false).Enabled {
		t.Fatal("one channel used twice")
	}
	p.ChB = 0
	p.Bytes = []int{256}
	if planHardwareSPI(p, false).Enabled {
		t.Fatal("wide value truncated")
	}
}

// The receiver on the samples plans to a 125 MHz clock with a gap of whole
// receiver clocks past one period and the data line's delay by the channels'
// sampling phases.
// TRLC-LINKS: REQ-SDS-229
func TestPlanHardwareSampleSPI(t *testing.T) {
	p := SerialParams{Proto: serSPI, HaveThr: true, Threshold: 128, SPIClockHz: 104000000, ChA: 1, ChB: 0, MSB: true, Bytes: []int{0x4f, 0x50}}
	if planHardwareSPI(p, false).Enabled {
		t.Fatal("104 MHz planned on the level receiver")
	}
	c := planHardwareSPI(p, true)
	if !c.Enabled || c.GapTicks != 4 || c.Delay != 23 || c.Pattern != 0x4f50 || c.Length != 2 || c.ClockChannel != 1 {
		t.Fatalf("plan: %+v", c)
	}
	p.ChA, p.ChB, p.SPIClockHz = 0, 1, 200000
	if c = planHardwareSPI(p, true); !c.Enabled || c.GapTicks != 938 || c.Delay != -23 {
		t.Fatalf("plan: %+v", c)
	}
	p.SPIClockHz = 130000000
	if planHardwareSPI(p, true).Enabled {
		t.Fatal("a clock past the receiver clock planned")
	}
}
