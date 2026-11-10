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
	"runtime"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/labeth/open-dso-core/hal"
	"github.com/labeth/open-dso-core/instrument"
	"github.com/labeth/open-dso-core/render"
	"github.com/labeth/open-dso-core/scopeapp"
	scpihost "github.com/labeth/open-dso-core/scpi/host"
	coresettings "github.com/labeth/open-dso-core/settings"
	"github.com/labeth/open-dso-core/streamview"
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
// core's scope application over it (scopeapp.Run: the core's HAL cycle,
// UI model, display and settings, with this scope's own parts) until
// SIGTERM, then returns the process's exit code.
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

// composition is the SDS1000CML+'s parts the core's application runs over
// its HAL cycle: the board, its own decoded streams, the analog front end
// and the levels its DACs hold (kept beside the instrument model), and the
// UI model and SCPI handler they wire to one another.
type composition struct {
	bus     *bus.Dev
	acq     *acq.Board
	streams *engine.DecodedStreams // the board's own stream sessions
	fe      *analog.FrontEnd       // nil without SPI to the front end
	afe     *modelAnalog           // fe as the controllers drive it; nil with fe
	codes   *board.ModelCodes      // the DAC codes the controllers chose, beside the model
	levels  *board.Levels
	lcd     *lcd.Display // the panel; its framebuffer once the display loop opened it
	keyFD   int
	ledLag  atomic.Int64 // the last lamp word's wait for its latch, ns

	quiet     scopeapp.QuietPacer // the cycle's pace and quiet lock; nil: none
	lcdFrames atomic.Uint64       // the pictures the display put up

	pc   *panel.Controller
	scpi *scpi.Handler
}

// newBoard brings the unit up to a board the core can run: the OTA agent's
// contract, the fabric and its worker, the board, the calibration and the
// front end, the profile with the unit's identity, the hand-over, the
// devices and the board's stream sessions. false: there is nothing to
// drive (logged; without the GPMC descriptor or a bus it waits for the
// signal, never writing the health token, so the agent sees why).
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

	var interleaveCal *acq.InterleaveCalibration
	if c, err := acq.DefaultInterleaveCalibration(); err != nil {
		logf("interleave calibration: built-in default rejected: %v", err)
	} else {
		interleaveCal = c
	}
	// The acquisition board: the record memory and the panel plane behind
	// the HAL; the core's HAL cycle drives it.
	var mem acq.Memory
	if sramBackend != nil { // a nil *Capture must leave the interface nil
		mem = sramBackend
	}
	acqBoard := acq.New(acq.Config{Memory: mem, Panel: b})
	c := &composition{bus: b, acq: acqBoard, keyFD: keyFD}
	c.wireBoard(imageSwitcher{fd: gpmcFD}, interleaveCal)

	// Per-unit calibration (spec 10): file → backup → compiled defaults.
	calTab := cal.Load(logf)
	logf("cal source: %s (C1@1V zero=%d gainDAC=%d)", calTab.Source,
		calTab.Rec[0][8].Zero, calTab.Rec[0][8].GainDAC)

	// The DAC codes the controllers choose are kept beside the instrument
	// model (built by the core's application: attach); the board stages
	// them on its panel plane.
	c.codes = board.NewModelCodes(nil)
	// The vertical front end is off the GPMC bus (SPI). Optional: without it
	// the scope still runs on the inherited boot range.
	c.fe = startFrontEnd(calTab, func(ch int, code uint16) {
		c.codes.SetOffsetDAC(ch, code)
		acqBoard.StageOffsetDAC(ch, code)
	})
	if c.fe != nil {
		for ch := range 2 {
			if err := c.fe.SetVdiv(ch, 8); err != nil {
				logf("SRAM: initial range: %v", err)
				return nil, false
			}
		}
	}
	// The trigger level and the offsets in volts over the codes: the
	// controllers' side of the board's levels. The controllers drive the
	// front end through the model's projection of it; the board's staging
	// and the owner's writes take it as it is.
	var bfe board.FrontEnd
	if c.fe != nil {
		c.afe = &modelAnalog{FrontEnd: c.fe}
		bfe = c.afe
		acqBoard.SetFrontEnd(c.fe)
	}
	c.levels = board.New(c.codes, bfe)
	acqBoard.SetLevelGrid(c.levels.TrigLevelGrid)
	attachProfile(acqBoard)
	acqBoard.SetHandover(hal.Handover{
		FDs:       map[string]int{"gpmc": gpmcFD, "fpga_key": keyFD},
		HealthTok: healthPath, StateDir: otaDir,
	})
	// The front panel: the key matrix on SIGIO off the inherited
	// /dev/fpga_key fd through the board's panel scan, the lamp latch. The
	// settings and setups live in the stick's ota/ directory.
	c.lcd = lcd.NewDisplay(acqBoard.Profile().Display)
	acqBoard.SetDevices(c.lcd, cmlpanel.NewInput(boardMatrix{acqBoard}, keyFD, logf),
		settings.Storage{Dir: otaDir, Cal: hal.CalSource{Source: calTab.Source, Valid: true}})
	// The board's own decoded streams, which the core's cycle serves and
	// steps (ADR-HAL-BOARD-SESSIONS).
	c.streams = engine.NewBoardStreams(acqBoard, b, logf)
	acqBoard.SetSession(c.streams)
	// The fabric's start on the general image: its capabilities, the
	// converters.
	acqBoard.StartFabric()
	return c, true
}

