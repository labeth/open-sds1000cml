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
	"open-sds/app/internal/fpgaload"
	"open-sds/app/internal/frames"
	"open-sds/app/internal/iface"
	"open-sds/app/internal/lcd"
	"open-sds/app/internal/panel"
	"open-sds/app/internal/scpi"
	"open-sds/app/internal/settings"
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

// WaitNextFrame lets the web layer park a long-poll until the fan-out
// snapshots a frame newer than last (web.go type-asserts for this method, so
// test doubles without it degrade to a short poll).
func (s scopeSource) WaitNextFrame(last uint64, timeout time.Duration) uint64 {
	return s.fo.WaitNext(last, timeout)
}

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
		URL: lcd.DeviceURL(), // cached; re-enumerates at most every few seconds
	}
	if fe != nil {
		idx, _ := fe.Snapshot()
		hud.C1VdivV = analog.Detents[idx[0]].VdivV
		hud.C2VdivV = analog.Detents[idx[1]].VdivV
		hud.Probe1 = fe.ProbeFactor(0)
		hud.Probe2 = fe.ProbeFactor(1)
		hud.Cpl1 = fe.Coupling(0)
		hud.Cpl2 = fe.Coupling(1)
		// AC removes the DC in software (mean → centre), so the ground marker
		// belongs at centre; leave it at 0 for an AC-coupled channel.
		if st.OffC1 != 0 && hud.Cpl1 != analog.CplAC {
			hud.OffC1V = fe.OffsetVolts(0, st.OffC1)
		}
		if st.OffC2 != 0 && hud.Cpl2 != analog.CplAC {
			hud.OffC2V = fe.OffsetVolts(1, st.OffC2)
		}
	}
	srcVdiv, srcOff := hud.C1VdivV, hud.OffC1V
	if st.TrigSource == 1 {
		srcVdiv, srcOff = hud.C2VdivV, hud.OffC2V
	}
	if st.TrigCode != 0 && srcVdiv > 0 {
		// Include the source channel's offset so the level marker sits in the
		// same display frame as the (offset-shifted) trace and ground marker —
		// otherwise centring a signal with DC pushes the marker off-screen.
		hud.TrigLvlDiv = (e.TrigVoltsAt(st.TrigCode, st.TrigSource) + srcOff) / srcVdiv
	}
	if pc := uiCtrl.Load(); pc != nil { // menu overlay + per-channel display
		mv := pc.MenuView()
		hud.ShowC1, hud.ShowC2 = mv.ShowC1, mv.ShowC2
		hud.ShowMeas = mv.ShowMeas
		hud.ViewMode, hud.MathMode = mv.ViewMode, mv.MathMode
		hud.AutosetBusy, hud.AutosetMsg = mv.AutosetBusy, mv.AutosetMsg
		hud.Zoom, hud.ZoomOff = mv.Zoom, mv.ZoomOff
		hud.Persist = mv.Persist
		hud.DecProto, hud.DecBaud = mv.DecProto, mv.DecBaud
		hud.DecChA, hud.DecChB = mv.DecChA, mv.DecChB
		hud.DecCPOL, hud.DecCPHA = mv.DecCPOL, mv.DecCPHA
		hud.DecFormat = mv.DecFormat
		rv := pc.RefView()
		for i := 0; i < 2; i++ {
			hud.RefC1[i], hud.RefC2[i], hud.RefShow[i] = rv[i].C1, rv[i].C2, rv[i].Show
		}
		hud.CurOn, hud.CurType, hud.CurSel = mv.CurOn, mv.CurType, mv.CurSel
		hud.CurX, hud.CurY = mv.CurX, mv.CurY
		hud.MenuOpen, hud.MenuTitle, hud.MenuSel = mv.Open, mv.Title, mv.Sel
		if mv.Open { // only the visible menu needs its item list copied each tick
			hud.MenuItems = make([]lcd.MenuItem, len(mv.Items))
			for i, it := range mv.Items {
				hud.MenuItems[i] = lcd.MenuItem{Label: it.Label, Value: it.Value}
			}
		}
		sv := pc.SuperresView()
		hud.SRActive, hud.SRFocus, hud.SRStatus = sv.Active, sv.Focus, sv.Status
		hud.SRBits, hud.SRMean, hud.SRk = sv.Bits, sv.Mean, sv.K
		hud.SRMean2, hud.SRAlign, hud.SRSampleS = sv.Mean2, sv.Align, sv.SampleS
		hud.SRGateLo, hud.SRGateHi, hud.SRN = sv.GateLo, sv.GateHi, sv.N
		hud.SRWinLo, hud.SRWinHi, hud.SRPeriod = sv.WinLo, sv.WinHi, sv.Period
		hud.MaskMsg = pc.MaskStatus()
	}
	if sh := scpiCtrl.Load(); sh != nil { // display-level INVS: the SCPI shadow is the truth
		inv := sh.Inverted()
		hud.Inv1, hud.Inv2 = inv[0], inv[1]
	}
	// Zone/mask overlay state (engine-side test; render parity with the web).
	hud.ZoneMode, hud.MaskMode = st.ZoneMode, st.MaskMode
	hud.MaskPass, hud.MaskFail, hud.MaskSkip = st.MaskPass, st.MaskFail, st.MaskSkip
	hud.MaskStopped = st.MaskStopped
	hud.ZoneSkip = st.ZoneSkip
	if st.ZoneCount > 0 {
		hud.Zones = e.Zones()
	}
	if st.MaskMode > 0 && st.MaskSet {
		if m := e.MaskEnvelope(); m != nil {
			hud.MaskLo, hud.MaskHi, hud.MaskWin = m.Lo, m.Hi, m.WinCols
		}
	}
	// FRA / Bode: selecting the BODE view (ViewMode 3) arms the engine's
	// accumulation (default ref C1 / DUT C2) and feeds the accumulated curve
	// to the renderer; leaving it disarms. The web can also arm FRA — both
	// observe the same accumulated points.
	if hud.ViewMode == 3 {
		e.SetBodeMode(true, 0, 1)
		pts := e.BodePoints()
		hud.BodeFreq = make([]float64, len(pts))
		hud.BodeGain = make([]float64, len(pts))
		hud.BodePhase = make([]float64, len(pts))
		for i, p := range pts {
			hud.BodeFreq[i], hud.BodeGain[i], hud.BodePhase[i] = p.FreqHz, p.GainDB, p.PhaseDeg
		}
	} else if st.BodeMode > 0 && lastViewWasBode.Swap(false) {
		// only the device auto-arm gets auto-disarmed; a web-armed FRA persists
		e.SetBodeMode(false, 0, 1)
	}
	if hud.ViewMode == 3 {
		lastViewWasBode.Store(true)
	}
	hud.Spect = lcdSpect
	return hud
}

