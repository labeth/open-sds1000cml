// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"testing"

	"open-sds/app/internal/decode"
	"open-sds/app/internal/panel/framecalc"
)

// An SPI bus BUS entry finds samples its data at the middle of the eye
// measured on it (Data delay Auto), as AUTO in BUS does: Default setup's
// fixed 0 ns sat on the 104 MHz flash's data edges (margin -3.6 ns) and every
// byte read one bit shifted ("85 80 08 00 7F" for "0B 00 10 00 FF"). A delay
// set by hand on the SPI bus in use stays.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-230
func TestFillInSPIMeasuresTheDelay(t *testing.T) {
	c, _, _ := newC(t)
	spi := decode.Result{OK: true, Proto: "spi", Bytes: []int{1, 2, 3, 4}, Roles: decode.Roles{ChA: 1, ChB: 0, MSB: true}}
	c.mu.Lock()
	c.decProto, c.decDelayNs, c.decDelayAuto = framecalc.ProtoOff, 0, false
	c.mu.Unlock()
	if !c.fillIn(spi) {
		t.Fatal("no bus filled in")
	}
	c.mu.Lock()
	auto := c.decDelayAuto
	c.decDelayNs, c.decDelayAuto = 2.5, false // the owner's hand setting
	c.mu.Unlock()
	if !auto {
		t.Fatal("a newly found SPI bus kept the fixed 0 ns data delay")
	}
	c.fillIn(spi)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.decDelayAuto || c.decDelayNs != 2.5 {
		t.Fatalf("the SPI bus in use lost its hand-set delay: auto %v, %g ns", c.decDelayAuto, c.decDelayNs)
	}
}
