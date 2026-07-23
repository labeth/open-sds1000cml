package engine

import (
	"math"
	"time"

	"open-sds/app/internal/iface"
)

// The owned-FPGA acquisition FSM (spec 03 §5): a small, single-owner cycle —
//
//	program(band): RUN{mode}, DECIM_LO/HI, PRETRIG_LO/HI, POSTTRIG_LO/HI  (bringUp)
//	arm:           OPCODE = OP_GO
//	wait:          poll STATUS_A until DONE (NORM: real TRIG; AUTO: DONE or budget)
//	halt:          OPCODE = OP_HALT; read FILL twice (froze) — coherence telemetry
//	drain:         BURST auto-inc port (raw) / ENV_DATA channel (envelope)
//	timestamp:     TRIGPOS{idx,frac} — the HW edge position
//	re-arm:        OPCODE = OP_GO
//	publish:       arena.Publish()
//
// The vendor bimodal-done / native-fast maturation / half-record re-capture /
// force-trigger / per-frame re-trigger machinery is DELETED, not ported: the
// owned fabric provides a trustworthy HW trigger + interpolating timestamp +
// static-freeze record (fpga doc §4/§7), so none of it is needed.

// bringUp programs the capture block for the current band (spec 03 §5.1): idle
// the FSM, set RUN (mode + run), then the decimation and pre/post-trigger
// depths. Envelope/roll bands additionally set ENV_COLS and clear the envelope
// FIFO. Run once at start and on every band or trigger-mode change — never per
// frame. It writes no CS3 registers: the analog front end is driven separately.
func (e *Engine) bringUp() {
	decim := e.band.Decim()
	pre := e.band.PreTrig()
	post := e.band.PostTrig()
	e.w(selOpcode, opReset) // idle the capture FSM before reprogramming
	e.w(selRun, e.runWord())
	e.w(selDecimLo, uint16(decim))
	e.w(selDecimHi, uint16(decim>>16))
	e.w(selPreLo, uint16(pre))
	e.w(selPreHi, uint16(pre>>16))
	e.w(selPostLo, uint16(post))
	e.w(selPostHi, uint16(post>>16))
	if k := e.band.Kind(); k == KindEnvelope || k == KindRoll {
		e.w(selEnvCols, uint16(envFabricCols)) // fold into this many columns (fits the fabric FIFO; app stretches to the display)
		e.w(selEnvReset, 0x0001)                // clear the envelope FIFO on (re)program
	}
}

// doReinit runs a staged FSM re-initialization on the OWNER goroutine at a loop
// boundary (no capture in flight). Level 1 re-programs (identical to a band
// change); level 2 additionally issues a clean halt+reset before re-programming.
// With the owned fabric there are no "untried lever" pulses to try — a mispaired
// build is refused at bring-up, and a healthy fabric always re-programs cleanly.
func (e *Engine) doReinit(level int64) {
	e.logf("engine: FSM re-init level %d", level)
	if level >= 2 {
		e.w(selOpcode, opHalt)  // freeze whatever is in flight, cleanly
		e.w(selOpcode, opReset) // idle
		e.clk.Sleep(2 * time.Millisecond)
	}
	e.bringUp()
}

// armEngine arms (or re-arms) the capture: OPCODE = OP_GO (spec 03 §5.1).
func (e *Engine) armEngine() { e.armEngineQuiet(false) }

// armEngineQuiet is armEngine with an optional already-held quiet lock. The
// arm-settle holds the single core so no goroutine perturbs the capture-setup
// window, and the quiet gate PAUSES the LCD render / web serialize across the
// settle+GO (a framebuffer blit contends on the memory bus, not just the CPU).
// NOTE (spec 03 §3, Phase E): the quiet lock is retired once the fabric's
// static-freeze byte-identity test passes on the bench — it is kept here until
// then, guarded by that property.
func (e *Engine) armEngineQuiet(quietHeld bool) {
	if !quietHeld {
		e.quiet.Lock()
	}
	settle := time.Duration(e.tuneArmSettleUs.Load()) * time.Microsecond
	if e.armBusy && e.tuneArmSpin.Load() {
		for start := time.Now(); time.Since(start) < settle; {
			// spin: deny the core to competing goroutines for these ~2ms
		}
	} else {
		e.clk.Sleep(settle)
	}
	e.w(selOpcode, opGo)
	if !quietHeld {
		e.quiet.Unlock()
	}
}

