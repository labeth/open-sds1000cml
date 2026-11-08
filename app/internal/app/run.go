// ENGMODEL-OWNER-UNIT: FU-APP-APP

// Package app is the scope application's wiring: it brings the fabric up,
// starts the acquisition engine and serves the LCD, the front panel, the web
// UI and SCPI (Run), and is the acquisition worker the app starts again
// (Worker).
package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/scopeapp"
	scpihost "github.com/labeth/open-dso-core/scpi/host"
	coresettings "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-dso-core/uimodel/panel"
	"github.com/labeth/open-dso-core/webserver"
	scope "github.com/labeth/open-sds1000cml/app"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/board"
	"github.com/labeth/open-sds1000cml/app/internal/board/acq"
	"github.com/labeth/open-sds1000cml/app/internal/buildinfo"
	"github.com/labeth/open-sds1000cml/app/internal/bus"
	"github.com/labeth/open-sds1000cml/app/internal/cal"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
	cmlpanel "github.com/labeth/open-sds1000cml/app/internal/panel"
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

// Run is the app: it brings the fabric up, builds the board and runs the
// core's scope application over it (scopeapp.Run) until SIGTERM, then
// returns the process's exit code.
// TRLC-LINKS: REQ-SDS-167
func Run() int {
	logf("start pid=%d version=%s", os.Getpid(), buildinfo.String())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	c, ok := newBoard(ctx)
	if !ok {
		return 0
	}
	if err := scopeapp.Run(ctx, c.acq, c.options()); err != nil {
		logf("FATAL: %v", err)
		return 1
	}
	return 0
}

// composition is the SDS1000CML+'s parts the core's application runs: the
// engine (its own owner until it is HAL terms throughout), the analog front
// end and its levels, the board, and the UI model, decoded stream and SCPI
// handler they wire to one another.
type composition struct {
	bus    *bus.Dev
	acq    *acq.Board
	e      *engine.Engine
	fe     *analog.FrontEnd // nil without SPI to the front end
	levels *board.Levels
	lcd    *lcd.Display // the panel; its framebuffer once the display loop opened it
	keyFD  int

	pc   *panel.Controller
	sv   *streamview.Controller
	scpi *scpi.Handler

	// lastViewWasBode tracks whether the device's BODE view armed FRA, so
	// leaving the view disarms only the device's own auto-arm (a web-armed
	// sweep persists).
	lastViewWasBode atomic.Bool
	lastStreamPage  atomic.Pointer[cachedStreamPage] // the HUD's cached transcript page
}

