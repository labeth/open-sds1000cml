// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type sentBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b sentBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 77 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func TestSENTArmDisableAndUnsupported(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = sentBus{f, 0x5e01}
	cfg := Config{Normal: true, PostWords: 16, SENT: SENTTriggerConfig{Enabled: true, Channel: 1, Inverted: true, PausePulse: true, TickTicks: 375, Nibbles: 8, Pattern: 0x050a, Length: 2}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{77: 47, 78: 375, 79: 0, 80: 0x050a, 81: 0, 82: 8} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.SENT.Enabled = false
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[77] != 0 {
		t.Fatal("SENT remained enabled for edge acquisition")
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.SENT.Enabled = true
	c.bus = sentBus{f, 0}
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported SENT accepted")
	}
	if f.writes != before {
		t.Fatal("unsupported SENT wrote registers")
	}
}

// TRLC-LINKS: REQ-SDS-013
func TestSENTRejectsInvalidBeforeWrites(t *testing.T) {
	valid := SENTTriggerConfig{Enabled: true, TickTicks: 375, Nibbles: 8, Pattern: 0x050a, Length: 2}
	for _, mutate := range []func(*Config){func(c *Config) { c.SENT.TickTicks = 3 }, func(c *Config) { c.SENT.Nibbles = 65 }, func(c *Config) { c.SENT.Pattern = 0x100a }, func(c *Config) { c.SPI.Enabled = true }, func(c *Config) { c.Normal = false }} {
		f := frozenBus()
		c := client(t, f)
		cfg := Config{Normal: true, PostWords: 16, SENT: valid}
		mutate(&cfg)
		if err := c.Arm(context.Background(), cfg); err == nil {
			t.Fatal("invalid SENT accepted")
		}
		if f.writes != 0 {
			t.Fatal("invalid config reached hardware")
		}
	}
}
