// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The general image's UART receiver reads 8N1 where whole-tick bit times
// and a transmitter's 2 % keep the stop bit's sample inside it: every
// standard rate to 5 MBd, not 8 MBd's 15.6 ticks or anything faster.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
func TestGeneralUARTReach(t *testing.T) {
	for _, baud := range []float64{300, 9600, 115200, 921600, 1e6, 2e6, 4e6, 5e6} {
		if !generalUARTReach(baud, 10) {
			t.Fatalf("%.0f Bd out of the general receiver's reach", baud)
		}
	}
	for _, baud := range []float64{8e6, 10e6, 12e6, 25e6, 31.25e6, 50e6, 62.5e6} {
		if generalUARTReach(baud, 10) {
			t.Fatalf("%.0f Bd within the general receiver's reach", baud)
		}
	}
}

// The receiver on the samples plans every data width and parity the
// decoder reads, a bit time in sixteenths of a nanosecond, to 62.5 MBd.
// TRLC-LINKS: REQ-SDS-229
func TestPlanHardwareSampleUART(t *testing.T) {
	p := core.SerialParams{Proto: core.SerUART, Baud: 50000000, Bits: 9, Parity: "odd", ChA: 1, Inverted: true, HaveThr: true, Threshold: 120, Bytes: []int{0x55, 0xaa}}
	if PlanHardwareUART(&p, false).Enabled {
		t.Fatal("50 MBd 9O1 planned on the level receiver")
	}
	c := PlanHardwareUART(&p, true)
	want := sramcapture.UARTTriggerConfig{Enabled: true, Channel: 1, Inverted: true, Period16: 320, Bits: 9, Parity: 2, Resync: true, Pattern: 0x55aa, Length: 2}
	if c != want {
		t.Fatalf("plan %+v, want %+v", c, want)
	}
	q := p
	q.Baud = 62642958 // as detection measured the bench's 62.5 MBd
	if c := PlanHardwareUART(&q, true); c.Period16 != 256 {
		t.Fatalf("62.6 MBd: %+v", c)
	}
	for _, change := range []func(*core.SerialParams){
		func(p *core.SerialParams) { p.Baud = 70000000 },
		func(p *core.SerialParams) { p.Baud = 900 },
		func(p *core.SerialParams) { p.Bits = 4 },
		func(p *core.SerialParams) { p.Parity = "mark" },
		func(p *core.SerialParams) { p.Bits, p.Bytes = 7, []int{0x80} },
	} {
		q := p
		change(&q)
		if PlanHardwareUART(&q, true).Enabled {
			t.Fatalf("planned %+v", q)
		}
	}
}
