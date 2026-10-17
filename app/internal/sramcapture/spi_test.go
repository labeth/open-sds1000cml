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

// The receiver on the samples takes the data line's delay in register 58
// (57 is the decoded-event stream's enable); the level receiver has no such
// register and is not written there.
// TRLC-LINKS: REQ-SDS-229
func TestHardwareSampleSPIDelay(t *testing.T) {
	for _, capability := range []uint16{0x5303, 0x5301} {
		f := frozenBus()
		f.revision = 10
		c := client(t, f)
		c.bus = spiBus{f, capability}
		if ok, err := c.SupportsSampleSPI(); err != nil || ok != (capability == 0x5303) {
			t.Fatalf("capability %x: sample receiver %v %v", capability, ok, err)
		}
		cfg := Config{Normal: true, PostWords: 16, SPI: SPITriggerConfig{Enabled: true, ClockChannel: 1, MSB: true, GapTicks: 4, Pattern: 0x4f50, Length: 2, Delay: -23}}
		if err := c.Arm(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
		if v, ok := f.staged[58]; ok != (capability == 0x5303) || (ok && v != 0xe9) {
			t.Fatalf("capability %x: register 58 = %x (%v)", capability, v, ok)
		}
	}
}

// The second line's level goes to register 59 with SPI or I2C armed; 0
// takes the trigger level.
// TRLC-LINKS: REQ-SDS-013
func TestSecondLineLevelRegister(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = spiBus{f, 0x5301}
	cfg := Config{Normal: true, PostWords: 16, TriggerLevel: 150, SecondLevel: 90, SPI: SPITriggerConfig{Enabled: true, GapTicks: 100}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[7]&0xff != 150 || f.staged[regSecondLevel] != 90 {
		t.Fatalf("levels %d, %d", f.staged[7]&0xff, f.staged[regSecondLevel])
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.SecondLevel = 0
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[regSecondLevel] != 150 {
		t.Fatalf("second level %d, want the trigger level", f.staged[regSecondLevel])
	}
}
