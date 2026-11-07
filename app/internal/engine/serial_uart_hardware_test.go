// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"strings"
	"testing"

	core "github.com/labeth/open-dso-core/engine"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// A UART past the general receiver's reach, or in a format it does not
// take, loads the fast image; a standard 8N1 rate stays on the general one.
// TRLC-LINKS: REQ-SDS-229
func TestFastImageTakesFastUART(t *testing.T) {
	e, b, images := newPacketTestEngine(t)
	slow := SerialParams{Proto: core.SerUART, Baud: 115200, ChA: 0, HaveThr: true, Threshold: 128, Bytes: []int{0x48}}
	e.SetSerialParams(slow)
	e.SetSerialMode(SerialTrigger)
	if e.serviceImage() || b.image != "general" {
		t.Fatalf("115200 Bd left the general image: %v", images.loads)
	}
	if cfg, _, _, _, _ := e.sramConfig(); !cfg.UART.Enabled || cfg.UART.BitTicks != 1085 || cfg.UART.Period16 != 0 {
		t.Fatalf("115200 Bd plan %+v", cfg.UART)
	}
	e.SetSerialParams(SerialParams{Proto: core.SerUART, Baud: 62500000, ChA: 0, HaveThr: true, Threshold: 128, Bytes: []int{0x48}})
	if !e.serviceImage() || b.image != "fast" || !e.hardwareSampleUART {
		t.Fatalf("62.5 MBd: loads %v image %s", images.loads, b.image)
	}
	cfg, _, _, _, _ := e.sramConfig()
	if !cfg.Normal || cfg.TriggerChannel != 0 || cfg.UART != (sramcapture.UARTTriggerConfig{Enabled: true, Period16: 256, Bits: 8, Resync: true, Pattern: 0x48, Length: 1}) {
		t.Fatalf("62.5 MBd plan %+v", cfg.UART)
	}
	if acq.HardwareSerialName(cfg) != "uart" {
		t.Fatalf("backend %q", acq.HardwareSerialName(cfg))
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
	p := SerialParams{Proto: core.SerI2C, ChA: 0, ChB: 1, Addr: 0x50, RW: 2, HaveThr: true, Threshold: 128, I2CClockHz: 1000000}
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
