// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type usbBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b usbBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 97 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestUSBLSArmAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = usbBus{f, 0x5501}
	cfg := Config{Normal: true, PostWords: 16, USBLS: USBLSTriggerConfig{Enabled: true, Channel: 1, Inverted: true, BitTicksQ8: 0x12345, Pattern: 0xff55aa00, Length: 4}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{97: 39, 98: 0x2345, 99: 1, 100: 0xaa00, 101: 0xff55} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.USBLS.Enabled = false
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[97] != 0 {
		t.Fatal("USB remained enabled")
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.USBLS.Enabled = true
	c.bus = usbBus{f, 0}
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported USB accepted")
	}
	if f.writes != before {
		t.Fatal("unsupported USB wrote registers")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestUSBLSInvalidBeforeWrites(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) { c.USBLS.BitTicksQ8 = 2047 }, func(c *Config) { c.USBLS.BitTicksQ8 = 0x1000000 }, func(c *Config) { c.USBLS.Channel = 2 }, func(c *Config) { c.USBLS.Length = 5 }, func(c *Config) { c.Normal = false }, func(c *Config) { c.MIL1553.Enabled = true }} {
		f := frozenBus()
		c := client(t, f)
		cfg := Config{Normal: true, PostWords: 16, USBLS: USBLSTriggerConfig{Enabled: true, BitTicksQ8: 21333}}
		mutate(&cfg)
		if err := c.Arm(context.Background(), cfg); err == nil {
			t.Fatal("invalid USB accepted")
		}
		if f.writes != 0 {
			t.Fatal("invalid USB reached hardware")
		}
	}
}
