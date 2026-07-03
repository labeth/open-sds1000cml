// Command app is the minimal clean-room scope application (v1). It satisfies
// the app ↔ OTA contract (ota/README.md): launched by the agent as a direct
// child, it discovers the boot-inherited /dev/Gpmc + /dev/fpga_key fds via
// /proc/self/fd (never fresh-opens, never closes), runs the single-owner
// acquisition engine, reports frame-advance health at OTA_HEALTH_PATH, exits
// cleanly on SIGTERM — and hosts the control webpage on :8080.
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/buildinfo"
	"open-sds/app/internal/bus"
	"open-sds/app/internal/cal"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/frames"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/scpi"
	"open-sds/app/internal/vxi11srv"
	"open-sds/app/internal/web"
)

// scopeSource wires the web layer: setters/stats from the engine, frames
// from the fan-out (the arena's read slot belongs to the fan-out alone).
type scopeSource struct {
	*engine.Engine
	fo *frames.Fanout
}

func (s scopeSource) WithFrame(fn func(*engine.Frame)) { s.fo.WithFrame(fn) }

// buildHUD assembles the LCD/SCDP heads-up state from the engine + front end.
// The trigger-level readout is scaled by the SOURCE channel's V/div.
func buildHUD(e *engine.Engine, fe *analog.FrontEnd) lcd.HUD {
	st := e.Snapshot()
	hud := lcd.HUD{
		C1VdivV: 1, C2VdivV: 1, TdivS: st.TdivS,
		TrigSrc: st.TrigSource, TrigRising: st.TrigRising,
		Running: st.Running, Norm: st.Norm, Single: st.Single,
		TrigPosFrac: st.TrigPosFrac, TwoChan: true,
		ShowC1: true, ShowC2: true,
	}
	if fe != nil {
		idx, _ := fe.Snapshot()
		hud.C1VdivV = analog.Detents[idx[0]].VdivV
		hud.C2VdivV = analog.Detents[idx[1]].VdivV
		hud.Probe1 = fe.ProbeFactor(0)
		hud.Probe2 = fe.ProbeFactor(1)
		if st.OffC1 != 0 {
			hud.OffC1V = fe.OffsetVolts(0, st.OffC1)
		}
		if st.OffC2 != 0 {
			hud.OffC2V = fe.OffsetVolts(1, st.OffC2)
		}
	}
	srcVdiv := hud.C1VdivV
	if st.TrigSource == 1 {
		srcVdiv = hud.C2VdivV
	}
	if st.TrigCode != 0 && srcVdiv > 0 {
		hud.TrigLvlDiv = engine.TrigLevelVolts(st.TrigCode) / srcVdiv
	}
	if pc := uiCtrl.Load(); pc != nil { // menu overlay + per-channel display
		mv := pc.MenuView()
		hud.ShowC1, hud.ShowC2 = mv.ShowC1, mv.ShowC2
		hud.MenuOpen, hud.MenuTitle, hud.MenuSel = mv.Open, mv.Title, mv.Sel
		hud.MenuItems = make([]lcd.MenuItem, len(mv.Items))
		for i, it := range mv.Items {
			hud.MenuItems[i] = lcd.MenuItem{Label: it.Label, Value: it.Value}
		}
	}
	return hud
}

// uiCtrl is the panel controller, published after creation so the LCD render
// loop (started earlier) and the SCDP screenshot can read the menu overlay.
var uiCtrl atomic.Pointer[panel.Controller]

// runLCD drives the device panel at the 50 ms display cadence (spec 07 §8 —
// a hard minimum; faster starves the acquisition owner). Fully optional: on
// any bring-up failure the scope keeps running headless.
func runLCD(e *engine.Engine, fe *analog.FrontEnd, fo *frames.Fanout) {
	if err := lcd.Bringup(logf); err != nil {
		logf("lcd: disabled: %v", err)
		return
	}
	fb, err := lcd.OpenFB()
	if err != nil {
		logf("lcd: disabled: %v", err)
		return
	}
	logf("lcd: renderer up on /dev/fb0")
	back := lcd.NewMemSurface()
	var lastSeq uint64
	var lastFresh time.Time
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		hud := buildHUD(e, fe)
		fo.WithFrame(func(f *engine.Frame) {
			// The publish rate is slower than the render tick, so "fresh"
			// is a short window, not a per-tick flag — otherwise the
			// liveness strip flickers red on every held tick.
			if f != nil && f.Seq != lastSeq {
				lastSeq = f.Seq
				lastFresh = time.Now()
			}
			if f != nil {
				hud.Trigd = f.Trigd
				hud.SampleS = f.SampleS
			}
			live := f != nil && time.Since(lastFresh) < 300*time.Millisecond
			lcd.Render(back, f, hud, live)
		})
		fb.Present(back)
	}
}

func logf(format string, a ...any) {
	fmt.Printf("[app] "+format+"\n", a...)
}

