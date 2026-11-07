// ENGMODEL-OWNER-UNIT: FU-APP-APP

package app

import (
	"math"
	"os"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/labeth/open-dso-core/engine/fanout"
	"github.com/labeth/open-dso-core/framecalc"
	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-dso-core/uimodel/wsview"
	"github.com/labeth/open-dso-core/webserver"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/lcd"
	"github.com/labeth/open-sds1000cml/app/internal/panel"
	"github.com/labeth/open-sds1000cml/app/internal/scpi"
	"github.com/labeth/open-sds1000cml/app/internal/streamview"
)

// lastViewWasBode tracks whether the DEVICE's BODE view armed FRA, so leaving
// the view disarms only the device's own auto-arm (a web-armed sweep persists).
var lastViewWasBode atomic.Bool

// lcdSpect is the one "FFT over time" waterfall buffer: the LCD loop pushes new
// spectrum rows into it (SPECTROGRAM view); every render path (panel, the
// /api/screen.png PNG, the SCDP hardcopy) blits it, so all three agree.
var lcdSpect = lcd.NewSpectrogram()

// uiCtrl is the panel controller, published after creation so the LCD render
// loop (started earlier) and the SCDP screenshot can read the workspace UI.
var uiCtrl atomic.Pointer[panel.Controller]

// webSrv is the web server, published after creation so the display loop
// can tell whether a page shows the scope (lcdGov).
var webSrv atomic.Pointer[webserver.Server]

// streamCtrl is the scope-side decoded stream, published for the HUD.
var streamCtrl atomic.Pointer[streamview.Controller]

// scpiCtrl is the SCPI handler, published after creation so the LCD render
// loop and the screenshot paths can read the display-invert (INVS) truth.
var scpiCtrl atomic.Pointer[scpi.Handler]

// renderSig is a scalar fingerprint of everything the static (Y-T / X-Y / FFT)
// views draw. When it is unchanged tick-to-tick the display is identical, so the
// LCD loop skips the rasterize+blit — the heavy, bus-contending part that
// otherwise ran 20×/s even on a held frame. Time-evolving views (persistence,
// Bode, spectrogram, super-res review, an active mask/zone test) bypass this and
// always repaint. All fields are comparable so `==` decides.
// TRLC-LINKS: REQ-SDS-168
type renderSig struct {
	streamOn, streamList                            bool
	streamState, streamTop, streamTotal, streamTrig int
	streamSlot                                      int64
	seq                                             uint64
	waitS                                           int // a waiting trigger's seconds (the screen counts them)
	view, math, dec, zoom                           int
	curType, curSel                                 int
	ui                                              uint64 // the workspace UI (uiSig)
	url                                             string
	tdiv, zoomOff                                   float64
	c1v, c2v, off1, off2                            float64
	trig                                            float64
	curX, curY                                      [2]float64
	probe1, probe2                                  float64
	cpl1, cpl2                                      int
	inv1, inv2                                      bool
	showMeas, persist                               bool
	running, single, norm                           bool
	trigd, live                                     bool
	showC1, showC2                                  bool
	curOn                                           bool
	ref0, ref1                                      bool
	// extra prints the remaining HUD state a held display shows: the autoset
	// banner, the notices, the trigger readout, decode parameters (a softkey
	// that changes only a value, e.g. TRIGGER Type Pulse->Slope while
	// stopped, must not leave the old value on screen).
	extra uint64
}

// statusSlot is what the top bar's status slot shows (lcd.DrawStatusSlot).
type statusSlot struct {
	busy       string
	pct, phase int
}

// uiSig fingerprints the workspace UI for the repaint check. Pointers are
// followed (fmt would print their addresses, new on every snapshot); the
// eye's density grid is left out: it changes only with a new frame, and the
// signature has the frame's Seq.
// TRLC-LINKS: REQ-SDS-168, REQ-SDS-211
func uiSig(v wsview.View) uint64 {
	if v.Eye != nil {
		e := *v.Eye
		e.Grid = nil
		v.Eye = &e
	}
	h := sigOffset
	h.add(reflect.ValueOf(&v).Elem())
	return uint64(h)
}

