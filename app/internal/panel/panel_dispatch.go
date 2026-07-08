package panel

import (
	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Run is the panel event loop: SIGIO (knobs + buttons, rate-capped ~150 Hz)
// plus the MANDATORY 40 ms re-sync tick (buttons only — a timer-driven read
// lands mid-detent and misreads quadrature). Blocks; run as a goroutine.
func (c *Controller) Run(stop <-chan struct{}) {
	// Push the qualifier shadows to the engine once so the pgTrigQ page and the
	// engine agree from boot (inert until a non-Edge trigger type is selected).
	c.eng.SetPulseParams(c.pulseLvl, c.pulseMin, c.pulseMax, c.pulseCond)
	c.eng.SetSlopeParams(c.slopeLo, c.slopeHi, c.slopeMin, c.slopeMax, c.slopeCond)
	c.eng.SetVideoParams(c.videoStd, c.videoLine, c.videoNeg)
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
			if m, ok := c.eng.ReadMatrix(); ok {
				// Fallback mode (no SIGIO) decodes knobs on the tick too —
				// the deliberate exception that accepts mid-detent reads.
				c.decode(m, !haveSIGIO)
			}
		}
	}
}

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

func (c *Controller) button(code int) {
	// While an autoset sweep runs, ignore everything except AUTO (which cancels)
	// — this both gives a clean "busy" UX and avoids racing its scale changes.
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy && code != btnAuto {
		return
	}
	switch code {
	case btnRunStop:
		// Toggle RUN/STOP and leave SINGLE mode (SyncLEDs keeps the shadow in step
		// with a single that self-stopped, so this toggles from the right state).
		c.mu.Lock()
		c.running = !c.running
		r := c.running
		c.single = false
		c.mu.Unlock()
		c.eng.SetRunning(r)
	case btnSingle:
		c.mu.Lock()
		c.norm, c.running, c.single = true, true, true
		c.mu.Unlock()
		c.eng.SetSingle() // true single-shot: capture one triggered frame, stop
	case btnAuto:
		c.autoset()
		return
	case btnCh1VdivPush:
		c.eng.SetTrigSource(0) // push CH1 V/DIV → trigger on C1
		return
	case btnCh2VdivPush:
		c.eng.SetTrigSource(1) // push CH2 V/DIV → trigger on C2
		return
	case btnTrigLvlPush:
		c.eng.SetTrigSlope(!c.eng.Snapshot().TrigRising) // push TRIG LEVEL → flip edge
		return
	case btnUtility:
		c.srToggle() // arm ⇄ cancel super-res (like SINGLE); handles its own LEDs
		return
	case btnAdjustPsh:
		c.srFocusCycle() // ADJUST/intensity push → cycle watch/gate-start/gate-end/review
		return
	default:
		// Menu / softkey / channel buttons (spec 08 §6). Anything else is
		// claimed-and-ignored so it can't cross-drive another control.
		c.menuButton(code)
		return
	}
	c.pushLEDs()
}

// resync refreshes the knob shadows from authoritative state before a step,
// so a step lands relative to whatever the web UI / SCPI last set — not a
// stale panel-local value (which would snap the setting on the first click).
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

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// knob services AT MOST ONE knob per event, walking the fixed priority order
// (the cross-coupling fix). Gate: 0x69 == 0 means a plain button interrupt.
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

func (c *Controller) dispatch(name string, dir, steps int) {
	c.mu.Lock()
	busy := c.autosetBusy
	c.mu.Unlock()
	if busy { // knobs are inert while autoset sweeps (avoids racing its writes)
		return
	}
	switch name {
	case "tdiv":
		// CW (+1) → slower timebase; ladder is ascending.
		c.tdivIdx = clampInt(c.tdivIdx+dir, 0, len(c.tdivs)-1)
		c.eng.SetTdiv(c.tdivs[c.tdivIdx])
	case "ch1vdiv", "ch2vdiv":
		ch := 0
		if name == "ch2vdiv" {
			ch = 1
		}
		if c.fe == nil {
			return // claim-and-ignore without an analog front end
		}
		c.vIdx[ch] = clampInt(c.vIdx[ch]+dir, 0, len(analog.Detents)-1)
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
		// Offset step is 20 DAC codes/accel-step; K=262 codes/V is fixed, so
		// step the input-referred volts by 20/262 and let the front end
		// re-derive the code (keeps the offset consistent across detents).
		v := c.fe.OffsetReqV(ch) + float64(dir*steps)*20.0/262.0
		c.fe.SetOffset(ch, v)
	case "triglevel":
		// Sign trap: CW RAISES the level, which LOWERS the code
		// (−938 codes/V); step 40 codes per accel step.
		nc := int(c.trigCode) - dir*40*steps
		nc = clampInt(nc, engine.TrigCodeMin, engine.TrigCodeMax)
		c.trigCode = uint16(nc)
		c.eng.SetTrigLevelCode(c.trigCode)
	case "adjust":
		// While editing a super-res gate edge, ADJUST moves that edge; otherwise it
		// drives the highlighted menu item (spec 08 §6.3; no-op if the menu is shut).
		if c.srGateAdjust(dir * steps) {
			return
		}
		c.menuAdjust(dir)
	case "horizpos":
		// Horizontal POSITION knob: when zoomed, pan the zoom window across the
		// record; otherwise pan the trigger point (was a dead knob before).
		c.mu.Lock()
		z := c.zoom
		if z > 1 {
			c.zoomOff = clampF(c.zoomOff+float64(dir*steps)*0.02, -0.5, 0.5)
			c.mu.Unlock()
		} else {
			c.mu.Unlock()
			c.eng.SetTrigPosFrac(clampF(c.trigPos()+float64(dir*steps)*0.01, 0.02, 1))
		}
	}
}
