// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
)

// Run is the panel event loop: SIGIO (knobs + buttons, rate-capped ~150 Hz)
// plus the MANDATORY 40 ms re-sync tick (buttons only — a timer-driven read
// lands mid-detent and misreads quadrature). Blocks; run as a goroutine.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) Run(stop <-chan struct{}) {
	// Adopt the engine's restored setup; starting the panel must not overwrite it.
	c.mu.Lock()
	c.adoptQual(c.eng.Snapshot())
	c.mu.Unlock()
	c.looping.Store(true) // the remote's changes now run on this goroutine
	defer c.looping.Store(false)

	sigio := make(chan os.Signal, 8)
	haveSIGIO := false
	if c.keyFD >= 0 {
		signal.Notify(sigio, syscall.SIGIO)
		if err := armSIGIO(c.keyFD); err != nil {
			c.logf("panel: SIGIO arming failed (%v) — poll fallback", err)
		} else {
			haveSIGIO = true
		}
	} else {
		c.logf("panel: no inherited /dev/fpga_key fd — poll fallback")
	}

	// Seed the decoder baseline (prevents fabricated first presses).
	if m, ok := c.eng.ReadMatrix(); ok {
		c.prev, c.havePrev = m, true
	}
	c.pushLEDs()

	// ADR-PANEL-SERIAL-SCAN: a matrix built from knob counts may be decoded
	// on every read.
	synthesized := false
	if s, ok := c.eng.(interface{ MatrixSynthesized() bool }); ok {
		synthesized = s.MatrixSynthesized()
	}
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	// trailing fires shortly after a rate-limited interrupt so the LAST
	// detent of a fast knob spin (whose interrupt lands inside the 6 ms cap)
	// is still read with knob decode enabled — a plain drop would lose it.
	trailing := time.NewTimer(time.Hour)
	trailing.Stop()
	defer trailing.Stop()
	var lastSig time.Time
	var pendingTrail bool
	for {
		select {
		case <-stop:
			return
		case fn := <-c.inject:
			fn() // API-injected button/knob, run on the panel goroutine
		case <-sigio:
			if time.Since(lastSig) < 6*time.Millisecond {
				if !pendingTrail {
					pendingTrail = true
					trailing.Reset(8 * time.Millisecond)
				}
				continue
			}
			lastSig, pendingTrail = time.Now(), false
			if m, ok := c.eng.ReadMatrix(); ok {
				c.decode(m, true)
			}
		case <-trailing.C:
			pendingTrail = false
			lastSig = time.Now()
			if m, ok := c.eng.ReadMatrix(); ok {
				c.decode(m, true) // knob decode: catches the burst's last detent
			}
		case <-tick.C:
			// Lamps follow engine-side changes (a SINGLE self-stopping, web/SCPI
			// run toggles) within a tick, whatever the LCD render costs.
			c.SyncLEDs()
			if m, ok := c.eng.ReadMatrix(); ok {
				// Fallback mode (no SIGIO) decodes knobs on the tick too —
				// the deliberate exception that accepts mid-detent reads. A
				// matrix synthesized from knob counts has no mid-detent phase.
				c.decode(m, !haveSIGIO || synthesized)
			}
		}
	}
}

// TRLC-LINKS: REQ-SDS-135
func armSIGIO(fd int) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETOWN, uintptr(syscall.Getpid())); errno != 0 {
		return errno
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags|syscall.O_ASYNC); errno != 0 {
		return errno
	}
	return nil
}

// decode processes one matrix snapshot: button 1→0 edges always; knob
// quadrature only on interrupt-aligned reads (spec 08 §1/§3).
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) decode(m [5]uint16, knobsOn bool) {
	if !c.havePrev {
		c.prev, c.havePrev = m, true
		return
	}
	for i := 0; i < 4; i++ {
		pressed := (c.prev[i] &^ m[i]) &^ uint16(knobPhaseMask)
		for bit := 0; bit < 16; bit++ {
			if pressed&(1<<uint(bit)) != 0 {
				c.button(bcode(i, bit))
			}
		}
	}
	if knobsOn {
		c.knob(m)
	}
	c.prev = m
}