// waitSecond is the whole seconds a waiting trigger has waited (the
// screen counts them), 0 when nothing waits.
// TRLC-LINKS: REQ-SDS-168
func waitSecond(hud lcd.HUD) int {
	if !hud.Waiting {
		return 0
	}
	return int(hud.WaitS)
}

// TRLC-LINKS: REQ-SDS-168
func renderSigOf(f *sample.Frame, hud lcd.HUD, live bool) renderSig {
	var seq uint64
	if f != nil {
		seq = f.Seq
	}
	return renderSig{
		seq:  seq,
		view: hud.ViewMode, math: hud.MathMode, dec: hud.DecProto, zoom: hud.Zoom,
		curType: hud.CurType, curSel: hud.CurSel, ui: uiSig(hud.UI),
		url:  hud.URL, // an IP appearing/changing must repaint a held display
		tdiv: hud.TdivS, zoomOff: hud.ZoomOff,
		c1v: hud.C1VdivV, c2v: hud.C2VdivV, off1: hud.OffC1V, off2: hud.OffC2V,
		trig: hud.TrigLvlDiv, curX: hud.CurX, curY: hud.CurY,
		probe1: hud.Probe1, probe2: hud.Probe2, cpl1: hud.Cpl1, cpl2: hud.Cpl2,
		inv1: hud.Inv1, inv2: hud.Inv2, // an INVS flip must repaint a held display
		showMeas: hud.ShowMeas, persist: hud.Persist,
		running: hud.Running, single: hud.Single, norm: hud.Norm,
		trigd: hud.Trigd, live: live, showC1: hud.ShowC1, showC2: hud.ShowC2,
		waitS: waitSecond(hud),
		curOn: hud.CurOn,
		ref0:  hud.RefShow[0], ref1: hud.RefShow[1],
		// Decoded stream: its state and the list's page repaint the display;
		// while streaming, a quarter-second slot paces the transcript at 4 Hz
		// so drawing leaves the CPU to the event pump.
		streamOn: hud.StreamOn, streamState: int(hud.Stream.State), streamList: hud.StreamList,
		streamTop: hud.StreamPage.Top, streamTotal: hud.StreamPage.Total, streamTrig: hud.StreamPage.TriggerLine,
		streamSlot: streamSlot(hud),
		extra:      hudExtraSig(&hud),
	}
}

// hudExtraSig fingerprints the rest of the HUD a held display shows,
// value by value: printing them with fmt every tick allocated a string.
// TRLC-LINKS: REQ-SDS-168
func hudExtraSig(hud *lcd.HUD) uint64 {
	h := sigOffset
	flag := func(b bool) {
		if b {
			h.byte1(1)
		} else {
			h.byte1(0)
		}
	}
	num := func(v int) { h.word(uint64(v)) } //nolint:gosec // G115: a hash input: the bits count, not the sign
	flt := func(v float64) { h.word(math.Float64bits(v)) }
	flag(hud.AutosetBusy)
	h.str(hud.AutosetMsg)
	h.str(hud.Remote)
	num(len(hud.MeasItems))
	for _, m := range hud.MeasItems {
		h.str(m)
	}
	num(hud.FFTSrc)
	num(hud.TrigSrc)
	flag(hud.TrigRising)
	flt(hud.TrigLvlV)
	flt(hud.TrigPosFrac)
	flag(hud.Waiting)
	num(hud.TrigType)
	q := hud.TrigQual // a copy: a field's address would move the whole HUD to the heap
	h.add(reflect.ValueOf(&q).Elem())
	flag(hud.SerialArmed)
	h.str(hud.SerialMatch)
	flag(hud.ZoneTrig)
	num(hud.Zoom1)
	num(hud.Zoom2)
	flag(hud.ChanExplicit)
	num(hud.DecBaud)
	num(hud.DecChA)
	num(hud.DecChB)
	flag(hud.DecCPOL)
	flag(hud.DecCPHA)
	num(hud.DecFormat)
	h.str(hud.MaskMsg)
	h.str(hud.SRStatus)
	h.str(hud.Notice)
	m := hud.Match
	h.add(reflect.ValueOf(&m).Elem())
	return uint64(h)
}

