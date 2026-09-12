package engine

import (
	"math"
	"time"

	"open-sds/app/internal/iface"
)

// The acq2 capture sequence (workplan §2, iface): RESET → RUN → DECIM →
// PRETRIG/POSTTRIG → ACQ_CTRL/TRIG_LEVEL → GO → poll STATUS_A → HALT →
// BURST_REMAIN → drain BURST (EDMA or ioctl) → GO (re-arm).

// maxRecordCols is the most a frame may drain from one finalized record. The
// fabric accepts PRETRIG <= PRETRIG_MAX and POSTTRIG <= PRETRIG_MAX - PRETRIG
// (default.v clamps both; PRETRIG_MAX = REC_DEPTH - 2 is the schema's margin),
// so a record is never longer than PRETRIG_MAX words. Programming 10240/10240
// silently became a 20478-word record and every full-depth frame a 2-word
// short drain (never coherent) — deepRecord stays the PHYSICAL depth (buffer
// sizes, FILL saturation), this bounds what is asked for and drained.
const maxRecordCols = iface.PretrigMax

// bringUp programs the record for the current band and drain depth. Run once
// at start and again on every band or trigger-mode change — never per frame.
// It writes no CS3 register (the MAX V comparator/offset DACs are inherited
// from boot or staged separately). Schema v3: the engine owns its test
// source (ADC) and drain window (the full record) explicitly, so a
// diagnostic job that died mid-way can never leave a ramp or a partial
// window under the frames.
func (e *Engine) bringUp() {
	capCols := e.capDepth()
	e.lastCapCols = capCols
	pre := uint32(capCols / 2)
	post := uint32(capCols - capCols/2)
	decim := e.band.Decim()
	e.w(iface.SelOpcode, iface.OpReset) // idle the capture FSM before reprogramming
	e.w(iface.SelRun, e.runWord())
	e.w(iface.SelDecimLo, uint16(decim))
	e.w(iface.SelDecimHi, uint16(decim>>16))
	e.w(iface.SelPretrigLo, uint16(pre))
	e.w(iface.SelPretrigHi, uint16(pre>>16))
	e.w(iface.SelPosttrigLo, uint16(post))
	e.w(iface.SelPosttrigHi, uint16(post>>16))
	e.flushTrigWords(true)
	e.w(iface.SelIlCtrl, iface.IlCtrlTsrcAdc<<iface.IlCtrlTsrcShift)
	e.w(iface.SelDrainStart, 0)
	e.w(iface.SelDrainLen, 0)
}

// capDepth is the record size programmed into the fabric: what the frame
// will drain, bounded by what the fabric can finalize (pre + post <=
// PRETRIG_MAX, see maxRecordCols) so the drain never exceeds the record.
func (e *Engine) capDepth() int {
	c := e.effDrainCols()
	if c > maxRecordCols {
		c = maxRecordCols
	}
	if c < 2 {
		c = 2
	}
	return c
}

// doReinit runs a staged FSM re-initialization on the OWNER goroutine at a loop
// boundary (no capture in flight). Level 1 re-programs (identical to a band
// change); level 2 first freezes and idles whatever is in flight.
func (e *Engine) doReinit(level int64) {
	e.logf("engine: FSM re-init level %d (degraded_run=%d)", level, e.degradedRun)
	if level >= 2 {
		e.w(iface.SelOpcode, iface.OpHalt)
		e.w(iface.SelOpcode, iface.OpReset)
		e.clk.Sleep(2 * time.Millisecond)
	}
	e.bringUp()
	e.degradedRun = 0
}

// armEngine arms (or re-arms) the capture: settle, then OPCODE = GO.
func (e *Engine) armEngine() { e.armEngineQuiet(false) }

// armEngineQuiet is armEngine with an optional already-held quiet lock. The
// settle holds the single core so no goroutine perturbs the capture-setup
// window, and the quiet gate pauses the LCD render / web serialize across
// the settle+GO (a framebuffer blit contends on the memory bus, not just the
// CPU). Kept from the factory-fabric engine until the default image's
// static-freeze byte-identity test passes on the bench.
func (e *Engine) armEngineQuiet(quietHeld bool) {
	if !quietHeld {
		e.quiet.Lock()
	}
	// The record depth follows effDrainCols (mem depth, SINGLE, stream) which
	// can change without a band transition; re-program before arming so the
	// fabric never finalizes fewer words than the frame will drain. The arm
	// point is a halted boundary, so the RESET inside bringUp kills nothing.
	if e.capDepth() != e.lastCapCols {
		e.bringUp()
	}
	settle := time.Duration(e.tuneArmSettleUs.Load()) * time.Microsecond
	if e.armBusy && e.tuneArmSpin.Load() {
		for start := time.Now(); time.Since(start) < settle; {
		}
	} else {
		e.clk.Sleep(settle)
	}
	e.w(iface.SelOpcode, iface.OpGo)
	if !quietHeld {
		e.quiet.Unlock()
	}
}

