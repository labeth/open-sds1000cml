// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"open-sds/app/internal/sramcapture"
	"strings"
	"testing"
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
	p := SerialParams{Proto: serUART, Baud: 50000000, Bits: 9, Parity: "odd", ChA: 1, Inverted: true, HaveThr: true, Threshold: 120, Bytes: []int{0x55, 0xaa}}
	if planHardwareUART(p, false).Enabled {
		t.Fatal("50 MBd 9O1 planned on the level receiver")
	}
	c := planHardwareUART(p, true)
	want := sramcapture.UARTTriggerConfig{Enabled: true, Channel: 1, Inverted: true, Period16: 320, Bits: 9, Parity: 2, Resync: true, Pattern: 0x55aa, Length: 2}
	if c != want {
		t.Fatalf("plan %+v, want %+v", c, want)
	}
	q := p
	q.Baud = 62642958 // as detection measured the bench's 62.5 MBd
	if c := planHardwareUART(q, true); c.Period16 != 256 {
		t.Fatalf("62.6 MBd: %+v", c)
	}
	for _, change := range []func(*SerialParams){
		func(p *SerialParams) { p.Baud = 70000000 },
		func(p *SerialParams) { p.Baud = 900 },
		func(p *SerialParams) { p.Bits = 4 },
		func(p *SerialParams) { p.Parity = "mark" },
		func(p *SerialParams) { p.Bits, p.Bytes = 7, []int{0x80} },
	} {
		q := p
		change(&q)
		if planHardwareUART(q, true).Enabled {
			t.Fatalf("planned %+v", q)
		}
	}
}

// A UART past the general receiver's reach, or in a format it does not
// take, loads the fast image; a standard 8N1 rate stays on the general one.
// TRLC-LINKS: REQ-SDS-229
func TestFastImageTakesFastUART(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	slow := SerialParams{Proto: serUART, Baud: 115200, ChA: 0, HaveThr: true, Threshold: 128, Bytes: []int{0x48}}
	e.SetSerialParams(slow)
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() || b.image != "general" {
		t.Fatalf("115200 Bd left the general image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.UART.Enabled || cfg.UART.BitTicks != 1085 || cfg.UART.Period16 != 0 {
		t.Fatalf("115200 Bd plan %+v", cfg.UART)
	}
	e.SetSerialParams(SerialParams{Proto: serUART, Baud: 62500000, ChA: 0, HaveThr: true, Threshold: 128, Bytes: []int{0x48}})
	if !e.serviceImage() || b.image != "fast" || !e.hardwareSampleUART {
		t.Fatalf("62.5 MBd: loads %v image %s", images.loads, b.image)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if !cfg.Normal || cfg.TriggerChannel != 0 || cfg.UART != (sramcapture.UARTTriggerConfig{Enabled: true, Period16: 256, Bits: 8, Resync: true, Pattern: 0x48, Length: 1}) {
		t.Fatalf("62.5 MBd plan %+v", cfg.UART)
	}
	if hardwareSerialName(cfg) != "uart" {
		t.Fatalf("backend %q", hardwareSerialName(cfg))
	}
	e.SetSerialParams(slow)
	if !e.serviceImage() || b.image != "general" {
		t.Fatalf("back to 115200 Bd: loads %v", images.loads)
	}
	seven := slow
	seven.Bits, seven.Parity = 7, "even"
	e.SetSerialParams(seven)
	if !e.serviceImage() || b.image != "fast" || strings.Join(images.loads, ",") != "fast,general,fast" {
		t.Fatalf("115200 7E1: loads %v", images.loads)
	}
}

// I2C stays on the general image to fast-mode plus and loads the fast
// image for high-speed mode (a measured or given clock), or when asked
// for the finest timing.
// TRLC-LINKS: REQ-SDS-229
func TestFastImageTakesFastI2C(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	p := SerialParams{Proto: serI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 2, HaveThr: true, Threshold: 128, I2CClockHz: 1000000}
	e.SetSerialParams(p)
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() || b.image != "general" {
		t.Fatalf("1 MHz left the general image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.I2C.Enabled || cfg.I2C.Samples {
		t.Fatalf("1 MHz plan %+v", cfg.I2C)
	}
	p.I2CClockHz = 3400000
	e.SetSerialParams(p)
	if !e.serviceImage() || b.image != "fast" {
		t.Fatalf("3.4 MHz: loads %v", images.loads)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if cfg.I2C != (sramcapture.I2CTriggerConfig{Enabled: true, Address: 0x50, Direction: 2, Samples: true, Delay: -23}) {
		t.Fatalf("3.4 MHz plan %+v", cfg.I2C)
	}
	p.I2CClockHz, p.Fine = 100000, true
	e.SetSerialParams(p)
	if e.serviceImage() || b.image != "fast" {
		t.Fatalf("100 kHz with the finest timing: loads %v", images.loads)
	}
	p.Fine = false
	e.SetSerialParams(p)
	if !e.serviceImage() || b.image != "general" {
		t.Fatalf("100 kHz: loads %v", images.loads)
	}
}