// runLCD drives the device panel at the 50 ms display cadence (spec 07 §8 —
// a hard minimum; faster starves the acquisition owner). Fully optional: on
// any bring-up failure the scope keeps running headless.
// TRLC-LINKS: REQ-SDS-168
func runLCD(e *engine.Engine, fe *analog.FrontEnd, fo *fanout.Fanout[sample.Frame]) { //nolint:gocyclo // the LCD loop: one tick's render decisions, read top to bottom in the order they apply
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
	sgSrc := -1                         // the channel the waterfall shows (it restarts on a change)
	var lastSeq uint64
	var lastPresented uint64
	var lastFresh time.Time
	var lastSig renderSig
	var lastSlot statusSlot
	haveSig := false
	// The screen saver (ADR-SCREEN-SAVER): what the LCD shows, when the saver
	// began (its drift is the time since), when it last moved, and the light.
	scr := panel.ScreenOn
	var saverT0, saverAt time.Time
	lit := true
	idle := false // the last tick drew nothing and saw no new frame
	var busyDrawAt time.Time
	var renderDue time.Time // when the next full picture may start
	var govAt time.Time     // when gov last looked
	gov := newLCDGov()
	var pictures, govPictures uint64 // full pictures drawn, and as gov last counted
	var cpu cpuMeter
	wasBusy := false
	for {
		switch {
		case wasBusy:
			time.Sleep(lcdIdleWait) // busy mode: only its own few draws a second
		case idle:
			// Nothing moved: wait for the next frame, or lcdIdleWait for the
			// UI's own changes. Checking at the render cadence (2 ms) built
			// the whole HUD 500 times a second on a still screen.
			fo.WaitNext(lastSeq, max(e.RenderPeriod(), lcdIdleWait))
		default:
			time.Sleep(e.RenderPeriod()) // tunable display cadence
			// never faster than the panel refreshes, nor than the engine's
			// frame rate allows (lcdGovern): a picture costs the engine
			// frames (polled: one long sleep overshoots on this kernel's
			// timer)
			now := time.Now()
			if dt := now.Sub(govAt); dt >= lcdGovEvery {
				ws := webSrv.Load()
				page := ws != nil && ws.FrameClient(lcdPageGone)
				lcd := float64(pictures-govPictures) / dt.Seconds()
				if govAt.IsZero() {
					lcd = 0
				}
				govAt, govPictures = now, pictures
				gov.step(e.Snapshot(), lcd, cpu.busy(), page, scr == panel.ScreenOn && !wasBusy)
				framecalc.SetSpectrumHold(gov.hold)
				e.SetDisplayPace(gov.pace)
			}
			if scr == panel.ScreenOn && now.Before(renderDue) {
				continue
			}
		}
		seenSeq := lastSeq
		// Busy mode: while the engine reads a full record or loads an image,
		// and while AUTO searches, the display draws only what shows the work
		// going on (lcdBusyDraw), a few times a second; one full redraw
		// follows.
		pc := uiCtrl.Load()
		st := e.Snapshot()
		if mode := lcdBusyMode(st, pc != nil && pc.AutosetBusy(), pc != nil && pc.StreamMode()); mode != busyNone && scr == panel.ScreenOn { //nolint:nestif // the busy screen's draw, in place of the workspace's
			if now := time.Now(); now.Sub(busyDrawAt) >= lcdBusyEvery || !wasBusy {
				busyDrawAt, wasBusy = now, true
				e.QuietRLock()
				if lcdBusyDraw(mode, st, back) {
					fb.PresentRows(back, 0, lcd.StatusSlotRows)
				} else {
					hud := buildHUD(e, fe)
					fo.WithFrame(func(f *sample.Frame) {
						lcd.Render(back, f, hud, true, persistLayer)
						if f != nil && f.Seq != lastPresented {
							lastSeq, lastFresh, lastPresented = f.Seq, now, f.Seq
							e.NoteLCDFrame()
						}
					})
					fb.Present(back)
				}
				e.QuietRUnlock()
			}
			continue
		}
		if wasBusy {
			wasBusy, haveSig = false, false // the work is done: one full redraw
		}
		// Render only OUTSIDE the engine's load-sensitive windows (arm-settle +
		// drain): a concurrent render burst there corrupts the HW capture on this
		// single core. This pauses ~19ms/frame; the wait+pace (~90ms) is free.
		e.QuietRLock()
		if pc := uiCtrl.Load(); pc != nil {
			fo.WithFrame(pc.OnFrame) // the workspace pane's analysis, once per new frame
			if s := pc.Screen(); s != scr {
				scr = s
				saverT0, saverAt = time.Now(), time.Time{}
				haveSig = false // back on, the workspace repaints in full
			}
		}
		hud := buildHUD(e, fe)
		present := false
		fo.WithFrame(func(f *sample.Frame) {
			// The publish rate is slower than the render tick, so "fresh"
			// is a short window, not a per-tick flag — otherwise the
			// liveness strip flickers red on every held tick.
			if f != nil && f.Seq != lastSeq {
				lastSeq = f.Seq
				lastFresh = time.Now()
			}
			if f != nil { //nolint:nestif // the frame's fields copied into the HUD only with a frame
				hud.Trigd = f.Status.Triggered // on-screen TRIG'd indicator (there is no TRIG'd lamp)
				hud.SampleS = f.SampleS
				// Zoomed into the live envelope: ask for the zoomed part at
				// full detail (the frame's view window).
				if a, b, ok := scopeViewLease(hud, f); ok {
					e.LeaseViewWindow(engine.ViewScope, a, b, lcd.PlotColumns(hud.UI.Layout), time.Second)
				}
				// Spectrogram: push each fresh per-sample frame's spectrum of the
				// FFT source as a new waterfall row while the SPECTROGRAM view is
				// active; a new source starts a new waterfall.
				if hud.ViewMode == 4 && !f.Flags.Has(sample.Envelope) && len(f.Codes8(0)) > 0 && (f.Seq != sgSeq || hud.FFTSrc&1 != sgSrc) {
					if hud.FFTSrc&1 != sgSrc {
						lcdSpect.Clear()
						sgSrc = hud.FFTSrc & 1
					}
					sgSeq = f.Seq
					effNyq := 0.0
					if f.SampleS > 0 {
						effNyq = 0.5 / f.SampleS
					}
					lcdSpect.Push(f, sgSrc, effNyq)
				}
			}
			// The panel's 40 ms tick keeps the lamps and the remote's snapshot
			// in step; doing it here too doubles their cost.
			live := f != nil && time.Since(lastFresh) < 300*time.Millisecond
			// Asleep, the scope keeps acquiring and analysing; only the
			// workspace's render stops.
			switch scr {
			case panel.ScreenOff: // black once, then the light goes off
				if lit {
					back.Fill(0)
					present = true
				}
				return
			case panel.ScreenSaver:
				if now := time.Now(); now.Sub(saverAt) >= lcd.SaverStep {
					saverAt = now
					lcd.DrawSaver(back, f, hud, int(now.Sub(saverT0)/lcd.SaverStep))
					present = true
				}
				return
			case panel.ScreenOn: // the workspace renders, below
			}
			// Repaint only when the picture changed.
			force := lcdForceRepaint(hud)
			sig := renderSigOf(f, hud, live)
			slot := statusSlot{hud.Busy, int(hud.BusyPct), hud.BusyPhase}
			// Stopped and the engine is re-showing the held record at the new
			// view: drawing the old frame at the new scale first only delays it.
			stale := haveSig && hud.ViewPending && f != nil && f.Seq == lastSig.seq
			if haveSig && (stale || !force && sig == lastSig) {
				// Only the loading indicator moved: redraw its slot alone.
				if slot != lastSlot {
					lastSlot = slot
					lcd.RedrawStatusSlot(back, hud)
					present = true
				}
				return // nothing else visible changed — skip the rasterize
			}
			lastSig, haveSig, lastSlot = sig, true, slot
			// the next picture is due a period after this one was: a start
			// late by the poll or the engine's quiet window is not added to
			// the next, and a long pause does not bring a burst
			now := time.Now()
			renderDue = renderDue.Add(gov.period)
			if renderDue.Before(now) {
				renderDue = now.Add(gov.period / 2)
			}
			if hud.StreamOn && hud.StreamList && renderDue.Before(now.Add(lcdStreamPeriod)) {
				renderDue = now.Add(lcdStreamPeriod) // the transcript's text: a few pictures a second read it
			}
			pictures++
			lcd.Render(back, f, hud, live, persistLayer)
			present = true
		})
		if present {
			fb.Present(back)
			if scr == panel.ScreenOn && lastSeq != lastPresented {
				e.NoteLCDFrame()
				lastPresented = lastSeq
			}
		}
		// The light follows the picture: off once the black frame is up, on
		// once the next picture is.
		if want := scr != panel.ScreenOff; want != lit && (present || !want) {
			if err := lcd.SetBacklight(want); err != nil {
				logf("lcd: backlight: %v", err)
			}
			lit = want
		}
		idle = !present && lastSeq == seenSeq
		e.QuietRUnlock()
	}
}