// wireBoard gives the board what it reports to and runs with: the lamp
// latch's lag and the panel plane's write errors, the image loader, the
// converters' interleave calibration and the converter check's look at
// each full-rate record, the log, and the tuning knobs at their defaults.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-005, REQ-SDS-127
func (c *composition) wireBoard(images acq.ImageSwitcher, cal *acq.InterleaveCalibration) {
	b := c.acq
	b.SetReporting(func(d time.Duration) { c.ledLag.Store(int64(d)) }, b.NoteBusError)
	b.SetImages(images)
	engine.NoteImages(images)
	b.SetInterleaveCalibration(cal)
	b.SetRawObserver(b.NoteRecordCores)
	b.SetLog(logf, time.Now)
	b.Tuning().Defaults(acq.DefaultArmSettle, b.HasMemory(), true)
}

// attach keeps the levels and the front end's channels in instrument model
// m, before the owner loop starts: the codes beside it, the level projected
// into it, the comparator DAC staged from its level, one trigger source,
// slope and level across the trigger types, a long read's progress on the
// loading indicator, and the soft memory limit above the working set.
// TRLC-LINKS: REQ-SDS-098, REQ-SDS-095, REQ-SDS-011
func (c *composition) attach(m *instrument.Model) {
	c.codes.Attach(m)
	c.levels.Project(m)
	if c.afe != nil {
		c.afe.m, c.afe.levels = m, c.levels
		c.afe.project(0)
		c.afe.project(1)
	}
	m.OnTrigType(board.ShareTrigger)
	m.OnTrigSlope(board.KeepSlope)
	c.acq.SetLevelDAC(func() (uint16, bool) {
		st := m.State()
		if !st.LevelSet {
			return 0, false // the boot comparator stays until a level is set
		}
		return c.levels.LevelCode(st.Trigger.LevelV(), st.Trigger.Source().Index), true
	})
	if c.acq.HasMemory() {
		c.acq.SetReadProgress(m.SetBusyProgress)
		setMemoryBackstop()
	}
}

// memBackstopMin is the least soft memory limit on the device: above a
// stopped deep record's working set, below the RSS the unit has room for
// (71 MB of its 125 MB left 25 MB free).
const memBackstopMin = 60 << 20

// setMemoryBackstop sets the soft memory limit above the measured working
// set: only a genuine leak climbs to it, where the collector beats an
// OOM-kill into a slot rollback. A stopped deep record holds about 35 MB
// live: a limit at or under that keeps the collector running without end.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-006
func setMemoryBackstop() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	debug.SetMemoryLimit(max(int64(ms.Sys)+32<<20, memBackstopMin)) //nolint:gosec // G115: the process memory, far below 2^63
}