// findInheritedFD scans /proc/self/fd for the descriptor whose link target is
// path. Same technique as the reference stubapp; fds < 3 are skipped.
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

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// healthLoop implements the app side of the health contract: the FIRST token
// write is gated on ≥3 genuine coherent frames; afterwards the token is
// re-touched (unique content) whenever the heartbeat advances, throttled to
// ~400 ms. A wedged engine stops the touches, so the agent relaunches us on
// the still-live fd.
func healthLoop(e *engine.Engine, path string) {
	var lastFrames uint64
	var lastWrite time.Time
	var started bool
	for {
		time.Sleep(100 * time.Millisecond)
		s := e.Snapshot()
		if s.Wedged {
			continue
		}
		if !started {
			if s.Coherent < 3 || s.Frames < 3 {
				continue
			}
			started = true
			logf("engine healthy: %d coherent frames — starting health reports", s.Coherent)
		}
		if s.Frames == lastFrames || time.Since(lastWrite) < 400*time.Millisecond {
			continue
		}
		tok := fmt.Sprintf("frames=%d coherent=%d published=%d ts=%d\n",
			s.Frames, s.Coherent, s.Published, time.Now().UnixNano())
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(tok), 0o644); err == nil {
			if os.Rename(tmp, path) == nil {
				lastFrames, lastWrite = s.Frames, time.Now()
			}
		}
	}
}

func main() {
	logf("start pid=%d version=%s", os.Getpid(), buildinfo.String())

	gpmcDev := envOr("SCOPE_GPMC", "/dev/Gpmc")
	healthPath := os.Getenv("OTA_HEALTH_PATH")
	mmapDrain := os.Getenv("SCOPE_MMAP_DRAIN") != "0"
	listen := envOr("SCOPE_HTTP", ":8080")

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

	b, err := bus.New(gpmcFD, mmapDrain)
	if err != nil {
		logf("FATAL: bus init: %v — refusing to drive", err)
		<-sig
		os.Exit(0)
	}
	logf("bus up, mmap drain=%v", b.MmapDrain())

	e := engine.New(engine.Config{Bus: b, Logf: logf})
	go e.Run()

	if healthPath != "" {
		go healthLoop(e, healthPath)
	} else {
		logf("WARNING: OTA_HEALTH_PATH unset — no health reporting")
	}

	// Per-unit calibration (spec 10): file → backup → compiled defaults.
	calTab := cal.Load(logf)
	logf("cal source: %s (C1@1V zero=%d gainDAC=%d)", calTab.Source,
		calTab.Rec[0][8].Zero, calTab.Rec[0][8].GainDAC)

	// The vertical front end is off the GPMC bus (SPI). Optional: without it
	// the scope still runs on the inherited boot range.
	var fe *analog.FrontEnd
	var feIface web.Analog
	if dev, err := analog.NewDev(); err != nil {
		logf("WARNING: SPI front end unavailable (%v) — V/div control disabled", err)
	} else {
		fe = analog.New(dev, nil, calTab)
		fe.OnOffset(e.SetOffsetDAC) // offset re-anchors to each detent's cal zero
		fe.OnVdiv(e.SetChannelVdiv) // keep the trigger level→display-code map current
		feIface = fe
		logf("SPI front end up (seeded to boot detent, not emitted)")
	}

	// The fan-out is the arena's single consumer; the web UI and the LCD
	// renderer read its snapshot under the fan-out lock.
	fo := frames.New()
	stopFo := make(chan struct{})
	go fo.Run(e, stopFo)

	go runLCD(e, fe, fo)

	// Front-panel controller: SIGIO off the inherited /dev/fpga_key fd,
	// matrix reads via the engine's request/reply channel.
	var pfe panel.Analog
	if fe != nil {
		pfe = fe
	}
	pc := panel.New(e, pfe, keyFD, engine.SupportedTdivs(), 500e-6, logf)
	uiCtrl.Store(pc) // publish for the LCD render loop + SCDP screenshot
	go pc.Run(stopFo)
	logf("panel controller up (fpga_key fd=%d)", keyFD)

	// Device-screen PNG for the web /api/screen.png endpoint: the exact LCD render.
	screenPNG := func() []byte {
		back := lcd.NewMemSurface()
		hud := buildHUD(e, fe)
		fo.WithFrame(func(f *engine.Frame) {
			if f != nil {
				hud.SampleS = f.SampleS
				hud.Trigd = f.Trigd
			}
			lcd.Render(back, f, hud, true)
		})
		return lcd.EncodePNG(back)
	}
	srv := &http.Server{Addr: listen, Handler: web.New(scopeSource{e, fo}, feIface, pc, screenPNG).Handler()}
	go func() {
		logf("web ui listening on %s", listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logf("web server: %v", err)
		}
	}()

	// SCPI over VXI-11 (spec 11): the host instrument-control interface.
	// SCDP renders a fresh headless frame — works with or without the LCD.
	var sfe scpi.Analog
	if fe != nil {
		sfe = fe
	}
	shot := func() []byte {
		back := lcd.NewMemSurface()
		hud := buildHUD(e, fe)
		fo.WithFrame(func(f *engine.Frame) {
			if f != nil {
				hud.SampleS = f.SampleS
				hud.Trigd = f.Trigd
			}
			lcd.Render(back, f, hud, true)
		})
		return lcd.EncodeBMP(back)
	}
	scpiH := scpi.New(scopeSource{e, fo}, sfe, shot, logf)
	if _, port, err := vxi11srv.Start(scpiH.HandleLine, true, logf); err != nil {
		logf("WARNING: VXI-11 server failed: %v", err)
	} else {
		logf("scpi: VXI-11 DEVICE_CORE on tcp/%d", port)
	}

	s := <-sig
	logf("signal %v — stopping engine at frame boundary", s)
	if !e.Stop(2 * time.Second) {
		logf("WARNING: engine did not stop in time")
	}
	os.Exit(0)
}
