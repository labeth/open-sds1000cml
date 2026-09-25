// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"fmt"
	"time"

	"open-sds/app/internal/bus"
	"open-sds/app/internal/iface"
)

// ReadMatrix is the panel's key-matrix request. The factory fabric decoded the
// front-panel matrix behind CS1 selectors 0x64..0x69; the acq2 default image
// has no panel block (workplan §2 puts SNOOP/DIAG there), so there is nothing
// to read: ok=false, and the panel keeps its poll fallback. Physical buttons
// are therefore inactive under this image — /api/panel injection and SCPI
// remain the control paths. Logged once at engine start.
// TRLC-LINKS: REQ-SDS-022
func (e *Engine) ReadMatrix() ([5]uint16, bool) { return [5]uint16{}, false }

// SetLEDs stages the panel LED latch word (MAX V, CS3 0x09..0x0b): compare-on-
// change with an init flag; the owner flushes the 4-write strobe at the boundary.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-022
func (e *Engine) SetLEDs(word uint16) {
	e.mu.Lock()
	if !e.ledInit || word != e.ledWord {
		e.ledWord, e.ledDirty, e.ledInit = word, true, true
	}
	e.mu.Unlock()
}

// Beats is the liveness heartbeat for the OTA health contract: it advances on
// every loop iteration AND inside every legitimate long wait (holdoff pacing,
// budget polls, recovery bring-up, the parked states). The health token keys
// on THIS, not on frame count alone.
// TRLC-LINKS: REQ-SDS-025
func (e *Engine) Beats() uint64 {
	if e.sram != nil {
		return e.beatN.Load() + e.sram.Beats()
	}
	return e.beatN.Load()
}

// sleepBeating sleeps d in ≤500 ms slices, beating each slice so long pacing
// stays visibly alive to the supervisor; aborts early on a stop request.
// TRLC-LINKS: REQ-SDS-008, REQ-SDS-025
func (e *Engine) sleepBeating(d time.Duration) {
	for d > 0 && !e.stopReq.Load() {
		s := d
		if s > 500*time.Millisecond {
			s = 500 * time.Millisecond
		}
		e.clk.Sleep(s)
		e.beatN.Add(1)
		d -= s
	}
}

// ---- register words ----

// runWord is the RUN register: MODE (auto/norm), RUN=1, STREAM on the roll
// band (the gapless ring the roll display chases). Envelope and roll always
// run auto — they are untriggered by construction.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func (e *Engine) runWord() uint16 {
	mode := uint16(0)
	k := e.band.Kind()
	if e.normNow() && k != KindEnvelope && k != KindRoll {
		mode = 1
	}
	w := mode<<iface.RunModeShift | iface.RunRunMask
	if k == KindRoll {
		w |= iface.RunStreamMask
	}
	return w
}

// acqCtrlWord is ACQ_CTRL: encode on at the fabric base rate, all five pairs
// enabled, the software-trigger hysteresis, and the trigger source/slope.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) acqCtrlWord() uint16 {
	w := iface.AcqCtrlEncEnMask |
		uint16(encRate)<<iface.AcqCtrlEncRateShift |
		uint16(trigHyst)<<iface.AcqCtrlTrigHystShift |
		iface.AcqCtrlPairEnMask
	if e.trigSrc.Load() == 1 {
		w |= iface.AcqCtrlTrigSrcMask
	}
	if !e.trigRising.Load() {
		w |= iface.AcqCtrlTrigSlopeMask
	}
	return w
}

// trigLevelWord is TRIG_LEVEL: the trigger level in sample codes (the same
// display-code mapping the software anchor uses, so the fabric fires where
// the trace crosses the marker) and HW_SEL when the A12 comparator is chosen.
// TRLC-LINKS: REQ-SDS-011
func (e *Engine) trigLevelWord() uint16 {
	lvl := e.trigDispLevel(int(e.trigSrc.Load()))
	if lvl < 0 {
		lvl = trigLevelUnset
	}
	w := uint16(lvl) & iface.TrigLevelLevelMask
	if e.tuneHwTrig.Load() {
		w |= iface.TrigLevelHwSelMask
	}
	return w
}

// flushTrigWords writes ACQ_CTRL / TRIG_LEVEL when they differ from the last
// words written (compare-on-change; force rewrites both). Returns whether
// anything was written.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-011
func (e *Engine) flushTrigWords(force bool) bool {
	acq, lvl := e.acqCtrlWord(), e.trigLevelWord()
	wrote := false
	if force || !e.shadowInit || acq != e.acqShadow {
		e.w(iface.SelAcqCtrl, acq)
		e.acqShadow = acq
		wrote = true
	}
	if force || !e.shadowInit || lvl != e.trigShadow {
		e.w(iface.SelTrigLevel, lvl)
		e.trigShadow = lvl
		wrote = true
	}
	e.shadowInit = true
	return wrote
}

// ---- raw access (owner goroutine only) ----

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) w(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS1, sel, val); err != nil {
		e.busErr(err)
	}
}

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) w3(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS3, sel, val); err != nil {
		e.busErr(err)
	}
}

