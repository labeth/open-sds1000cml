// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// TRLC-LINKS: REQ-SDS-013
func TestPlanHardwareSPI(t *testing.T) {
	p := SerialParams{Proto: serSPI, HaveThr: true, Threshold: 128, SPIClockHz: 200000, ChA: 1, ChB: 0, MSB: true, CPOL: true, CPHA: true, Inverted: true, Bytes: []int{0x48, 0xa5}}
	c := planHardwareSPI(p)
	if !c.Enabled || c.GapTicks != 938 || c.Pattern != 0x48a5 || c.Length != 2 || c.ClockChannel != 1 || !c.CPOL || !c.CPHA || !c.MSB || !c.Inverted {
		t.Fatalf("plan: %+v", c)
	}
	p.SPIClockHz = 0
	if planHardwareSPI(p).Enabled {
		t.Fatal("inferred clock advertised as hardware")
	}
	p.SPIClockHz = 200000
	p.ChB = 1
	if planHardwareSPI(p).Enabled {
		t.Fatal("one channel used twice")
	}
	p.ChB = 0
	p.Bytes = []int{256}
	if planHardwareSPI(p).Enabled {
		t.Fatal("wide value truncated")
	}
}
