// ENGMODEL-OWNER-UNIT: FU-APP-APP

// Package app is the scope application's wiring: it brings the fabric up,
// starts the acquisition engine and serves the LCD, the front panel, the web
// UI and SCPI (Run), and is the acquisition worker the app starts again
// (Worker).
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/vxi11srv"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/buildinfo"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/cal"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/scpi"
	"github.com/labeth/open-sds1000cml/app/internal/settings"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
	"github.com/labeth/open-sds1000cml/app/internal/streamview"
	"github.com/labeth/open-sds1000cml/app/internal/webscope"
)

// TRLC-LINKS: REQ-SDS-167
func logf(format string, a ...any) {
	fmt.Printf("[app] "+format+"\n", a...)
}

// findInheritedFD scans /proc/self/fd for the descriptor whose link target is
// path; fds < 3 are skipped.
// TRLC-LINKS: REQ-SDS-002
func findInheritedFD(path string) int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < 3 {
			continue
		}
		if t, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && t == path {
			return fd
		}
	}
	return -1
}

// TRLC-LINKS: REQ-SDS-002, REQ-SDS-003, REQ-SDS-004, REQ-SDS-167
// appMemoryLimit is the Go runtime's soft memory limit on the device.
const appMemoryLimit = 48 << 20

// startAt is the app's start, for the start-up stages in the log.
var startAt = time.Now()

// startMark logs a start-up stage and the time since the start.
// TRLC-LINKS: REQ-SDS-001
func startMark(stage string) {
	logf("start: %s at %v", stage, time.Since(startAt).Round(time.Millisecond))
}

