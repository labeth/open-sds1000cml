// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type spiBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b spiBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 64 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareSPIProgrammingAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = spiBus{f, 0x5301}
	cfg := Config{Normal: true, PostWords: 16, SPI: SPITriggerConfig{Enabled: true, ClockChannel: 1, Inverted: true, CPOL: true, CPHA: true, MSB: true, GapTicks: 0x12345, Pattern: 0x4869, Length: 2}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{64: 191, 65: 0x2345, 66: 1, 67: 0x4869, 68: 0} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.SPI.Enabled = false
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[64] != 0 {
		t.Fatal("SPI remained enabled")
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.SPI.Enabled = true
	c.bus = spiBus{f, 0}
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported SPI accepted")
	}
	if f.writes != before {
		t.Fatal("unsupported SPI wrote registers")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareSPIRejectsInvalidConfiguration(t *testing.T) {
	for _, p := range []SPITriggerConfig{{Enabled: true, GapTicks: 2}, {Enabled: true, GapTicks: 1 << 24}, {Enabled: true, GapTicks: 100, Length: 5}, {Enabled: true, GapTicks: 100, ClockChannel: 2}} {
		f := frozenBus()
		c := client(t, f)
		before := f.writes
		if err := c.Arm(context.Background(), Config{Normal: true, PostWords: 16, SPI: p}); err == nil {
			t.Fatal("invalid SPI accepted")
		}
		if f.writes != before {
			t.Fatal("invalid SPI wrote registers")
		}
	}
}