// newBoard brings the unit up to a board the core can run: the OTA agent's
// contract, the fabric and its worker, the engine, the calibration and the
// front end, the profile with the unit's identity, the hand-over and the
// devices. false: there is nothing to drive (logged; without the GPMC
// descriptor or a bus it waits for the signal, never writing the health
// token, so the agent sees why).
// TRLC-LINKS: REQ-SDS-167, REQ-SDS-002
func newBoard(ctx context.Context) (*composition, bool) {
	// The OTA agent starts the app and hands it its contract: the GPMC device
	// it opened (inherited, never opened here), the health token's path and
	// the stick's ota/ directory, where the settings live. The app runs only
	// under the agent.
	gpmcDev, healthPath, otaDir := os.Getenv("SCOPE_GPMC"), os.Getenv("OTA_HEALTH_PATH"), os.Getenv("OTA_DIR")
	if gpmcDev == "" || healthPath == "" || otaDir == "" {
		logf("FATAL: SCOPE_GPMC, OTA_HEALTH_PATH and OTA_DIR come from the OTA agent (got %q, %q, %q)", gpmcDev, healthPath, otaDir)
		os.Exit(1)
	}

	gpmcFD := findInheritedFD(gpmcDev)
	keyFD := findInheritedFD("/dev/fpga_key")
	logf("inherited %s fd=%d  /dev/fpga_key fd=%d", gpmcDev, gpmcFD, keyFD)

	if gpmcFD < 0 {
		// Spec 01 §5.2: refuse to drive, never write the health token, stay
		// alive for diagnosis. No fresh open — that could wedge the driver.
		logf("FATAL: no inherited %s fd — refusing to drive (spec 01 §5.2)", gpmcDev)
		<-ctx.Done()
		return nil, false
	}

	b, err := bus.New(gpmcFD)
	if err != nil {
		logf("FATAL: bus init: %v — refusing to drive", err)
		<-ctx.Done()
		return nil, false
	}
	// Fabric deployment: load the general image, verify the SRAM transport,
	// then hand the bus to the acquisition worker. Any failure stops here:
	// no health token is written, so the agent rolls the app back.
	sramBackend, ok := bringUpFabric(b, gpmcFD)
	if !ok {
		return nil, false
	}

	var interleaveCal *engine.InterleaveCalibration
	if c, err := engine.DefaultInterleaveCalibration(); err != nil {
		logf("interleave calibration: built-in default rejected: %v", err)
	} else {
		interleaveCal = c
	}
	// The acquisition board: the record memory and the panel plane behind
	// the HAL; the engine's owner loop drives it.
	var mem acq.Memory
	if sramBackend != nil { // a nil *Capture must leave the interface nil
		mem = sramBackend
	}
	acqBoard := acq.New(acq.Config{Memory: mem, Panel: b})
	e := engine.New(engine.Config{Bus: b, Logf: logf, Board: acqBoard, InterleaveCalibration: interleaveCal})
	e.SetImageSwitcher(imageSwitcher{fd: gpmcFD})

	// Per-unit calibration (spec 10): file → backup → compiled defaults.
	calTab := cal.Load(logf)
	logf("cal source: %s (C1@1V zero=%d gainDAC=%d)", calTab.Source,
		calTab.Rec[0][8].Zero, calTab.Rec[0][8].GainDAC)

	// The vertical front end is off the GPMC bus (SPI). Optional: without it
	// the scope still runs on the inherited boot range.
	fe := startFrontEnd(e, calTab)
	if fe != nil {
		for ch := range 2 {
			if err := fe.SetVdiv(ch, 8); err != nil {
				logf("SRAM: initial range: %v", err)
				return nil, false
			}
		}
	}
	// The trigger level and the offsets in volts over the DACs the engine
	// stages: the controllers' side of the board's levels.
	var bfe board.FrontEnd
	if fe != nil {
		bfe = fe
		acqBoard.SetFrontEnd(fe)
	}
	levels := board.New(e, bfe)
	acqBoard.SetLevelGrid(levels.TrigLevelGrid)
	attachProfile(acqBoard)
	acqBoard.SetHandover(hal.Handover{
		FDs:       map[string]int{"gpmc": gpmcFD, "fpga_key": keyFD},
		HealthTok: healthPath, StateDir: otaDir,
	})
	// The front panel: the key matrix on SIGIO off the inherited
	// /dev/fpga_key fd through the engine, the lamp latch. The settings and
	// setups live in the stick's ota/ directory.
	panelLCD := lcd.NewDisplay(acqBoard.Profile().Display)
	acqBoard.SetDevices(panelLCD, cmlpanel.NewInput(e, keyFD, logf),
		settings.Storage{Dir: otaDir, Cal: hal.CalSource{Source: calTab.Source, Valid: true}})
	return &composition{bus: b, acq: acqBoard, e: e, fe: fe, levels: levels, lcd: panelLCD, keyFD: keyFD}, true
}

// options are the SDS1000CML+'s parts of the core's application: its own
// engine, its UI model's hardware facts, its LCD and HUD, its SCPI dialect
// on VXI-11, its web scope on :8080, and the settings file's level code.
// TRLC-LINKS: REQ-SDS-167
func (c *composition) options() scopeapp.Options {
	var setFE coresettings.Analog
	if c.fe != nil { // a nil *FrontEnd must leave the interface nil
		setFE = c.fe
	}
	return scopeapp.Options{
		Logf: logf, Mark: startMark,
		// The unit has 125 MB and the default collector lets the heap reach
		// twice the live set: after 1 M-sample stopped records and
		// full-depth WF?/frame transfers the app reaches 71 MB RSS with 25 MB
		// left free. A soft limit makes the collector work harder near it
		// instead of growing.
		MemoryLimit: appMemoryLimit,
		Owner:       c.e,
		UI:          c.ui,
		Display: scopeapp.Display{
			Open: c.openLCD, Engine: c.e, HUD: c.hud, OnFrame: c.viewLease, URL: lcd.DeviceURL,
			Canvas: func() render.Canvas { return lcd.NewBackBuffer() },
		},
		// VXI-11 is this scope's only LAN SCPI path (spec 11 §1: no raw
		// socket, no telnet).
		SCPI:     c.scpiEngine,
		SCPIHost: scpihost.Config{VXI11: true, Register: true},
		// No read or write timeouts beyond the header's: /api/frame.bin
		// parks requests up to 2 s (long-poll); slow-peer writes are bounded
		// per response inside the handler.
		Listen: ":8080", Web: c.web, WebOptions: []webserver.Option{webserver.WithPprof()},
		Settings: scopeapp.Settings{
			Analog: setFE, Extension: board.SettingsExtension{Levels: c.levels}, SlotKey: settings.SlotKey,
		},
		Describe: describe,
		OnStop: func() {
			if err := c.bus.CloseWorker(); err != nil {
				logf("acquisition worker shutdown: %v", err)
			}
		},
	}
}