// lcdSoundView is the SOUND view's ViewMode (panel's viewSound).
const lcdSoundView = 5

// lcdForceRepaint reports a picture that changes without a frame or a UI
// change the render signature sees, which bypasses the repaint cache:
// afterglow decay, the Bode sweep, the spectrogram waterfall, super-res
// review, a running mask/zone test whose live counters advance faster than
// the published frame seq. The SOUND view is not one: its pane is in the
// signature and changes with each of the sound service's readings; forced,
// it was redrawn as fast as the core allowed (a fifth of it) and the audio
// stream's listeners ran dry.
// TRLC-LINKS: REQ-SDS-168, REQ-SDS-273
func lcdForceRepaint(hud lcd.HUD) bool {
	return hud.Persist || (hud.ViewMode >= 3 && hud.ViewMode != lcdSoundView) || hud.SRActive ||
		hud.MaskMode > 0 || hud.ZoneMode > 0
}

// lcdIdleWait bounds how long a still display waits for something to change
// that brings no frame (a menu, a notice, the loading indicator).
const lcdIdleWait = 20 * time.Millisecond

// lcdFramePeriod is the panel's refresh period (800x480 at 52 Hz): the
// display loop puts up at most one picture a refresh.
const lcdFramePeriod = time.Second / 52

// The display shares the one core with the engine: a picture (render and
// copy, ~6 ms; in SPECTRUM two 8192-point spectra more, ~25) costs it
// frames, measured 53 fps with the panel at 51 and 64 with it at 32. Who
// gets the core depends on who is looking (lcdGov.step).
const (
	lcdEngineFPS     = 61
	lcdEngineRoom    = 64
	lcdSlowestPeriod = time.Second / 20 // the panel looks live down to 20 pictures a second
	lcdLongestHold   = lcdSlowestPeriod
	lcdLongestPace   = time.Second / 20 // the engine runs at 20 frames a second or more
	lcdCoreBusy      = 0.90
	lcdCoreWaiting   = 0.85
	lcdGovStep       = 2 * time.Millisecond
	lcdHoldStep      = 10 * time.Millisecond
	lcdPaceStep      = time.Millisecond
	lcdGovEvery      = 250 * time.Millisecond
	// lcdPageGone is how long after its last frame request a page counts
	// as gone (it polls at least every 1.5 s while it shows the scope).
	lcdPageGone = 3 * time.Second
)

