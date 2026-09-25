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