// TRLC-LINKS: REQ-SDS-135
func (c *Controller) button(code int) {
	// While an autoset sweep runs, ignore everything except AUTO (which cancels)
	// — this both gives a clean "busy" UX and avoids racing its scale changes.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy && code != btnAuto {
		return
	}
	defer c.afterInput()
	switch code {
	case btnRunStop:
		if c.streaming() { // Auto Stream: STOP ends the stream (manual trigger), RUN restarts it
			c.mu.Lock()
			s := c.stream
			c.mu.Unlock()
			if s.Active() {
				c.stopStream()
			} else {
				c.startStream()
			}
			return
		}
		// Toggle RUN/STOP from the engine's state, not the shadow: a single that
		// self-stopped moments ago may not be synced into the shadow yet.
		c.setRun(map[bool]int{true: 1, false: 0}[c.eng.Snapshot().Running])
	case btnSingle:
		c.single1() // Normal + one triggered capture, then stop
	case btnAuto:
		c.runAuto()
	case btnDefaultSetup:
		c.defaultSetupUI()
	case btnSet50:
		c.setTrigMid()
	case btnForce:
		c.force()
	case btnPrint:
		c.printScreen()
	case btnCh1VdivPush, btnCh2VdivPush:
		c.variablePush(map[bool]int{true: 1, false: 0}[code == btnCh2VdivPush])
	case btnCh1PosPush, btnCh2PosPush:
		ch := map[bool]int{true: 1, false: 0}[code == btnCh2PosPush]
		if c.fe != nil {
			c.setOffsetTip(ch, 0) // Zero: the trace back to 0 V
			c.toast(fmt.Sprintf("CH%d offset 0 V", ch+1), 900*time.Millisecond)
		}
	case btnTrigLvlPush:
		c.zeroTrigLevel() // Zero: the trigger level to 0 V
		c.toast("Trigger level 0 V", 900*time.Millisecond)
	case btnTdivPush:
		c.tdivPush()
	case btnHorizPosPush:
		c.positionPush()
	case btnAdjustPsh:
		c.adjustPush()
	case btnMenuOnOff:
		c.menuKey()
	case btnHelp:
		c.helpKey()
	case btnCh1, btnCh2:
		c.chanKey(map[bool]int{true: 1, false: 0}[code == btnCh2])
	default:
		for i, sk := range softkeys {
			if code == sk {
				c.fKey(i)
				return
			}
		}
		if name, ok := tabKeys[code]; ok {
			c.mu.Lock()
			c.closePickLocked()
			c.mu.Unlock()
			c.keyTab(name)
		}
		// anything else is claimed and ignored, so it cannot cross-drive a control
	}
}

// resync refreshes the knob shadows from authoritative state before a step,
// so a step lands relative to whatever the web UI / SCPI last set — not a
// stale panel-local value (which would snap the setting on the first click).
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) resync() {
	// Autoset owns the shadows while it sweeps (it writes them under mu on its own
	// goroutine). Skip here so the injected-knob path can't race it either.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy {
		return
	}
	st := c.eng.Snapshot()
	if st.TrigCode != 0 {
		c.trigCode = st.TrigCode
	}
	c.mu.Lock()
	c.running, c.norm, c.single = st.Running, st.Norm, st.Single // guarded — pushLEDs reads these
	c.mu.Unlock()
	for i, t := range c.tdivs {
		if st.TdivS > 0 && absf(t-st.TdivS) <= t*1e-6 {
			c.tdivIdx = i
			break
		}
	}
	if c.fe != nil {
		idx, _ := c.fe.Snapshot()
		c.vIdx = idx
	}
}

// TRLC-LINKS: REQ-SDS-135
func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// knob services AT MOST ONE knob per event, walking the fixed priority order
// (the cross-coupling fix). Gate: 0x69 == 0 means a plain button interrupt.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) knob(m [5]uint16) {
	raw := m[4]
	if raw == 0 {
		return
	}
	// Autoset owns the shadows while it sweeps; resync() below writes them
	// unguarded, so the busy check MUST be here (before resync), not just in
	// dispatch() — otherwise a knob turn mid-sweep races the autoset goroutine.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy {
		return
	}
	c.resync()
	for _, k := range knobs {
		w := m[k.selIdx]
		loDown := w&(1<<k.bitLo) == 0
		hiDown := w&(1<<k.bitHi) == 0
		if !loDown && !hiDown {
			continue
		}
		dir := 1
		if hiDown {
			dir = -1
		}
		steps := 1
		if !k.stepped {
			steps = accel(raw)
		}
		c.dispatch(k.name, dir, steps)
		return // exactly one knob per interrupt
	}
}

