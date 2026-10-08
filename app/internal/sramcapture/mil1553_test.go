// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type milBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b milBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 93 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
func TestMIL1553ArmAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = milBus{f, 0x4d01}
	cfg := Config{Normal: true, PostWords: 16, MIL1553: MIL1553TriggerConfig{Enabled: true, Channel: 1, Inverted: true, BitTicks: 0x12345, Pattern: 0xa55a}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{93: 7, 94: 0x2345, 95: 1, 96: 0xa55a} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.MIL1553.Enabled = false
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[93] != 0 {
		t.Fatal("MIL remained enabled")
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.MIL1553.Enabled = true
	c.bus = milBus{f, 0}
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported MIL accepted")
	}
	if f.writes != before {
		t.Fatal("unsupported MIL wrote registers")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestMIL1553InvalidBeforeWrites(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) { c.MIL1553.BitTicks = 7 }, func(c *Config) { c.MIL1553.BitTicks = 0x400000 }, func(c *Config) { c.MIL1553.Channel = 2 }, func(c *Config) { c.Normal = false }, func(c *Config) { c.SENT.Enabled = true }} {
		f := frozenBus()
		c := client(t, f)
		cfg := Config{Normal: true, PostWords: 16, MIL1553: MIL1553TriggerConfig{Enabled: true, BitTicks: 125}}
		mutate(&cfg)
		if err := c.Arm(context.Background(), cfg); err == nil {
			t.Fatal("invalid MIL accepted")
		}
		if f.writes != 0 {
			t.Fatal("invalid MIL reached hardware")
		}
	}
}