// Run is the app: it brings the fabric up, starts the acquisition engine and
// serves the LCD, the front panel, the web UI and SCPI until SIGTERM.
// TRLC-LINKS: REQ-SDS-167
func Run() {
	logf("start pid=%d version=%s", os.Getpid(), buildinfo.String())
	// The unit has 125 MB and the default collector lets the heap reach twice
	// the live set: after 1 M-sample stopped records and full-depth WF?/frame
	// transfers the app reaches 71 MB RSS with 25 MB left free.
	// A soft limit makes the collector work harder near it instead of growing.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(appMemoryLimit)
	}

	// The OTA agent starts the app and hands it its contract: the GPMC device
	// it opened (inherited, never opened here), the health token's path and
	// the stick's ota/ directory, where the settings live. The app runs only
	// under the agent.
	const listen = ":8080"
	gpmcDev, healthPath, otaDir := os.Getenv("SCOPE_GPMC"), os.Getenv("OTA_HEALTH_PATH"), os.Getenv("OTA_DIR")
	if gpmcDev == "" || healthPath == "" || otaDir == "" {
		logf("FATAL: SCOPE_GPMC, OTA_HEALTH_PATH and OTA_DIR come from the OTA agent (got %q, %q, %q)", gpmcDev, healthPath, otaDir)
		os.Exit(1)
	}

	gpmcFD := findInheritedFD(gpmcDev)
	keyFD := findInheritedFD("/dev/fpga_key")
	logf("inherited %s fd=%d  /dev/fpga_key fd=%d", gpmcDev, gpmcFD, keyFD)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)

	if gpmcFD < 0 {
		// Spec 01 §5.2: refuse to drive, never write the health token, stay
		// alive for diagnosis. No fresh open — that could wedge the driver.
		logf("FATAL: no inherited %s fd — refusing to drive (spec 01 §5.2)", gpmcDev)
		<-sig
		os.Exit(0)
	}

	b, err := bus.New(gpmcFD)
	if err != nil {
		logf("FATAL: bus init: %v — refusing to drive", err)
		<-sig
		os.Exit(0)
	}
	// Fabric deployment: load the general image, verify the SRAM transport,
	// then hand the bus to the acquisition worker. Any failure stops here:
	// no health token is written, so the agent rolls the app back.
	sramBackend, ok := bringUpFabric(b, gpmcFD)
	if !ok {
		return
	}

	var interleaveCal *engine.InterleaveCalibration
	if c, err := engine.DefaultInterleaveCalibration(); err != nil {
		logf("interleave calibration: built-in default rejected: %v", err)
	} else {
		interleaveCal = c
	}
	e := engine.New(engine.Config{Bus: b, Logf: logf, SRAM: sramBackend, InterleaveCalibration: interleaveCal})
	e.SetImageSwitcher(imageSwitcher{fd: gpmcFD})

	go healthLoop(e, healthPath)

	// Per-unit calibration (spec 10): file → backup → compiled defaults.
	calTab := cal.Load(logf)
	logf("cal source: %s (C1@1V zero=%d gainDAC=%d)", calTab.Source,
		calTab.Rec[0][8].Zero, calTab.Rec[0][8].GainDAC)

	// The vertical front end is off the GPMC bus (SPI). Optional: without it
	// the scope still runs on the inherited boot range.
	fe := startFrontEnd(e, calTab)
	var feIface webscope.Analog
	if fe != nil {
		feIface = fe
	}

	if fe != nil {
		for ch := range 2 {
			if err := fe.SetVdiv(ch, 8); err != nil {
				logf("SRAM: initial range: %v", err)
				return
			}
		}
	}
	go e.Run()
	// The fan-out is the arena's single consumer; the web UI and the LCD
	// renderer read its snapshot under the fan-out lock.
	fo := fanout.NewFrames()
	stopFo := make(chan struct{})
	go fo.Run(e, stopFo)

	go runLCD(e, fe, fo)

	// Front-panel controller: SIGIO off the inherited /dev/fpga_key fd,
	// matrix reads via the engine's request/reply channel.
	var pfe panel.Analog
	var bfe board.FrontEnd
	if fe != nil {
		pfe, bfe = fe, fe
	}
	// The trigger level and the offsets in volts over the DACs the engine
	// stages: the controllers' side of the board's levels.
	levels := board.New(e, bfe)
	pc := panel.New(e, pfe, levels, keyFD, engine.SupportedTdivs(), 500e-6, logf)
	pc.SetFrameSource(fo.WithFrame) // let the AUTO button measure the live signal
	// Decoded stream (ADR-PANEL-DECODED-STREAM): the panel starts it from the
	// BUS sheet's decoder settings (typed in, or filled in by Auto).
	sv := streamview.New(e, logf)
	streamCtrl.Store(sv)
	pc.SetDecodeStream(sv)
	uiCtrl.Store(pc) // publish for the LCD render loop + SCDP screenshot
	go pc.Run(stopFo)
	logf("panel controller up (fpga_key fd=%d)", keyFD)
	startMark("panel up")

	// Device-screen PNG for the web /api/screen.png endpoint: the exact LCD render.
	screenPNG := func() []byte { return lcd.EncodePNG(renderScreen(e, fe, fo)) }
	// SCPI over VXI-11 (spec 11): the host instrument-control interface.
	// SCDP renders a fresh headless frame — works with or without the LCD.
	// Created BEFORE the web server so its INVS shadow (the display-invert
	// truth) can be wired into the status snapshot; the panel controller
	// backs the SCPI display commands (XYDS/PESU/MENU) with the real state.
	var sfe scpi.Analog
	if fe != nil {
		sfe = fe
	}
	shot := func() []byte { return lcd.EncodeBMP(renderScreen(e, fe, fo)) }
	scpiH := scpi.New(scopeSource{e, fo}, sfe, levels, pc, shot, logf)
	scpiCtrl.Store(scpiH) // publish for the LCD render loop + screenshot paths
	pc.SetInverter(scpiH) // the CH tabs' Invert rows: the same INVS shadow
	// Settings persistence: restore the last user setup now that the engine is
	// up and the panel has its display invert (restored with the rest; a
	// restore run before it would lose the invert) — through the
	// SAME setter paths the panel/web use, so every clamp and side-effect
	// applies — then watch for changes and save them debounced to the U-disk.
	// A missing/corrupt file just means "boot with defaults".
	var setFE settings.Analog
	if fe != nil {
		setFE = fe
	}
	setExt := board.SettingsExtension{Levels: levels}
	setPath := settings.PathIn(otaDir)
	if st, ok := settings.Load(setPath, logf); ok {
		settings.Apply(st, e, setFE, pc, setExt, logf)
		logf("settings: restored %s", setPath)
	} else {
		logf("settings: no saved setup at %s — defaults", setPath)
	}
	saver := settings.NewSaver(setPath, func() settings.Settings {
		return settings.Collect(e, setFE, pc, setExt)
	}, logf)
	go saver.Run(stopFo)
	// *SAV/*RCL: numbered setups beside the persisted one, through the same
	// Collect/Apply paths as the boot restore.
	setupSlots := settings.Slots{
		Dir:     filepath.Dir(setPath),
		Collect: func() settings.Settings { return settings.Collect(e, setFE, pc, setExt) },
		Apply:   func(st settings.Settings) { settings.Apply(st, e, setFE, pc, setExt, logf) },
	}
	pc.SetSetupMemory(setupSlots) // SAVE/RECALL key: the same slots as *SAV/*RCL
	// SCPI and the web save and recall through the panel, so its FILE rows and
	// SAVE/RECALL lamp follow them.
	sharedSetups := pc.SharedSetups()
	scpiH.SetSetupMemory(sharedSetups)
	pc.SetHardcopy(func() (string, error) {
		if !mountedAt(uDisk) { // the bare directory would be the internal flash
			return "", fmt.Errorf("no U-disk mounted at %s", uDisk)
		}
		return saveHardcopy(uDisk, screenPNG())
	})
	if _, port, err := vxi11srv.Start(scpiH.HandleLine, true, logf); err != nil {
		logf("WARNING: VXI-11 server failed: %v", err)
	} else {
		logf("scpi: VXI-11 DEVICE_CORE on tcp/%d", port)
	}

	// Deliberately NO Read/Write timeouts (see newWebServer): /api/frame.bin
	// parks requests up to 2 s (long-poll), and a global WriteTimeout would
	// kill every parked request. Slow-peer writes are bounded per-response
	// inside the handler (SetWriteDeadline). Don't "harden" this without
	// moving that contract.
	sc := webscope.New(scopeSource{e, fo}, feIface, levels, pc, screenPNG)
	sc.SetInvertSource(scpiH.Inverted)         // display-level INVS: SCPI shadow → /api/status
	sc.SetSetupMemory(webSetups{sharedSetups}) // the web top bar's Setups: the same slots as *SAV/*RCL
	ws := webserver.New(sc, webserver.WithPprof())
	webSrv.Store(ws)
	srv := newWebServer(listen, ws.Handler())
	go func() {
		logf("web ui listening on %s", listen)
		startMark("web up")
		go webserver.Precompress()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf("web server: %v", err)
		}
	}()

	s := <-sig
	logf("signal %v — stopping engine at frame boundary", s)
	saver.Flush() // a change still inside the debounce window survives the restart
	// Never leave the panel dark behind the screen saver.
	if err := lcd.SetBacklight(true); err != nil {
		logf("backlight: %v", err)
	}
	if !e.Stop(2 * time.Second) {
		logf("WARNING: engine did not stop in time")
	}
	if err := b.CloseWorker(); err != nil {
		logf("acquisition worker shutdown: %v", err)
	}
	os.Exit(0)
}

