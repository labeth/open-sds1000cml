package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/fpgaload"
	"open-sds/app/internal/sramcapture"
	"open-sds/app/internal/web"
)

// SRAM mode deliberately has no settings store, normal acquisition engine,
// analog reinitialization, or default-fabric loader. It inherits the front end
// and serves raw-code captures through the shared SRAM acquisition owner.
func runSRAMMode(b *bus.Dev, fd int, listen, healthPath string, signals <-chan os.Signal) error {
	if healthPath != "" && !sramHealthPathAllowed(healthPath) {
		return fmt.Errorf("health token must be /dev/acq-* or on U-disk0; refusing internal persistent storage path %q", healthPath)
	}
	capture, err := sramcapture.New(b)
	if err != nil {
		path := os.Getenv("SCOPE_SRAM_RBF")
		if path == "" {
			return fmt.Errorf("%w; preload the SRAM image or set SCOPE_SRAM_RBF to its owned RBF", err)
		}
		image, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		logf("SRAM: configuring volatile Cyclone image from %s", path)
		if e = fpgaload.ConfigureVolatile(fd, image, fpgaload.Options{BitOrder: fpgaload.BitOrderReverse, Force: true, Attempts: 1, Logf: logf}); e != nil {
			return e
		}
		capture, err = sramcapture.New(b)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if healthPath != "" && os.Getenv("SCOPE_DIAG_HEARTBEAT") != "0" {
		logf("SRAM: diagnostic heartbeat attests verified fabric and bus progress, not frame publication")
		go sramHealthLoop(ctx, capture, healthPath)
	}
	server := &http.Server{Addr: listen, Handler: web.SRAMHandler(capture), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	failures := make(chan error, 1)
	go func() { failures <- server.ListenAndServe() }()
	logf("SRAM: full-depth capture mode listening on %s; map=%04x, capacity=%d bytes", listen, sramcapture.QualifiedMapID, sramcapture.Bytes)
	select {
	case <-signals:
	case err = <-failures:
		if err != http.ErrServerClosed {
			return err
		}
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = server.Shutdown(stopCtx)
	_ = capture.Halt(stopCtx)
	return nil
}

func sramHealthPathAllowed(path string) bool {
	path = filepath.Clean(path)
	return (filepath.Dir(path) == "/dev" && strings.HasPrefix(filepath.Base(path), "acq-")) || strings.HasPrefix(path, "/usr/bin/siglent/usr/media/U-disk0/")
}

func sramHealthLoop(ctx context.Context, capture *sramcapture.Capture, path string) {
	// An idle status poll verifies clock/map health. During a long recall the
	// owner remains busy, so successful bus-operation beats keep health live.
	var qualified atomic.Bool
	go func() {
		timer := time.NewTicker(500 * time.Millisecond)
		defer timer.Stop()
		for {
			m, e := capture.Status()
			qualified.Store(e == nil && m.Locked && m.MapID == sramcapture.QualifiedMapID)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	timer := time.NewTicker(400 * time.Millisecond)
	defer timer.Stop()
	var previous uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		beats := capture.Beats()
		if !qualified.Load() || beats == previous {
			continue
		}
		token := []byte(fmt.Sprintf("mode=diag backend=sram beats=%d ts=%d\n", beats, time.Now().UnixNano()))
		if e := os.WriteFile(path+".tmp", token, 0o644); e == nil {
			if e = os.Rename(path+".tmp", path); e == nil {
				previous = beats
			}
		}
	}
}