// lcdGov is the display's share of the core: its period, how long it keeps
// a spectrum (framecalc.SetSpectrumHold) and the least time it asks an
// engine capture to take (engine.SetDisplayPace).
type lcdGov struct {
	period, hold, pace time.Duration
	lcd                float64 // pictures a second, smoothed
}

// newLCDGov is the governor at the panel's rate, the engine unpaced.
// TRLC-LINKS: REQ-SDS-168
func newLCDGov() *lcdGov { return &lcdGov{period: lcdFramePeriod} }

// step looks once at the engine, the pictures drawn a second since the
// last look (lcd), the core's busy fraction, whether a web page shows the
// scope and whether the display draws the engine's frames at all (the
// screen on, not in its busy mode): asleep or busy it draws few or none on
// purpose, and that is no reason to pace the engine.
//
// With no page the panel is the only view and gets the core: pictures at
// the panel's rate, a new spectrum each. A picture needs a new frame, so
// while the display falls behind the engine's frames on a busy core the
// engine is paced (never under 20 frames a second) until it keeps up.
//
// With a page the engine's rate is what the page sees: while the engine is
// under lcdEngineFPS on a busy core the display's period grows, then it
// keeps each spectrum for later pictures; with room above lcdEngineRoom, or
// the core waiting (a record that takes longer than a 60th of a second to
// come), they come back. Never below 20 pictures a second, though: under
// that the panel stops looking live, worth more than the engine's frames,
// and a display still under it paces the engine.
// TRLC-LINKS: REQ-SDS-168
func (g *lcdGov) step(st engine.Stats, lcd, busy float64, page, drawing bool) {
	g.lcd = (g.lcd + lcd) / 2
	if !st.Running {
		*g = lcdGov{period: lcdFramePeriod}
		return
	}
	if !drawing {
		g.pace = 0
		return
	}
	busyCore := busy >= lcdCoreBusy
	if !page {
		g.period, g.hold = lcdFramePeriod, 0
		switch {
		case busyCore && g.lcd < min(st.FPS, 52)-2:
			g.pace = min(g.pace+lcdPaceStep, lcdLongestPace)
		case g.lcd >= st.FPS-1 || busy < lcdCoreWaiting:
			g.pace = max(g.pace-lcdPaceStep, 0)
		}
		return
	}
	switch {
	case st.FPS < lcdEngineFPS && busyCore:
		if g.period < lcdSlowestPeriod {
			g.period = min(g.period+lcdGovStep, lcdSlowestPeriod)
		} else {
			g.hold = min(g.hold+lcdHoldStep, lcdLongestHold)
		}
	case st.FPS >= lcdEngineRoom || busy < lcdCoreWaiting:
		if g.hold > 0 {
			g.hold = max(g.hold-lcdHoldStep, 0)
		} else {
			g.period = max(g.period-lcdGovStep, lcdFramePeriod)
		}
	}
	// paced only while the display falls behind the frames it is given: a
	// paced engine's frames bound the display too
	switch {
	case g.lcd < 19.5 && g.lcd < st.FPS-2 && g.period >= lcdSlowestPeriod && busyCore:
		g.pace = min(g.pace+lcdPaceStep, lcdLongestPace)
	case g.lcd > 21 || g.lcd >= st.FPS-1:
		g.pace = max(g.pace-lcdPaceStep, 0)
	}
}