// options are the SDS1000CML+'s parts of the core's application over its
// HAL cycle: its power-on state and the state kept beside the model, its
// UI model's hardware facts, its LCD, its SCPI dialect on VXI-11, its web
// scope on :8080, and the settings file's level code.
// TRLC-LINKS: REQ-SDS-167
func (c *composition) options() scopeapp.Options {
	var setFE coresettings.Analog
	if c.afe != nil { // a nil *modelAnalog must leave the interface nil
		setFE = c.afe
	}
	return scopeapp.Options{
		Logf: logf, Mark: startMark,
		// The unit has 125 MB and the default collector lets the heap reach
		// twice the live set: after 1 M-sample stopped records and
		// full-depth WF?/frame transfers the app reaches 71 MB RSS with 25 MB
		// left free. A soft limit makes the collector work harder near it
		// instead of growing.
		MemoryLimit: appMemoryLimit,
		Defaults:    board.PowerOn,
		OnModel:     c.attach,
		// The fabric compares no mask: the mask test runs in software on
		// every frame the owner reads.
		SoftwareMask: true,
		UI:           c.ui,
		Display: scopeapp.Display{
			Open: c.openLCD, Engine: lcdEngine{c}, URL: lcd.DeviceURL,
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
			// the setup through the engine the UI model drives: its depth,
			// its frequency response's channels and its roll stream
			Controls: func(env *scopeapp.Env) instrument.Controls { return env.HALScope.Engine() },
		},
		Describe: describe,
		OnStop: func() {
			if c.bus == nil {
				return
			}
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

// ui is the core's UI model over the instrument model's controls (the HAL
// scope's engine), this scope's front end, levels and hardware facts, with
// the board's decoded streams (ADR-PANEL-DECODED-STREAM: the panel starts
// them from the BUS sheet's decoder settings) and PRINT's hardcopy to the
// U-disk.
// TRLC-LINKS: REQ-SDS-167
func (c *composition) ui(env *scopeapp.Env) (*panel.Controller, error) {
	c.quiet = env.Quiet() // before the display loop runs (lcdEngine)
	var pfe panel.Analog
	if c.afe != nil {
		pfe = c.afe
	}
	pc := panel.New(env.HALScope.Engine(), pfe, c.levels, cmlpanel.Hardware{}, engine.SupportedTdivs(), uiStartTdiv, logf)
	pc.SetDecodeStream(streamview.New(c.streams.StreamSource(), logf))
	pc.SetHardcopy(func() (string, error) {
		if !mountedAt(uDisk) { // the bare directory would be the internal flash
			return "", fmt.Errorf("no U-disk mounted at %s", uDisk)
		}
		return saveHardcopy(uDisk, env.ScreenPNG())
	})
	c.pc = pc
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
	if c.afe != nil {
		sfe = c.afe
	}
	shot := func() []byte {
		back := lcd.NewBackBuffer()
		env.Render(back)
		return lcd.EncodeBMP(back)
	}
	h := scpi.New(scopeSource{env.HALScope.Engine(), env.Frames}, sfe, c.levels, c.pc, shot, logf)
	c.scpi = h
	c.pc.SetInverter(panel.Inverter2(h)) // the CH tabs' Invert rows: the same INVS shadow
	return scopeapp.SCPI{
		Exec:   h.HandleLine,
		Setups: func(s *panel.SharedSetups) { h.SetSetupMemory(s) },
	}, nil
}

// web is the web scope over the HAL scope's engine (webEngine: the frames,
// the cycle's quiet lock and the board's counters beside it) and this
// scope's front end, with the board's capabilities and routes, the board's
// stacking session as LONG work on the cycle, the SCPI shadow's display
// invert and the shared setup memory.
// TRLC-LINKS: REQ-SDS-164, REQ-SDS-167
func (c *composition) web(env *scopeapp.Env) (webserver.Scope, error) {
	var feIface webscope.Analog
	if c.afe != nil {
		feIface = c.afe
	}
	eng := env.HALScope.Engine()
	we := &webEngine{scopeSource: scopeSource{eng, env.Frames}, board: c.acq, quiet: c.quiet, ledLag: &c.ledLag, lcdFrames: &c.lcdFrames, mask: env.Mask}
	opts := []webscope.Option{webscope.WithBoard(c.acq)}
	if st, ok := eng.(webscope.FPGAStacker); ok {
		opts = append(opts, webscope.WithStacker(st))
	}
	sc := webscope.New(we, feIface, c.levels, c.pc, env.ScreenPNG, opts...)
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

// startFrontEnd brings up the SPI vertical front end, its offset DAC codes
// staged by stage; nil when it is unavailable (logged).
// TRLC-LINKS: REQ-SDS-167
func startFrontEnd(calTab *cal.Table, stage func(ch int, code uint16)) *analog.FrontEnd {
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
	fe.OnOffset(stage) // offset re-anchors to each detent's cal zero
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