// bringUpFabric loads the general image, checks the SRAM transport (fast
// GPMC timing where the image takes it) and starts the acquisition worker.
// false: the fabric is not fit to drive; the failure is logged.
// TRLC-LINKS: REQ-SDS-167
func bringUpFabric(b *bus.Dev, gpmcFD int) (*sramcapture.Capture, bool) {
	startMark("loading the general image")
	if err := loadGeneralImage(gpmcFD); err != nil {
		logf("FATAL: general image load: %v", err)
		return nil, false
	}
	startMark("general image loaded")
	sramBackend, err := sramcapture.New(b)
	if err != nil {
		logf("FATAL: SRAM capture: %v", err)
		return nil, false
	}
	m, err := sramBackend.Status()
	if err != nil || (m.Revision != 9 && m.Revision != 10 && m.Revision != 11) || !m.Locked {
		logf("FATAL: the general image must report revision 9, 10 or 11, status=%+v error=%v", m, err)
		return nil, false
	}
	if err := bus.EnsureDcinv(logf); err != nil {
		logf("SRAM: coherent DMA unavailable: %v", err)
	} else {
		b.EnableEDMA(8192, logf)
	}
	if m.Revision == 10 && b.FastDrain() {
		tp, err := bus.OpenTimingPort()
		if err != nil {
			logf("FATAL: SRAM timing port: %v", err)
			return nil, false
		}
		fast, err := applySRAMReadTiming(tp, func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return sramBackend.VerifyCounter(ctx)
		})
		tp.Close()
		if err != nil {
			logf("FATAL: SRAM startup transport check: %v", err)
			return nil, false
		}
		logf("SRAM: full counter verified, fast GPMC timing=%v", fast)
	}
	logf("bus up, fast drain=%v", b.FastDrain())
	startMark("bus up")
	executable, err := os.Executable()
	if err == nil {
		err = b.StartWorker(executable, logf)
	}
	if err != nil {
		logf("FATAL: acquisition worker: %v", err)
		return nil, false
	}
	return sramBackend, true
}

// startFrontEnd brings up the SPI vertical front end and wires it to the
// engine; nil when it is unavailable (logged).
// TRLC-LINKS: REQ-SDS-167
func startFrontEnd(e *engine.Engine, calTab *cal.Table) *analog.FrontEnd {
	dev, err := analog.NewDev()
	if err != nil {
		logf("WARNING: SPI front end unavailable (%v) — V/div control disabled", err)
		return nil
	}
	fe := analog.New(dev, nil, calTab)
	if t, err := analog.DefaultZeroTrim(); err != nil {
		logf("zero trim: built-in default rejected: %v", err)
	} else {
		fe.SetZeroTrim(t)
	}
	fe.OnOffset(e.SetOffsetDAC)       // offset re-anchors to each detent's cal zero
	fe.OnOffsetV(e.SetChannelOffsetV) // trigger level rides the same offset reference as the samples
	fe.OnVdiv(e.SetChannelVdiv)       // keep the trigger level→display-code map current
	fe.OnApply(e.NoteFrontEndWrite)   // roll drops data captured across a relay switch
	e.SetBodeScaleSource(func(ch int) float64 {
		idx, _ := fe.Snapshot()
		return analog.Vdiv(idx[ch&1]) * fe.ProbeFactor(ch)
	})
	fe.OnCoupling(e.SetChannelCoupling) // software AC: the trigger level follows the AC trace
	logf("SPI front end up (seeded to boot detent, not emitted)")
	return fe
}

// Worker is the acquisition worker, the app started again with
// --acquisition-worker: it owns the GPMC transfers (bus.RunAcquisitionWorker)
// and returns the process's exit code.
// TRLC-LINKS: REQ-SDS-001
func Worker() int {
	if err := bus.RunAcquisitionWorker(3, 4, logf); err != nil {
		logf("acquisition worker: %v", err)
		return 1
	}
	return 0
}
