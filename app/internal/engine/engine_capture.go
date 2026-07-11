package engine

import (
	"math"
	"time"
)

// The native-fast fill busy-poll window is the tunable tuneBusyFillUs (default 0
// = off; it burned CPU without lowering the bus-driven half rate). See waitCapture.

// bringUp is the engine enable+divisor sequence (spec 03 §4.1), run once at
// start and again on every band or trigger-mode change — never per frame.
// Divisor-hi is cleared FIRST (a stale hi silently mis-clocks every hi=0
// band; live precisely on slow→fast transitions where hi ≠ 0). It programs
// Prog() — the envelope formula / roll divisor on slow bands, never the
// nominal table row. It writes no CS3 registers: the boot comparator is
// inherited.
func (e *Engine) bringUp() {
	class, lo, hi := e.band.Prog()
	e.w(selResetHd, 0x0001)
	e.w(selResetHd, 0x0000)
	e.w(selRunWord, e.runWord())
	e.w(selReset2, 0x0000)
	e.w(selDivHi, 0x0000)
	e.w(selClass, class)
	e.w(selDivLo, lo)
	e.w(selDivHi, hi)
}

// doReinit runs a staged FSM re-initialization on the OWNER goroutine at a loop
// boundary (no capture in flight). Levels escalate; all writes are CS1-plane FSM
// controls, never CS3 (the boot comparator stays inherited, spec 03 §2):
//
//	1  the documented bring-up sequence (identical to a band change)
//	2  a deeper knock for the stuck half-record FSM state: clean halt, reset-head
//	   strobe, a 1→0 PULSE on the secondary reset 0x36 (bring-up only ever writes
//	   0 — but its sibling resets 0x44/0x57 are both pulsed, so the pulse shape is
//	   the natural hypothesis for the reset the vendor init performs that we
//	   lack), run word OFF, settle, then the full bring-up.
//
// Recovery target: the persistent stuck state (every native-fast capture keeps a
// dead tail; bench-proven to survive band changes, app restarts, ETS class flips,
// memdepth cycles and autoset — historically cured only by a power-cycle).
func (e *Engine) doReinit(level int64) {
	e.logf("engine: FSM re-init level %d (degraded_run=%d)", level, e.degradedRun)
	if level >= 2 {
		e.w(selArm, opHalt) // freeze whatever is in flight, cleanly
		e.w(selResetHd, 0x0001)
		e.w(selResetHd, 0x0000)
		e.w(selReset2, 0x0001) // secondary-reset PULSE — the untried lever
		e.w(selReset2, 0x0000)
		e.w(selRunWord, 0x0000) // run word OFF: stop the fill FSM outright
		e.clk.Sleep(2 * time.Millisecond)
	}
	e.bringUp()
	e.degradedRun = 0 // give the detector a fresh window to judge the result
}

// armEngine per spec 03 §5.1: reset-head ×2, write-pointer pulse, settle, go.
func (e *Engine) armEngine() {
	e.armEngineQuiet(false)
}

// armEngineQuiet is armEngine with an optional already-held quiet lock.
func (e *Engine) armEngineQuiet(quietHeld bool) {
	if e.armBusy && e.tuneFrameTail.Load() {
		e.w(selPreamble, 0x0000) // reference-device frame preamble
	}
	e.w(selArm, opResetHead)
	e.w(selArm, opResetHead)
	e.w(selWrPtr, 0x0001)
	e.w(selWrPtr, 0x0000)
	// ROOT CAUSE of the native-fast half-record: the settle must HOLD the single
	// core, not yield it. As a time.Sleep it yields — concurrent goroutines (GC,
	// net/http, the LCD render) run during this capture-setup window and freeze
	// the pre-trigger half of the record (bench: 33% half under load with Sleep,
	// 2% with a busy-wait; the 2% residual is the drain, which the gate + re-
	// capture mop up). SCHED_FIFO couldn't fix it — a sleeping FIFO thread yields
	// anyway — and the RE never saw it because it ran with no concurrent load.
	// Busy-wait on the real monotonic clock; fake-clock tests keep the Sleep so
	// their synthetic Now still advances. Also hold the quiet gate: the busy-wait
	// denies the core to CPU-bound goroutines, but the LCD render's framebuffer
	// blit contends on the memory bus (not just the CPU), so the render must be
	// PAUSED — not merely out-scheduled — across the settle+go. The gate blocks it
	// (and the web serialize) here and across the drain; both run in the dead time.
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
	e.w(selArm, opGo)
	if !quietHeld {
		e.quiet.Unlock()
	}
}