// TRLC-LINKS: REQ-SDS-135
func (c *Controller) dispatch(name string, dir, steps int) {
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy { // knobs are inert while autoset sweeps (avoids racing its writes)
		return
	}
	defer c.afterInput()
	switch name {
	case "tdiv":
		// CW (+1) → faster timebase, toward "nS" on the knob's printed arc
		// ("S ... nS"); a sheet turns its tab instead, a zoom its factor.
		c.tdivTurn(dir)
	case "ch1vdiv", "ch2vdiv":
		ch := 0
		if name == "ch2vdiv" {
			ch = 1
		}
		if c.fe == nil {
			return // claim-and-ignore without an analog front end
		}
		// CW (+1) → smaller V/div, toward "mV" on the knob's printed arc
		// ("V ... mV"); the detents are ascending. It turned the other way,
		// against the marking.
		c.vIdx[ch] = clampInt(c.vIdx[ch]-dir, 0, len(analog.Detents)-1)
		if err := c.fe.SetVdiv(ch, c.vIdx[ch]); err != nil {
			c.logf("panel: SetVdiv: %v", err)
		}
	case "ch1pos", "ch2pos":
		ch := 0
		if name == "ch2pos" {
			ch = 1
		}
		if c.fe == nil {
			return // no analog front end: offset knob claim-and-ignore
		}
		// Offset step is 0.2 displayed div per accel-step on every detent;
		// positive steps raise the trace. A fixed 20 DAC codes was 0.2 div at
		// 1 V/div but 2 div at 2 mV/div and 0.02 div at 10 V/div.
		idx, _ := c.fe.Snapshot()
		v := c.fe.OffsetReqV(ch) + float64(dir*steps)*panelOffsetStepDiv*analog.Detents[idx[ch]].VdivV
		c.fe.SetOffset(ch, v)
	case "triglevel":
		// Sign trap: CW RAISES the level, which LOWERS the code
		// (−911 codes/V). The step is 40 codes per accel step at 1 V/div,
		// scaled by the source's displayed V/div (at least one code): a fixed
		// 40 codes was 22 div at 2 mV/div.
		nc := int(c.trigCode) - dir*c.trigStepCodes()*steps
		nc = clampInt(nc, engine.TrigCodeMin, engine.TrigCodeMax)
		c.trigCode = uint16(nc)
		c.eng.SetTrigLevelCode(c.trigCode)
	case "adjust":
		// The workspace's tool, the sheet's row, or the picker's byte.
		c.adjustTurn(dir, steps)
	case "horizpos":
		// The workspace's tool, the sheet's value, or a stopped stream's list.
		c.positionTurn(dir, steps)
	}
}

// panelOffsetStepDiv is the position knob's step in displayed divisions.
const panelOffsetStepDiv = 0.2

// panelTrigStepCodesPerVdiv is the trigger-level knob's step in trigger codes
// per volt of the source channel's V/div (40 codes at 1 V/div, 0.044 div).
const panelTrigStepCodesPerVdiv = 40.0

// trigStepCodes is the trigger-level knob step for the current source.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) trigStepCodes() int {
	if c.fe == nil {
		return int(panelTrigStepCodesPerVdiv)
	}
	idx, _ := c.fe.Snapshot()
	src := c.eng.Snapshot().TrigSource & 1
	return max(1, int(math.Round(panelTrigStepCodesPerVdiv*analog.Detents[idx[src]].VdivV)))
}

// setTrigMid is SET TO 50%: the trigger level at the midpoint of the source
// channel's latest frame.
// TRLC-LINKS: REQ-SDS-135
func (c *Controller) setTrigMid() {
	m, ok := c.measureChans()
	src := c.eng.Snapshot().TrigSource & 1
	if !ok || !has(m[src]) {
		return
	}
	mid := (m[src].Vmax + m[src].Vmin) / 2
	codeF := 31437 - 911*mid
	if c.fe != nil {
		codeF = c.fe.TrigCode(mid, src)
	}
	code := uint16(min(max(math.Round(codeF), engine.TrigCodeMin), engine.TrigCodeMax))
	c.mu.Lock()
	c.trigCode = code
	c.mu.Unlock()
	c.eng.SetTrigLevelCode(code)
}
