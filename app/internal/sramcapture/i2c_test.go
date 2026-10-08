// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

type i2cBus struct {
	*fakeBus
	capability uint16
}

// TRLC-LINKS: REQ-SDS-013
func (b i2cBus) Read(p uint8, s uint16) (uint16, error) {
	if p == 1 && s == 53 {
		return b.capability, nil
	}
	return b.fakeBus.Read(p, s)
}

// TRLC-LINKS: REQ-SDS-013
func TestHardwareI2CProgrammingAndDisable(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = i2cBus{f, 0x4901}
	cfg := Config{Normal: true, PostWords: 16, I2C: I2CTriggerConfig{Enabled: true, ClockChannel: 1, Inverted: true, Address: 0x24, Pattern: 0x55aa, Length: 2}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, v := range map[uint16]uint16{53: 135, 54: 0x24, 55: 0x55aa, 56: 0} {
		if f.staged[s] != v {
			t.Fatalf("register %d=%x want %x", s, f.staged[s], v)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	cfg.I2C.Enabled = false
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if f.staged[53] != 0 {
		t.Fatal("I2C remained enabled")
	}
	f.flags = 4 | 16 | 32 | 64
	c.bus = i2cBus{f, 0}
	cfg.I2C.Enabled = true
	before := f.writes
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("unsupported image accepted I2C")
	}
	if f.writes != before {
		t.Fatal("unsupported request wrote registers")
	}
}