// trigPosFracVal reads the horizontal trigger-position fraction (0..1).
func (e *Engine) trigPosFracVal() float64 {
	return math.Float64frombits(e.trigPosFrac.Load())
}

// waitCapture runs the bounded wait gate (spec 03 §5.2): poll 0x39 + 0x46
// every 150 µs within the band budget. Returns the gate results plus whether
// the fill counter advanced at all (wedge evidence when it never does).
//
// A frame is complete when it anchors on the comparator DONE bit (0x39 bit2,
// a real trigger) with the post-trigger record filled (0x46 ≥ LatchAt) — the
// triggered path, both modes. In AUTO it ALSO completes when the free-running
// record has simply filled to (near) the 11-bit counter max: an untriggered
// AUTO display then publishes a free-run frame at the full FSM rate instead
// of holding on every frame that fails to trigger. NORM never takes the
// free-run path — a quiet NORM screen legitimately holds until a real trigger.
// `full` is tracked independently of `anchored` (they were coupled before,
// which starved AUTO). bit0 (VALID/auto-timeout) is honoured too as an early
// AUTO completion.
func (e *Engine) waitCapture(norm bool) (anchored, sawTrig, filled, fillMoved bool, trigPos int) {
	start := e.clk.Now()
	deadline := start.Add(time.Duration(e.band.WaitBudgetNs()))
	// Decimated NORM needs a DENSE record — the buffer filled to drainCols — so
	// software centring locks a mid-record crossing instead of the jittery LAST
	// crossing at the rail boundary of a sparse (fill≥LatchAt) triggered capture
	// (that boundary drifts non-periodically frame-to-frame → the wave jitters).
	// The fill counter saturates at 11 bits well before drainCols, so gate on
	// TIME: the interval to clock drainCols samples. This is free (well under the
	// 50 ms publish pace floor) and scoped to decimated NORM — native-fast, AUTO,
	// envelope and roll are untouched. AUTO already fills densely via its budget.
	denseWait := norm && e.band.Kind() == KindDecimated
	denseNs := int64(float64(e.effDrainCols()) * e.band.CaptureIntervalNs())
	// Native-fast FREE RUN + TRIGGER HOLD (spec 04 §11): halt once the HW comparator has fired
	// (bit1) AND the deep record has FILLED — so the frozen record is coherent to ~20480 (spec
	// 04 §4) with the edge near record/2 (cross-frame std 1–2). The comparator fires almost
	// immediately on a continuous signal, so returning on bit1 ALONE freezes a half-filled
	// buffer whose unwritten tail drains as a flat dead repeat (display then centres on the
	// live/dead boundary; super-res sees the dead tail). The fill counter saturates at 11 bits
	// well before drainCols, so gate the fill on TIME — the interval to clock drainCols samples
	// (denseNs) — exactly as decimated NORM does above. On the budget timeout (no comparator
	// edge) AUTO free-runs a live refresh; NORM holds.
	nativeFast := e.band.NativeFast()
	// Native-fast maturation: 0x39 DONE and 0x46≥LatchAt assert ~1 ms after arm
	// but the deep record may not be fully written yet; halting too early keeps
	// a dead tail. The historical 40 ms "lower bound" was measured in what is
	// now known to have been the UNTRIGGERED state (where the record parks at
	// half regardless of time) — tunable so the true triggered-state bound can
	// be measured on hardware.
	nativeMature := time.Duration(e.tuneMatureUs.Load()) * time.Microsecond
	// AUTO FORCE-TRIGGER (reference-device op): with no comparator edge — e.g.
	// the level outside the signal band — the FSM never runs the post-trigger
	// fill and every deep record keeps a ~half dead tail (the "ACQ STUCK"
	// state). The reference device pulses 0x2c (0→1) on its untriggered AUTO
	// frames, forcing the FSM to complete the record. Issue it once per wait,
	// after force_after_us without a trigger. NORM never forces — it holds.
	forced := false
	forceMode := e.tuneForceMode.Load()
	forceAfter := time.Duration(e.tuneForceAfterUs.Load()) * time.Microsecond
	fill0 := e.r(selFill) & fillMask
	var trigAt time.Time
	for {
		s := e.r(selStatus)
		if s&statTrig != 0 && !sawTrig {
			sawTrig = true
			trigAt = e.clk.Now()
			// 0x46 RESETS when the comparator fires (it counts post-trigger
			// samples). Gates latched during the pre-trigger free-run must not
			// satisfy a post-trigger record: without this reset, a late edge on
			// a decimated AUTO band returned on the stale pre-trigger `filled`
			// and halted with the post-trigger half unwritten — the fuzz-caught
			// published triggered-half (fill_at_halt ≈ 25–68, valid ≈ cols/2).
			filled = false
			trigPos = int(e.r(selTrigHi))<<8 | int(e.r(selTrigLo)&0xff)
			// The budget bounds the UNTRIGGERED wait. Once the edge HAS fired
			// the record completes within the post-trigger time — extend the
			// deadline so an edge landing late in the budget is not halted
			// mid post-fill by the unconditional expiry return (fuzz-caught
			// residual: NORM 2 ms/div, arm_to_latch ≈ 42 ms, valid ≈ cols/2).
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
			completed = true // AUTO free-run timeout
		}
		if completed && !anchored {
			anchored = true
			if !sawTrig {
				trigPos = int(e.r(selTrigHi))<<8 | int(e.r(selTrigLo)&0xff)
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
			// Native-fast halts on CONTENT, not the status bits: bit2(done) is
			// bimodal on hardware (asserts in ~1 ms or not at all within the
			// budget) while the drained record is full either way once the
			// maturation floor has passed. So once mature+filled, return when
			// EITHER completion evidence arrived (done or the comparator edge)
			// OR this is AUTO (an untriggered AUTO frame stays parked at the
			// pre-trigger half no matter how long we wait — return and publish
			// the honest free-run view instead of burning the 40 ms budget).
			// NORM without a trigger still waits the full budget, then holds.
			if filled && e.clk.Now().Sub(start) >= nativeMature &&
				(anchored || sawTrig || !norm) {
				return
			}
		} else {
			if anchored && filled {
				// Decimated NORM also waits for a dense buffer (see above); other
				// bands return as soon as the post-trigger record is filled. A
				// TRIGGERED capture additionally waits out the post-trigger
				// record time FROM THE EDGE — the fill counter alone (≥ LatchAt
				// = 512) is far short of the post-trigger half, and both status
				// gates can pre-date the edge.
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
			if !norm && !sawTrig && fill >= fillFull {
				return // AUTO free-run (no edge): the record saturated, drain it now
			}
		}
		if nativeFast && !norm && e.armBusy && !sawTrig && forceMode > 0 &&
			e.clk.Now().Sub(start) >= forceAfter {
			if !forced {
				forced = true
				if forceMode&1 != 0 {
					e.w(selForce, 0x0000)
					e.w(selForce, 0x0001)
				}
				if forceMode&2 != 0 {
					e.w(selRetrig, 0x0001)
				}
			}
			// bit3: the reference device's untriggered-wait spin — while no
			// trigger, continuously rewrite the acq-control pair and pulse the
			// 0x16 re-arm strobe (~800 Hz on the reference; here every poll
			// pass the deadline check reaches, ~150 µs–1 ms).
			if forceMode&8 != 0 {
				e.w(selTailC, 0x0000)
				e.w(selTailD, 0x0000)
				e.w(selTailA, uint16(e.tuneTail3c.Load()))
				e.w(selTailB, uint16(e.tuneTail3d.Load()))
				e.w(selRetrig, 0x0001)
			}
		}
		if e.stopReq.Load() {
			return // abandon armed+filling: safe; boundary handles shutdown
		}
		if !e.clk.Now().Before(deadline) {
			return // budget expired: AUTO free-runs a refresh, NORM holds
		}
		e.beatN.Add(1)
		// Native-fast fill is load-sensitive like the settle: a concurrent bus burst
		// (LCD blit, GC, socket I/O) while the deep record is freezing corrupts it —
		// and the fill's poll-sleep YIELDS the core, letting exactly that run. On a
		// real signal the record fills in ~µs, so busy-poll (hold the core, no yield)
		// for a short bounded window that covers the fast fill; past it (a quiet
		// screen with no edge) fall back to sleeping — there is no coherent record to
		// protect, and holding the core for the whole budget would starve the UI.
		// Gated on armBusy so fake-clock tests keep sleeping (their Now only advances
		// on Sleep).
		busyFill := time.Duration(e.tuneBusyFillUs.Load()) * time.Microsecond
		if nativeFast && e.armBusy && busyFill > 0 && e.clk.Now().Sub(start) < busyFill {
			continue // spin-poll: deny the core to competing bus traffic
		}
		e.clk.Sleep(e.pollEvery)
	}
}

// halt latches the frozen record (0xC8) and confirms the fill froze. Freezing
// a free-running (untriggered AUTO) buffer can take a few bus cycles to
// settle, so poll a handful of times and accept the first pair of equal reads
// rather than demand the very first back-to-back pair match — a strict
// double-read spuriously fails on ~1/5 of AUTO frames and holds them.
func (e *Engine) halt() bool {
	e.w(selArm, opHalt)
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

// haltSettle gives a confirmed native-fast capture-halt a short, quiet window
// before the first mmap sample-port read.  It tests whether the FPGA's two
// deep-memory banks complete their read-side latch after the fill counter has
// already frozen.
func (e *Engine) haltSettle(nativeFast bool) {
	if !nativeFast || !e.armBusy {
		return
	}
	d := time.Duration(e.tuneHaltSettleUs.Load()) * time.Microsecond
	for start := time.Now(); time.Since(start) < d; {
	}
}

// drain reads the frozen deep record into the producer slot: sample i comes
// from port 0x30+(i mod 5); each word packs C1 in the high byte, C2 low.
func (e *Engine) drain(f *Frame, cols int) {
	// ONE tight bulk read — no per-sample interface dispatch, no modulo. The
	// busy-wait settle (armEngine) is the root-cause fix; the drain is the
	// second-order window (the residual ~2%): a concurrent CPU burst here can
	// corrupt the frozen record. Hold the quiet gate so the render/web/panel
	// pause for the ~17ms drain — shrinking the residual so re-capture rarely
	// fires. They run freely during the loop's ~90ms of dead time.
	e.quiet.Lock()
	e.drainQuiet(f, cols)
	e.quiet.Unlock()
}

// drainQuiet drains while the caller already owns quiet exclusively.  The
// native-fast path holds that lock across arm -> fill -> halt -> drain, because
// releasing it after opGo lets a waiting web serializer run during the
// load-sensitive fill window.  Other paths use drain(), which brackets only
// the drain as before.
func (e *Engine) drainQuiet(f *Frame, cols int) {
	e.b.DrainInto(f.C1[:cols], f.C2[:cols], cols)
}

// frameTail issues the reference device's per-frame completion op after the
// drain and before the re-arm: clear 0x3e/0x58, reload 0x3c/0x3d, then strobe
// 0x16=1 (re-trigger). This is the op whose absence starves the trigger engine
// into the persistent half-record state (every capture keeps a ~cols/2 dead
// tail, saw_trig never asserts, survives restarts/band changes/power-cycle).
func (e *Engine) frameTail() {
	if !e.armBusy || !e.tuneFrameTail.Load() {
		return
	}
	e.w(selTailC, 0x0000)
	e.w(selTailD, 0x0000)
	e.w(selTailA, uint16(e.tuneTail3c.Load()))
	e.w(selTailB, uint16(e.tuneTail3d.Load()))
	e.w(selRetrig, 0x0001)
	// force_mode bit2: issue the 0x2c force pulse HERE (post-drain, pre-arm —
	// the reference device's actual placement) instead of mid-wait.
	if e.tuneForceMode.Load()&4 != 0 {
		e.w(selForce, 0x0000)
		e.w(selForce, 0x0001)
	}
}

// stitchFrame runs one STREAM window: arm → PURE TIMED wait of exactly N·dt
// (no trigger/saturation poll — that wait is the recoverable overhead the
// free-run/hold technique eliminates) → halt → mmap drain → re-arm → publish
// EVERY window raw + contiguous with continuity metadata. The client stitches
// consecutive windows on one axis, marking the GapNs blackout (the unavoidable
// drain+re-arm time) between them, and decodes per window.
func (e *Engine) stitchFrame(norm bool) {
	cols := e.effDrainCols()
	fillNs := int64(float64(cols) * e.band.CaptureIntervalNs())

	armStart := e.clk.Now()
	var gapNs int64
	if !e.lastHalt.IsZero() {
		gapNs = int64(armStart.Sub(e.lastHalt))
	}
	e.armEngine() // opGo: begin the free-run fill

	// Pure timed wait: the deep record is full after N·dt. No status/trigger
	// poll — a triggered wait was the ~11 ms/frame overhead we're removing.
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
	// No re-arm here — the next stitchFrame arms once. (A re-arm here would be
	// discarded by that arm's reset-head and just double the arm overhead/gap.)

	// Raw, contiguous, edge-agnostic — the stream is not trigger-centred. WinCols
	// stays the screen window so the web deep-serve path (Valid > WinCols) ships
	// the FULL raw record and the client navigator spans the whole window.
	f.Valid, f.WinCols = cols, decimWin
	f.EdgeX = -1
	f.Interp, f.IsEnv, f.EnvCols, f.RollCodes = false, false, 0, false
	f.Norm = norm
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	_, _, p := ptp(f.C1[:cols])
	f.Ptp, f.Trigd, f.Coherent, f.HaltOK = p, false, true, haltOK
	f.Degraded = false // arena slots are reused; only the native-fast path sets this
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
	e.stats.ValidDepth = validDepthP(f.C1[:cols], p) // reuse the ptp scan from above
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