// lastViewWasBode tracks whether the DEVICE's BODE view armed FRA, so leaving
// the view disarms only the device's own auto-arm (a web-armed sweep persists).
var lastViewWasBode atomic.Bool

// lcdSpect is the one "FFT over time" waterfall buffer: the LCD loop pushes new
// spectrum rows into it (SPECTROGRAM view); every render path (panel, the
// /api/screen.png PNG, the SCDP hardcopy) blits it, so all three agree.
var lcdSpect = lcd.NewSpectrogram()

// uiCtrl is the panel controller, published after creation so the LCD render
// loop (started earlier) and the SCDP screenshot can read the menu overlay.
var uiCtrl atomic.Pointer[panel.Controller]

// scpiCtrl is the SCPI handler, published after creation so the LCD render
// loop and the screenshot paths can read the display-invert (INVS) truth.
var scpiCtrl atomic.Pointer[scpi.Handler]

// renderSig is a scalar fingerprint of everything the static (Y-T / X-Y / FFT)
// views draw. When it is unchanged tick-to-tick the display is identical, so the
// LCD loop skips the rasterize+blit — the heavy, bus-contending part that
// otherwise ran 20×/s even on a held frame. Time-evolving views (persistence,
// Bode, spectrogram, super-res review, an active mask/zone test) bypass this and
// always repaint. All fields are comparable so `==` decides.
type renderSig struct {
	seq                   uint64
	view, math, dec, zoom int
	curType, curSel       int
	menuSel               int
	url                   string
	tdiv, zoomOff         float64
	c1v, c2v, off1, off2  float64
	trig                  float64
	curX, curY            [2]float64
	probe1, probe2        float64
	cpl1, cpl2            int
	inv1, inv2            bool
	showMeas, persist     bool
	running, single, norm bool
	trigd, live           bool
	showC1, showC2        bool
	menuOpen, curOn       bool
	ref0, ref1            bool
}

