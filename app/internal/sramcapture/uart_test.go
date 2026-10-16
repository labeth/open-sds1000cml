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

// The receiver on the samples takes the bit time in sixteenths of a
// nanosecond, the data bits, parity and resync; each receiver refuses the
// other's configuration without writing its block.
// TRLC-LINKS: REQ-SDS-229
func TestHardwareSampleUARTProgramming(t *testing.T) {
	f := frozenBus()
	f.revision = 10
	c := client(t, f)
	c.bus = uartBus{f, 0x5503}
	if ok, err := c.SupportsSampleUART(); !ok || err != nil {
		t.Fatalf("sample UART %v %v", ok, err)
	}
	cfg := Config{Normal: true, PostWords: 16, UART: UARTTriggerConfig{Enabled: true, Channel: 1, Period16: 320, Bits: 9, Parity: 2, Resync: true, Pattern: 0x55, Length: 1}}
	if err := c.Arm(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	for s, want := range map[uint16]uint16{48: 1 | 2 | 1<<3 | 9<<6 | 2<<10 | 1<<12, 49: 320, 50: 0, 51: 0x55, 52: 0} {
		if f.staged[s] != want {
			t.Fatalf("register %d=%x, want %x", s, f.staged[s], want)
		}
	}
	for _, bad := range []UARTTriggerConfig{{Enabled: true, Period16: 255}, {Enabled: true, Period16: 1 << 24}, {Enabled: true, Period16: 320, Bits: 4},
		{Enabled: true, Period16: 320, Parity: 3}, {Enabled: true, BitTicks: 100, Parity: 1}} {
		f.flags = 4 | 16 | 32 | 64
		if err := c.Arm(context.Background(), Config{Normal: true, PostWords: 16, UART: bad}); err == nil {
			t.Fatalf("invalid %+v accepted", bad)
		}
	}
	f.flags = 4 | 16 | 32 | 64
	if err := c.Arm(context.Background(), Config{Normal: true, PostWords: 16, UART: UARTTriggerConfig{Enabled: true, BitTicks: 2170}}); err == nil {
		t.Fatal("the receiver on the samples accepted a level receiver's configuration")
	}
	f.flags = 4 | 16 | 32 | 64
	c.bus = uartBus{f, 0x5502}
	if err := c.Arm(context.Background(), cfg); err == nil {
		t.Fatal("the level receiver accepted a receiver on the samples' configuration")
	}
}