// trigPosFracVal reads the horizontal trigger-position fraction (0..1).
func (e *Engine) trigPosFracVal() float64 {
	return math.Float64frombits(e.trigPosFrac.Load())
}

// readTrigPos reads the trigger sample index (TRIGPOS_HI.IDX). Telemetry.
func (e *Engine) readTrigPos() int {
	return int(e.r(iface.SelTrigposHi) & iface.TrigposHiIdxMask)
}

// waitCapture runs the bounded wait gate: poll STATUS_A + FILL every pollEvery
// within the band budget. Returns the gate results plus whether the fill
// counter advanced at all (wedge evidence when it never does).
//
// A frame anchors on STATUS_A.DONE (the post-trigger record completed) with
// the record filled (FILL ≥ latchAt). In AUTO it ALSO completes on
// STATUS_A.VALID (the fabric's auto/free-run completion) or on a fill that
// reached the programmed record, so an untriggered AUTO display publishes a
// free-run frame instead of holding forever; NORM never free-runs.
func (e *Engine) waitCapture(norm bool) (anchored, sawTrig, filled, fillMoved bool, trigPos int) {
	start := e.clk.Now()
	deadline := start.Add(time.Duration(e.band.WaitBudgetNs()))
	nativeFast := e.band.NativeFast()
	// Decimated NORM needs a DENSE record — the buffer filled to drainCols — so
	// software centring locks a mid-record crossing instead of the sparse
	// triggered gate. Gate on TIME: the interval to clock drainCols samples.
	denseWait := norm && e.band.Kind() == KindDecimated
	denseNs := int64(float64(e.effDrainCols()) * e.band.CaptureIntervalNs())
	// Native-fast maturation: DONE can assert before the deep record is fully
	// written; hold a short floor before halting (tunable).
	nativeMature := time.Duration(e.tuneMatureUs.Load()) * time.Microsecond
	capCols := uint16(e.lastCapCols)
	fill0 := e.r(iface.SelFill) & fillMask
	var trigAt time.Time
	for {
		s := e.r(iface.SelStatusA)
		if s&statTrig != 0 && !sawTrig {
			sawTrig = true
			trigAt = e.clk.Now()
			trigPos = e.readTrigPos()
			// Once the edge has fired the record completes within the
			// post-trigger time — extend the deadline so a late edge is not
			// halted mid post-fill by the budget expiry.
			if !nativeFast {
				postNs := time.Duration(denseNs)
				if frac := e.trigPosFracVal(); frac > 0 && frac < 1 {
					postNs = time.Duration(float64(denseNs) * (1 - frac) * 1.15)
				}
				if d2 := trigAt.Add(postNs + 2*time.Millisecond); d2.After(deadline) {
					deadline = d2
				}
			}
		}
		completed := s&statDone != 0
		if !norm && s&statValid != 0 {
			completed = true // AUTO: the fabric's auto completion
		}
		if completed && !anchored {
			anchored = true
			if !sawTrig {
				trigPos = e.readTrigPos()
			}
		}
		fill := e.r(iface.SelFill) & fillMask
		if fill != fill0 {
			fillMoved = true
		}
		if fill >= latchAt {
			filled = true
		}
		if nativeFast {
			// Native-fast: the record fills in ~µs. Halt once filled AND either
			// completion evidence arrived (DONE or the trigger) or this is AUTO
			// (an untriggered AUTO frame free-runs its live view rather than
			// burning the whole budget). NORM without a trigger waits the full
			// budget, then holds.
			if filled && e.clk.Now().Sub(start) >= nativeMature &&
				(anchored || sawTrig || !norm) {
				return
			}
		} else {
			if anchored && filled {
				postOK := true
				if sawTrig {
					postNs := time.Duration(denseNs)
					if frac := e.trigPosFracVal(); frac > 0 && frac < 1 {
						postNs = time.Duration(float64(denseNs) * (1 - frac) * 1.15)
					}
					postOK = e.clk.Now().Sub(trigAt) >= postNs
				}
				if postOK && (!denseWait || e.clk.Now().Sub(start) >= time.Duration(denseNs)) {
					return // triggered, post-trigger record filled (and dense in NORM)
				}
			}
			if !norm && !sawTrig && capCols > 0 && fill >= capCols {
				return // AUTO free-run (no edge): the record is written through, drain it now
			}
		}
		if e.stopReq.Load() {
			return // abandon armed+filling: safe; boundary handles shutdown
		}
		if !e.clk.Now().Before(deadline) {
			return // budget expired: AUTO free-runs a refresh, NORM holds
		}
		e.beatN.Add(1)
		busyFill := time.Duration(e.tuneBusyFillUs.Load()) * time.Microsecond
		if nativeFast && e.armBusy && busyFill > 0 && e.clk.Now().Sub(start) < busyFill {
			continue // spin-poll: deny the core to competing bus traffic
		}
		e.clk.Sleep(e.pollEvery)
	}
}