// trigPosFracVal reads the horizontal trigger-position fraction (0..1).
func (e *Engine) trigPosFracVal() float64 {
	return math.Float64frombits(e.trigPosFrac.Load())
}

// readTrigPos reads the interpolating HW trigger position (spec 03 §4.3):
// TRIGPOS_HI carries the physical sample index, TRIGPOS_LO the fractional word.
// Read-after-halt only. Returned as telemetry (Frame.TrigPos).
func (e *Engine) readTrigPos() int {
	hi := e.r(selTrigPosHi)
	return int(iface.TrigposHiIdx(hi))
}

// waitCapture runs the bounded wait gate (spec 03 §5.2): poll STATUS_A + FILL
// every pollEvery within the band budget. Returns the gate results plus whether
// the fill counter advanced at all (wedge evidence when it never does).
//
// A frame anchors on STATUS_A.DONE (a real trigger completed the record) with
// the post-trigger record filled. In AUTO it ALSO completes on STATUS_A.VALID
// (the free-run timeout) or a saturated fill, so an untriggered AUTO display
// publishes a free-run frame instead of holding forever; NORM never free-runs.
func (e *Engine) waitCapture(norm bool) (anchored, sawTrig, filled, fillMoved bool, trigPos int) {
	start := e.clk.Now()
	deadline := start.Add(time.Duration(e.band.WaitBudgetNs()))
	nativeFast := e.band.NativeFast()
	// Decimated NORM needs a DENSE record (buffer filled to drainCols) so software
	// centring locks a mid-record crossing instead of the sparse triggered gate.
	// The fill counter saturates well before drainCols, so gate on TIME — the
	// interval to clock drainCols samples. Free (well under the publish floor).
	denseWait := norm && e.band.Kind() == KindDecimated
	denseNs := int64(float64(e.effDrainCols()) * e.band.CaptureIntervalNs())
	fill0 := e.r(selFill) & fillMask
	var trigAt time.Time
	for {
		s := e.r(selStatus)
		if s&statTrig != 0 && !sawTrig {
			sawTrig = true
			trigAt = e.clk.Now()
			trigPos = e.readTrigPos()
		}
		completed := s&statDone != 0
		if !norm && s&statValid != 0 {
			completed = true // AUTO free-run timeout
		}
		if completed && !anchored {
			anchored = true
			if !sawTrig {
				trigPos = e.readTrigPos()
			}
		}
		fill := e.r(selFill) & fillMask
		if fill != fill0 {
			fillMoved = true
		}
		if fill >= latchAt {
			filled = true
		}
		if nativeFast {
			// Native-fast: the deep record fills in ~µs. Halt once filled AND
			// either completion evidence arrived (DONE or the comparator edge) or
			// this is AUTO (an untriggered AUTO frame free-runs its live view
			// rather than burning the whole budget). NORM without a trigger waits
			// the full budget, then holds.
			if filled && (anchored || sawTrig || !norm) {
				return
			}
		} else {
			if anchored && filled {
				// A triggered capture additionally waits out the post-trigger
				// record time from the edge; decimated NORM also waits dense.
				postOK := true
				if sawTrig {
					postNs := time.Duration(denseNs)
					if frac := e.trigPosFracVal(); frac > 0 && frac < 1 {
						postNs = time.Duration(float64(denseNs) * (1 - frac) * 1.15)
					}
					postOK = e.clk.Now().Sub(trigAt) >= postNs
				}
				if postOK && (!denseWait || e.clk.Now().Sub(start) >= time.Duration(denseNs)) {
					return
				}
			}
			if !norm && !sawTrig && fill >= fillFull {
				return // AUTO free-run (no edge): the record saturated, drain it now
			}
		}
		if e.stopReq.Load() {
			return // abandon armed+filling: safe; boundary handles shutdown
		}
		if !e.clk.Now().Before(deadline) {
			return // budget expired: AUTO free-runs a refresh, NORM holds
		}
		e.beatN.Add(1)
		e.clk.Sleep(e.pollEvery)
	}
}