// cpuMeter reads the core's busy fraction from /proc/stat.
type cpuMeter struct{ total, idle uint64 }

// busy is the fraction of the core's time not idle (nor waiting on I/O)
// since the last call; 1 when /proc/stat cannot be read, so the governor
// acts on the frame rate alone.
// TRLC-LINKS: REQ-SDS-168
func (m *cpuMeter) busy() float64 {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 1
	}
	total, idle, ok := cpuTimes(b)
	if !ok {
		return 1
	}
	dt, di := total-m.total, idle-m.idle
	m.total, m.idle = total, idle
	if dt == 0 || dt < di {
		return 1
	}
	return 1 - float64(di)/float64(dt)
}

// cpuTimes sums the first "cpu" line of /proc/stat: all its times, and the
// idle and iowait ones.
// TRLC-LINKS: REQ-SDS-168
func cpuTimes(b []byte) (total, idle uint64, ok bool) {
	if len(b) < 4 || string(b[:4]) != "cpu " {
		return 0, 0, false
	}
	field, v, in := 0, uint64(0), false
	for _, c := range b[4:] {
		if c >= '0' && c <= '9' {
			v, in = v*10+uint64(c-'0'), true
			continue
		}
		if in {
			total += v
			if field == 3 || field == 4 {
				idle += v
			}
			field++
			v, in = 0, false
		}
		if c == '\n' {
			break
		}
	}
	return total, idle, field >= 5
}

// lcdBusyEvery is the busy mode's drawing period: the work shown going on
// a few times a second, the core left to the work.
const lcdBusyEvery = 250 * time.Millisecond