// halt freezes the record (OPCODE = HALT) and confirms the fill froze:
// accept the first pair of equal FILL reads within a handful of polls.
func (e *Engine) halt() bool {
	e.w(iface.SelOpcode, iface.OpHalt)
	prev := e.r(iface.SelFill) & fillMask
	e.lastFillAtHalt = int(prev)
	for i := 0; i < 5; i++ {
		cur := e.r(iface.SelFill) & fillMask
		e.lastFillAtHalt = int(cur)
		if cur == prev {
			return true
		}
		prev = cur
	}
	return false
}

// haltSettle gives a confirmed native-fast capture-halt a short, quiet window
// before the first pop (tunable; 0 = off).
func (e *Engine) haltSettle(nativeFast bool) {
	if !nativeFast || !e.armBusy {
		return
	}
	d := time.Duration(e.tuneHaltSettleUs.Load()) * time.Microsecond
	for start := time.Now(); time.Since(start) < d; {
	}
}

// drain reads the frozen record into the producer slot through the BURST
// port, under the quiet gate.
func (e *Engine) drain(f *Frame, cols int) {
	e.quiet.Lock()
	e.drainQuiet(f, cols)
	e.quiet.Unlock()
}

// drainQuiet drains while the caller already owns quiet exclusively. The pop
// count is bounded by BURST_REMAIN: a record shorter than requested (the
// fabric finalized fewer words) is drained to what it holds and the rest of
// the slot is filled with the last real word so no stale samples leak; the
// shortfall is counted as telemetry. A port that reports nothing ready pops
// nothing and the frame is marked incoherent by the caller through drainN.
func (e *Engine) drainQuiet(f *Frame, cols int) {
	rem := e.r(iface.SelBurstRemain)
	n := cols
	avail := int(rem & iface.BurstRemainRemainMask)
	if rem&iface.BurstRemainReadyMask == 0 {
		n = 0
	} else if avail < n {
		n = avail
	}
	if n > 0 {
		e.b.BurstInto(f.C1[:n], f.C2[:n], n)
	}
	if n < cols {
		fillC1, fillC2 := uint8(128), uint8(128)
		if n > 0 {
			fillC1, fillC2 = f.C1[n-1], f.C2[n-1]
		}
		for i := n; i < cols; i++ {
			f.C1[i], f.C2[i] = fillC1, fillC2
		}
		e.mu.Lock()
		e.stats.ShortDrains++
		e.mu.Unlock()
	}
	e.lastDrainN = n
}

// stitchFrame runs one STREAM window: arm → PURE TIMED wait of exactly N·dt
// (no trigger/saturation poll) → halt → burst drain → publish EVERY window raw
// + contiguous with continuity metadata. The client stitches consecutive
// windows on one axis, marking the GapNs blackout between them.
func (e *Engine) stitchFrame(norm bool) {
	cols := e.effDrainCols()
	fillNs := int64(float64(cols) * e.band.CaptureIntervalNs())

	armStart := e.clk.Now()
	var gapNs int64
	if !e.lastHalt.IsZero() {
		gapNs = int64(armStart.Sub(e.lastHalt))
	}
	e.armEngine()

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

	f.Valid, f.WinCols = cols, decimWin
	f.EdgeX = -1
	f.Interp, f.IsEnv, f.EnvCols, f.RollCodes = false, false, 0, false
	f.Norm = norm
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	_, _, p := ptp(f.C1[:cols])
	f.Ptp, f.Trigd, f.Coherent, f.HaltOK = p, false, haltOK && e.lastDrainN == cols, haltOK
	f.Degraded = false
	e.streamSeq++
	f.StreamSeq, f.WindowNs, f.GapNs = e.streamSeq, fillNs, gapNs

	e.seq++
	f.Seq = e.seq
	e.arena.Publish()

	e.mu.Lock()
	e.stats.Published++
	e.stats.Seq = e.seq
	if f.Coherent {
		e.stats.Coherent++
	}
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