// halt freezes the record (OP_HALT) and confirms the fill froze. Freezing a
// free-running (untriggered AUTO) buffer can take a few bus cycles to settle, so
// poll a handful of times and accept the first pair of equal reads rather than
// demand the very first back-to-back pair match.
func (e *Engine) halt() bool {
	e.w(selOpcode, opHalt)
	prev := e.r(selFill) & fillMask
	e.lastFillAtHalt = int(prev) // instrumentation: final fill read (updated below)
	for i := 0; i < 5; i++ {
		cur := e.r(selFill) & fillMask
		e.lastFillAtHalt = int(cur)
		if cur == prev {
			return true
		}
		prev = cur
	}
	return false
}

// drain reads the frozen record into the producer slot through the single
// auto-inc BURST port. The quiet gate is held across the drain (Phase-E-retired,
// see armEngineQuiet) so the render/web/panel pause for the drain.
func (e *Engine) drain(f *Frame, cols int) {
	e.quiet.Lock()
	e.drainQuiet(f, cols)
	e.quiet.Unlock()
}

// drainQuiet drains while the caller already owns quiet exclusively. The
// native-fast path holds that lock across arm → fill → halt → drain.
func (e *Engine) drainQuiet(f *Frame, cols int) {
	e.b.BurstInto(f.C1[:cols], f.C2[:cols], cols)
}

// stitchFrame runs one STREAM window: arm → PURE TIMED wait of exactly N·dt (no
// trigger/saturation poll) → halt → burst drain → publish EVERY window raw +
// contiguous with continuity metadata. The client stitches consecutive windows
// on one axis, marking the GapNs blackout between them, and decodes per window.
func (e *Engine) stitchFrame(norm bool) {
	cols := e.effDrainCols()
	fillNs := int64(float64(cols) * e.band.CaptureIntervalNs())

	armStart := e.clk.Now()
	var gapNs int64
	if !e.lastHalt.IsZero() {
		gapNs = int64(armStart.Sub(e.lastHalt))
	}
	e.armEngine() // OP_GO: begin the free-run fill

	// Pure timed wait: the record is full after N·dt. No status/trigger poll.
	target := armStart.Add(time.Duration(fillNs))
	for {
		if e.interrupted() {
			return // armed+filling is a safe park
		}
		rem := target.Sub(e.clk.Now())
		if rem <= 0 {
			break
		}
		if rem > e.pollEvery {
			rem = e.pollEvery
		}
		e.beatN.Add(1)
		e.clk.Sleep(rem)
	}
	if e.stopReq.Load() {
		return
	}

	haltOK := e.halt()
	e.lastHalt = e.clk.Now()
	f := e.arena.Write()
	drainStart := e.clk.Now()
	e.drain(f, cols)
	drainMs := e.clk.Now().Sub(drainStart)
	// No re-arm here — the next stitchFrame arms once.

	// Raw, contiguous, edge-agnostic — the stream is not trigger-centred.
	f.Valid, f.WinCols = cols, decimWin
	f.EdgeX = -1
	f.Interp, f.IsEnv, f.EnvCols, f.RollCodes = false, false, 0, false
	f.Norm = norm
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	_, _, p := ptp(f.C1[:cols])
	f.Ptp, f.Trigd, f.Coherent, f.HaltOK = p, false, true, haltOK
	f.Degraded = false
	e.streamSeq++
	f.StreamSeq, f.WindowNs, f.GapNs = e.streamSeq, fillNs, gapNs

	e.seq++
	f.Seq = e.seq
	e.arena.Publish()

	e.mu.Lock()
	e.stats.Published++
	e.stats.Seq = e.seq
	e.stats.Coherent++
	e.stats.LastPtp = p
	e.stats.ValidDepth = validDepthP(f.C1[:cols], p)
	e.stats.MemDepth = int(e.memDepth.Load())
	e.stats.DrainMs = float64(drainMs) / float64(time.Millisecond)
	e.stats.GapMs = float64(gapNs) / float64(time.Millisecond)
	e.stats.Stream = true
	e.pubTimes = append(e.pubTimes, e.clk.Now())
	if len(e.pubTimes) > 64 {
		e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
	}
	e.mu.Unlock()
}