// uiStartTdiv is the timebase the UI model starts at, before the setup is
// restored.
const uiStartTdiv = 500e-6

// describe states the UI model's facts this scope builds it with (ui), for
// the composition's description the browser demo is compared with.
// TRLC-LINKS: REQ-SDS-167
func describe(d *scopeapp.Composition) {
	d.UI = scopeapp.UIFactsOf(cmlpanel.Hardware{}, engine.SupportedTdivs(), uiStartTdiv)
}

// ui is the core's UI model over this scope's engine, front end, levels
// and hardware facts, with the decoded stream (ADR-PANEL-DECODED-STREAM:
// the panel starts it from the BUS sheet's decoder settings) and PRINT's
// hardcopy to the U-disk.
// TRLC-LINKS: REQ-SDS-167
func (c *composition) ui(env *scopeapp.Env) (*panel.Controller, error) {
	var pfe panel.Analog
	if c.fe != nil {
		pfe = c.fe
	}
	pc := panel.New(c.e, pfe, c.levels, cmlpanel.Hardware{}, engine.SupportedTdivs(), uiStartTdiv, logf)
	sv := streamview.New(c.e, logf)
	pc.SetDecodeStream(sv)
	pc.SetHardcopy(func() (string, error) {
		if !mountedAt(uDisk) { // the bare directory would be the internal flash
			return "", fmt.Errorf("no U-disk mounted at %s", uDisk)
		}
		return saveHardcopy(uDisk, env.ScreenPNG())
	})
	c.pc, c.sv = pc, sv
	logf("panel controller up (fpga_key fd=%d)", c.keyFD)
	return pc, nil
}

// scpiEngine is SCPI over VXI-11 (spec 11): its INVS shadow is the
// display-invert truth the status and the HUD read and the CH tabs' Invert
// rows write, and the panel's display state backs its display commands
// (XYDS/PESU/MENU). SCDP renders a fresh headless picture.
// TRLC-LINKS: REQ-SDS-024, REQ-SDS-167
func (c *composition) scpiEngine(env *scopeapp.Env) (scopeapp.SCPI, error) {
	var sfe scpi.Analog
	if c.fe != nil {
		sfe = c.fe
	}
	shot := func() []byte {
		back := lcd.NewBackBuffer()
		env.Render(back)
		return lcd.EncodeBMP(back)
	}
	h := scpi.New(scopeSource{c.e, env.Frames}, sfe, c.levels, c.pc, shot, logf)
	c.scpi = h
	c.pc.SetInverter(panel.Inverter2(h)) // the CH tabs' Invert rows: the same INVS shadow
	return scopeapp.SCPI{
		Exec:   h.HandleLine,
		Setups: func(s *panel.SharedSetups) { h.SetSetupMemory(s) },
	}, nil
}

// web is the web scope over this scope's engine and front end, with the
// board's capabilities, the SCPI shadow's display invert and the shared
// setup memory.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-167
func (c *composition) web(env *scopeapp.Env) (webserver.Scope, error) {
	var feIface webscope.Analog
	if c.fe != nil {
		feIface = c.fe
	}
	sc := webscope.New(scopeSource{c.e, env.Frames}, feIface, c.levels, c.pc, env.ScreenPNG, webscope.WithBoard(c.acq))
	sc.SetInvertSource(c.scpi.Inverted)                             // display-level INVS: SCPI shadow → /api/status
	sc.SetSetupMemory(scopeapp.WebSetups{SharedSetups: env.Setups}) // the web top bar's Setups: the same slots as *SAV/*RCL
	return sc.WithCaps(), nil
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

// attachProfile attaches the scope's profile for this unit (its identity,
// the serial read from the vendor's record) to the board, from which the
// page reads its capabilities (with the front end and the trigger); without
// it the web serves none.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-010
func attachProfile(b *acq.Board) {
	prof, err := scope.Profile(scope.Identity())
	if err != nil {
		logf("profile: %v — the web serves no capabilities", err)
		return
	}
	b.SetProfile(prof)
}
