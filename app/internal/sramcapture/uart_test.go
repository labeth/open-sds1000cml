// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type uartBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b uartBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 48 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareUARTCapabilityAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = uartBus{f, 0x5502}
	cfg := Config{Normal: true, PostWords: 16, UART: UARTTriggerConfig{Enabled: true, Channel: 1, Inverted: true, BitTicks: 2170, Pattern: 0x4869, Length: 2}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, want := range map[uint16]uint16{48: 23, 49: 2170, 50: 0, 51: 0x4869, 52: 0} {
		if f.staged[s] != want {
			t.Fatalf("register %d=%x, want %x", s, f.staged[s], want)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.UART = UARTTriggerConfig{}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[48] != 0 {
		t.Fatal("edge mode left UART enabled")
	}
	f.flags = 4 | 16 | 32 | 64
	c.bus = uartBus{f, 0}
	cfg.UART = UARTTriggerConfig{Enabled: true, BitTicks: 2170}
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported image accepted UART")
	}
	if f.writes != before {
		t.Fatal("unsupported request changed fabric")
	}
}