// lcdStreamPeriod is the least time between pictures while the decoded
// stream's transcript fills the screen: its lines scroll at text speed, and
// the full rate (52 a second) cost the core a tenth for nothing seen.
const lcdStreamPeriod = time.Second / 10

// The display's busy modes (lcdBusyMode).
const (
	busyNone    = iota
	busyLoad    // a full-record read or an image switch: the loading indicator alone
	busyAutoset // AUTO's search: the whole screen, with its banner, a few times a second
)

// lcdBusyMode is the display's busy mode: the engine's loading work (shown
// after the indicator's grace, so a quick read never enters it), else
// AUTO's search. A decoded stream is never one: its live view is the point.
// TRLC-LINKS: REQ-SDS-168
func lcdBusyMode(st engine.Stats, autoset, streaming bool) int {
	switch {
	case streaming:
		return busyNone
	case st.Busy != "" && st.BusyMs >= 150:
		return busyLoad
	case autoset:
		return busyAutoset
	}
	return busyNone
}

// lcdBusyDraw draws the loading indicator alone into back and reports true
// for busyLoad; for busyAutoset it draws nothing and reports false (the
// caller renders the whole screen).
// TRLC-LINKS: REQ-SDS-168
func lcdBusyDraw(mode int, st engine.Stats, back *lcd.MemSurface) bool {
	if mode != busyLoad {
		return false
	}
	lcd.RedrawStatusSlot(back, lcd.HUD{Busy: st.Busy, BusyPct: st.BusyPct, BusyPhase: int(st.BusyMs / 250), URL: lcd.DeviceURL()})
	return true
}

// renderScreen renders the LCD picture headlessly (the /api/screen.png PNG,
// the SCDP hardcopy, PRINT): the workspace analysis of the frame first, so
// the pane describes the frame drawn.
// TRLC-LINKS: REQ-SDS-168, REQ-SDS-211
func renderScreen(e *engine.Engine, fe *analog.FrontEnd, fo *fanout.Fanout[sample.Frame]) *lcd.MemSurface {
	if pc := uiCtrl.Load(); pc != nil {
		fo.WithFrame(pc.OnFrame)
	}
	back := lcd.NewMemSurface()
	hud := buildHUD(e, fe)
	fo.WithFrame(func(f *sample.Frame) {
		if f != nil {
			hud.SampleS = f.SampleS
			hud.Trigd = f.Status.Triggered
		}
		lcd.Render(back, f, hud, true)
	})
	return back
}

// streamSlot is a quarter-second counter while a decoded stream runs (0
// otherwise): the display's 4 Hz transcript refresh.
// scopeViewLease is the stretch of live envelope frame f the LCD's zoom
// shows, in screen widths from the trigger, while zoomed in Y-T (X-Y draws no
// zoomed stretch, and its reads cost frames).
// TRLC-LINKS: REQ-SDS-021
func scopeViewLease(hud lcd.HUD, f *sample.Frame) (a, b float64, ok bool) {
	if hud.Zoom <= 1 || hud.ViewMode != 0 || f == nil || f.Content != sample.PeakPairs || f.WinPoints <= 0 || f.Len() <= 0 {
		return 0, 0, false
	}
	c := hud.ZoomOff * float64(f.Len()) / float64(min(f.WinPoints, f.Len()))
	h := 0.5 / float64(hud.Zoom)
	return c - h, c + h, true
}

// TRLC-LINKS: REQ-SDS-018
func streamSlot(hud lcd.HUD) int64 {
	if hud.StreamOn && (hud.Stream.State == streamview.Streaming || hud.Stream.State == streamview.Starting) {
		return time.Now().UnixMilli() / 250
	}
	return 0
}

// streamPageKey is what a cached transcript page depends on.
type streamPageKey struct {
	state      streamview.State
	list       bool
	top, lines int
	trig       int
	slot       int64
}

type cachedStreamPage struct {
	key  streamPageKey
	page streamview.Page
}

// lastStreamPage is buildHUD's cached transcript page.
var lastStreamPage atomic.Pointer[cachedStreamPage]