// TRLC-LINKS: REQ-SDS-001
func (e *Engine) r(sel uint16) uint16 {
	v, err := e.b.Read(bus.PlaneCS1, sel)
	if err != nil {
		e.busErr(err)
	}
	return v
}

// TRLC-LINKS: REQ-SDS-127
func (e *Engine) busErr(err error) {
	e.mu.Lock()
	e.stats.BusErrors++
	n := e.stats.BusErrors
	e.mu.Unlock()
	if n <= 5 || n%100 == 0 {
		e.logf("engine: bus error #%d: %v", n, err)
	}
}

// ---- identity ----

// checkIdentity reads the four identity words and compares them with the
// generated interface. The engine refuses to drive any other fabric.
// TRLC-LINKS: REQ-SDS-004
func (e *Engine) checkIdentity() error {
	rd := func(sel uint16) (uint16, error) { return e.b.Read(bus.PlaneCS1, sel) }
	lo, err := rd(iface.SelBuildidLo)
	if err != nil {
		return err
	}
	hi, err := rd(iface.SelBuildidHi)
	if err != nil {
		return err
	}
	ver, err := rd(iface.SelVersion)
	if err != nil {
		return err
	}
	fab, err := rd(iface.SelFabricId)
	if err != nil {
		return err
	}
	return iface.CheckIdentity(lo, hi, ver, fab)
}

// ---- diagnostic access through the owner ----

// ErrExecTimeout is returned by Exec when the owner did not reach a service
// point within the timeout (a long capture, or a stopped engine mid-sleep).
var ErrExecTimeout = fmt.Errorf("engine: exec timeout (owner busy)")

// ErrExecStopped is returned when the engine has exited.
var ErrExecStopped = fmt.Errorf("engine: stopped")

// TRLC-LINKS: REQ-SDS-001, REQ-SDS-127
type execReq struct {
	fn   func(bus.Bus) error
	done chan error
}

// Exec runs fn on the owner goroutine with the bus — the ONE door through
// which the diagnostic block (or anything else) reaches the fabric. It runs
// at the next service point: the frame boundary, the mid-frame pumps of the
// long envelope/roll loops, the STOP sleep, and the parked states (identity
// failure), so diagnostics work on a fabric the engine refuses to drive. fn
// must not block. The result (or ErrExecTimeout) is returned to the caller.
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-127
func (e *Engine) Exec(fn func(bus.Bus) error, timeout time.Duration) error {
	if e.sram != nil {
		return fmt.Errorf("legacy fabric diagnostics unavailable on SRAM ABI")
	}
	req := execReq{fn: fn, done: make(chan error, 1)}
	select {
	case e.execReq <- req:
	case <-e.done:
		return ErrExecStopped
	case <-time.After(timeout):
		return ErrExecTimeout
	}
	select {
	case err := <-req.done:
		return err
	case <-e.done:
		return ErrExecStopped
	case <-time.After(timeout):
		return ErrExecTimeout
	}
}

// serviceExec drains every queued Exec request (owner goroutine only).
// TRLC-LINKS: REQ-SDS-001, REQ-SDS-127
func (e *Engine) serviceExec() {
	for {
		select {
		case req := <-e.execReq:
			func() {
				defer func() {
					if r := recover(); r != nil {
						req.done <- fmt.Errorf("engine: exec panic: %v", r)
					}
				}()
				req.done <- req.fn(e.b)
			}()
			e.beatN.Add(1)
		default:
			return
		}
	}
}

// ---- wedge ladder ----

// TRLC-LINKS: REQ-SDS-128
func (e *Engine) resetDeadRuns() {
	e.deadRuns = 0
	e.mu.Lock()
	e.stats.DeadRuns = 0
	e.mu.Unlock()
}

// deadEvidence walks the wedge-recovery ladder (app spec 03 §11): re-assert
// bring-up every 10 dead frames; at 50, mark Wedged — which stops the health
// token so the agent relaunches us on the still-live fd. On the drain path
// (certain=false) a healthy-but-flat input is indistinguishable from a wedge
// by fill+ptp alone, so Wedged additionally requires a dead fabric: CONF_DONE
// (CS3 0x07 bit7, the MAX V configuration port — a read never disturbs it)
// reading clear. Otherwise we keep re-asserting bring-up and surface DeadRuns
// instead of crash-looping a healthy app.
// TRLC-LINKS: REQ-SDS-128
func (e *Engine) deadEvidence(certain bool) {
	e.deadRuns++
	e.mu.Lock()
	e.stats.DeadRuns = e.deadRuns
	e.mu.Unlock()
	if e.deadRuns%10 != 0 {
		return
	}
	e.logf("engine: %d dead frames (fill frozen, flat drain) — re-asserting bring-up", e.deadRuns)
	e.beatN.Add(1)
	e.bringUp()
	e.beatN.Add(1)
	if e.deadRuns%50 != 0 {
		return
	}
	if certain {
		e.logf("engine: %d dead frames at a decimated band — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	if v, err := e.b.Read(bus.PlaneCS3, cs3ConfStatus); err == nil && v&0x80 == 0 {
		e.logf("engine: CONF_DONE lost after %d dead frames — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	e.logf("engine: %d dead frames but CONF_DONE high — flat input or partial wedge; continuing with periodic bring-up", e.deadRuns)
}
