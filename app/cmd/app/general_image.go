// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"open-sds/app/internal/fpgaload"
	"open-sds/app/internal/lcd"
)

// loadGeneralImage loads the release image before any SRAM register access.
// Development binaries rely on an explicitly preloaded image, verified by the
// caller. Releases reload their embedded image so an older compatible register
// signature cannot silently keep stale logic running.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-021
func loadGeneralImage(fd int) error {
	rbf := fpgaload.General()
	if len(rbf) == 0 {
		return nil
	}
	show := func(stage string, sent, total int) {}
	if fb, err := lcd.OpenFB(); err == nil {
		defer fb.Close()
		surface := lcd.NewMemSurface()
		show = func(stage string, sent, total int) { lcd.DrawLoading(surface, stage, sent, total); fb.Present(surface) }
	} else {
		logf("loading display unavailable: %v", err)
	}
	show("Preparing firmware", 0, 0)
	err := fpgaload.ConfigureVolatile(fd, rbf, fpgaload.Options{BitOrder: fpgaload.BitOrderReverse, Force: true, Attempts: 1, Logf: logf, Progress: func(sent, total int) {
		stage := "Transferring firmware"
		if sent == total {
			stage = "Verifying firmware"
		}
		show(stage, sent, total)
	}})
	if err != nil {
		show("Firmware loading failed", 0, 0)
		return err
	}
	show("Starting acquisition", len(rbf), len(rbf))
	return nil
}

// imageSwitcher reloads the acquisition FPGA for FPGA stacking or packet
// protocol triggering without LCD takeover; the engine owner calls it between
// acquisitions only.
// TRLC-LINKS: REQ-SDS-005, REQ-SDS-141
type imageSwitcher struct{ fd int }

// TRLC-LINKS: REQ-SDS-005, REQ-SDS-141
func (s imageSwitcher) load(rbf []byte) error {
	return fpgaload.ConfigureVolatile(s.fd, rbf, fpgaload.Options{BitOrder: fpgaload.BitOrderReverse, Force: true, Attempts: 1, Logf: logf})
}

// TRLC-LINKS: REQ-SDS-141
func (s imageSwitcher) LoadStack() error { return s.load(fpgaload.Stack()) }

// TRLC-LINKS: REQ-SDS-005
func (s imageSwitcher) LoadGeneral() error { return s.load(fpgaload.General()) }

// TRLC-LINKS: REQ-SDS-013
func (s imageSwitcher) LoadPacket() error { return s.load(fpgaload.Packet()) }

// TRLC-LINKS: REQ-SDS-141
func (s imageSwitcher) HasStack() bool { return len(fpgaload.Stack()) != 0 }

// TRLC-LINKS: REQ-SDS-013
func (s imageSwitcher) HasPacket() bool { return len(fpgaload.Packet()) != 0 }

// TRLC-LINKS: REQ-SDS-013
func (s imageSwitcher) LoadLine() error { return s.load(fpgaload.Line()) }

// TRLC-LINKS: REQ-SDS-013
func (s imageSwitcher) HasLine() bool { return len(fpgaload.Line()) != 0 }