func renderSigOf(f *engine.Frame, hud lcd.HUD, live bool) renderSig {
	var seq uint64
	if f != nil {
		seq = f.Seq
	}
	return renderSig{
		seq:  seq,
		view: hud.ViewMode, math: hud.MathMode, dec: hud.DecProto, zoom: hud.Zoom,
		curType: hud.CurType, curSel: hud.CurSel, menuSel: hud.MenuSel,
		url:  hud.URL, // an IP appearing/changing must repaint a held display
		tdiv: hud.TdivS, zoomOff: hud.ZoomOff,
		c1v: hud.C1VdivV, c2v: hud.C2VdivV, off1: hud.OffC1V, off2: hud.OffC2V,
		trig: hud.TrigLvlDiv, curX: hud.CurX, curY: hud.CurY,
		probe1: hud.Probe1, probe2: hud.Probe2, cpl1: hud.Cpl1, cpl2: hud.Cpl2,
		inv1: hud.Inv1, inv2: hud.Inv2, // an INVS flip must repaint a held display
		showMeas: hud.ShowMeas, persist: hud.Persist,
		running: hud.Running, single: hud.Single, norm: hud.Norm,
		trigd: hud.Trigd, live: live, showC1: hud.ShowC1, showC2: hud.ShowC2,
		menuOpen: hud.MenuOpen, curOn: hud.CurOn,
		ref0: hud.RefShow[0], ref1: hud.RefShow[1],
	}
}

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
	persistLayer := lcd.NewMemSurface() // afterglow trace layer, owned by this loop
	var sgSeq uint64                    // last frame pushed into the shared spectrogram (lcdSpect)
	var lastSeq uint64
	var lastFresh time.Time
	var lastSig renderSig
	haveSig := false
	for {
		time.Sleep(e.RenderPeriod()) // tunable display cadence (default 50ms, spec 07 §8 floor)
		// Render only OUTSIDE the engine's load-sensitive windows (arm-settle +
		// drain): a concurrent render burst there corrupts the HW capture on this
		// single core. This pauses ~19ms/frame; the wait+pace (~90ms) is free.
		e.QuietRLock()
		hud := buildHUD(e, fe)
		present := false
		fo.WithFrame(func(f *engine.Frame) {
			// The publish rate is slower than the render tick, so "fresh"
			// is a short window, not a per-tick flag — otherwise the
			// liveness strip flickers red on every held tick.
			if f != nil && f.Seq != lastSeq {
				lastSeq = f.Seq
				lastFresh = time.Now()
			}
			if f != nil {
				hud.Trigd = f.Trigd // on-screen TRIG'd indicator (there is no TRIG'd lamp)
				hud.SampleS = f.SampleS
				// Spectrogram: push each fresh per-sample frame's spectrum as a
				// new waterfall row while the SPECTROGRAM view is active.
				if hud.ViewMode == 4 && !f.IsEnv && f.Seq != sgSeq && len(f.C1) > 0 {
					sgSeq = f.Seq
					effNyq := 0.0
					if f.SampleS > 0 {
						effNyq = 0.5 / f.SampleS
					}
					lcdSpect.Push(f, 0, effNyq) // C1
				}
			}
			if pc := uiCtrl.Load(); pc != nil {
				pc.SyncLEDs() // keep RUN/STOP + SINGLE lamps in step (re-latches only on change)
			}
			live := f != nil && time.Since(lastFresh) < 300*time.Millisecond
			// Repaint only when the picture changed. Time-evolving views and active
			// tests bypass the cache and always repaint (afterglow decay, the Bode
			// sweep, the spectrogram waterfall, super-res review, a running mask/zone
			// test whose live counters advance faster than the published frame seq).
			force := hud.Persist || hud.ViewMode >= 3 || hud.SRActive ||
				hud.MaskMode > 0 || hud.ZoneMode > 0
			sig := renderSigOf(f, hud, live)
			if haveSig && !force && sig == lastSig {
				return // nothing visible changed — skip the rasterize + framebuffer blit
			}
			lastSig, haveSig = sig, true
			lcd.Render(back, f, hud, live, persistLayer)
			present = true
		})
		if present {
			fb.Present(back)
		}
		e.QuietRUnlock()
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
	var lastBeats uint64
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
		// Key on the BEAT counter, not the frame counter: holdoff pacing (up
		// to 10 s between frames) and recovery bring-up are healthy states
		// that advance beats without advancing frames; a frame-keyed token
		// went stale inside the agent's 3 s window and got a healthy app
		// killed (live-storm finding).
		beats := e.Beats()
		if beats == lastBeats || time.Since(lastWrite) < 400*time.Millisecond {
			continue
		}
		tok := fmt.Sprintf("frames=%d beats=%d coherent=%d published=%d ts=%d\n",
			s.Frames, beats, s.Coherent, s.Published, time.Now().UnixNano())
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(tok), 0o644); err == nil {
			if os.Rename(tmp, path) == nil {
				lastBeats, lastWrite = beats, time.Now()
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

	b, err := bus.New(gpmcFD)
	if err != nil {
		logf("FATAL: bus init: %v — refusing to drive", err)
		<-sig
		os.Exit(0)
	}
	logf("bus constructed (ioctl)")

	// Configure the fabric with OUR standard bitstream before the engine drives
	// it, and BEFORE mapping the fast-path drain (mmap verifies VERSION, which a
	// cold-boot factory fabric fails). Cold boot leaves the factory NAND image in
	// the fabric; method-B reload reconfigures the volatile CRAM to the owned
	// build and verifies it by the interface build-ID. This runs before the
	// analog front end opens the shared passive-serial node (spidev1.1). The
	// identity verify is the safety interlock: even when the reload is skipped it
	// still runs, so the app never drives an unverified fabric.
	loaderSPI := envOr("SCOPE_LOADER_SPIDEV", "/dev/spidev1.1")
	if os.Getenv("SCOPE_SKIP_FPGA_LOAD") == "1" {
		logf("fpgaload: SCOPE_SKIP_FPGA_LOAD=1 — skipping reconfig, verifying identity only")
		if err := iface.Verify(b.Read); err != nil {
			logf("FATAL: fabric identity: %v — refusing to drive", err)
			<-sig
			os.Exit(0)
		}
	} else if err := fpgaload.Bringup(gpmcFD, loaderSPI, b.Read, logf); err != nil {
		logf("FATAL: fpga bringup: %v — refusing to drive", err)
		<-sig
		os.Exit(0)
	}

	// Fabric confirmed to be the owned build — now enable the fast-path drains.
	logf("bus up, mmap drain=%v", b.EnableMmap(mmapDrain))
	// EDMA/sDMA drain: CPU-free ~21 MB/s record drain (vs ~0.8 MB/s ioctl). Sized for
	// the max record depth; falls back to ioctl if EDMA can't initialize.
	logf("edma drain=%v", b.EnableEDMA(20480))

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
		fe.OnOffset(e.SetOffsetDAC)       // offset re-anchors to each detent's cal zero
		fe.OnOffsetV(e.SetChannelOffsetV) // trigger level rides the same offset reference as the samples
		fe.OnVdiv(e.SetChannelVdiv)       // keep the trigger level→display-code map current
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
	pc.SetFrameSource(fo.WithFrame) // let the AUTO button measure the live signal
	uiCtrl.Store(pc)                // publish for the LCD render loop + SCDP screenshot
	go pc.Run(stopFo)
	logf("panel controller up (fpga_key fd=%d)", keyFD)

	// Settings persistence: restore the last user setup now that the engine is
	// up — through the SAME setter paths the panel/web use, so every clamp and
	// side-effect applies — then watch for changes and save them debounced to
	// the U-disk. A missing/corrupt file just means "boot with defaults".
	var setFE settings.Analog
	if fe != nil {
		setFE = fe
	}
	setPath := settings.DefaultPath()
	if st, ok := settings.Load(setPath, logf); ok {
		settings.Apply(st, e, setFE, pc, logf)
		logf("settings: restored %s", setPath)
	} else {
		logf("settings: no saved setup at %s — defaults", setPath)
	}
	saver := settings.NewSaver(setPath, func() settings.Settings {
		return settings.Collect(e, setFE, pc)
	}, logf)
	go saver.Run(stopFo)

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
	// SCPI over VXI-11 (spec 11): the host instrument-control interface.
	// SCDP renders a fresh headless frame — works with or without the LCD.
	// Created BEFORE the web server so its INVS shadow (the display-invert
	// truth) can be wired into the status snapshot; the panel controller
	// backs the SCPI display commands (XYDS/PESU/MENU) with the real state.
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
	scpiH := scpi.New(scopeSource{e, fo}, sfe, pc, shot, logf)
	scpiCtrl.Store(scpiH) // publish for the LCD render loop + screenshot paths
	if _, port, err := vxi11srv.Start(scpiH.HandleLine, true, logf); err != nil {
		logf("WARNING: VXI-11 server failed: %v", err)
	} else {
		logf("scpi: VXI-11 DEVICE_CORE on tcp/%d", port)
	}

	// Deliberately NO Read/Write/Idle timeouts: /api/frame.bin parks requests
	// up to 2 s (long-poll), and a global WriteTimeout would kill every parked
	// request. Slow-peer writes are bounded per-response inside the handler
	// (SetWriteDeadline). Don't "harden" this without moving that contract.
	ws := web.New(scopeSource{e, fo}, feIface, pc, screenPNG)
	ws.SetInvertSource(scpiH.Inverted) // display-level INVS: SCPI shadow → /api/status
	srv := &http.Server{Addr: listen, Handler: ws.Handler()}
	go func() {
		logf("web ui listening on %s", listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logf("web server: %v", err)
		}
	}()

	s := <-sig
	logf("signal %v — stopping engine at frame boundary", s)
	saver.Flush() // a change still inside the debounce window survives the restart
	if !e.Stop(2 * time.Second) {
		logf("WARNING: engine did not stop in time")
	}
	os.Exit(0)
}
