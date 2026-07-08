package engine

import "time"

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

// armEngine per spec 03 §5.1: reset-head ×2, write-pointer pulse, settle, go.
func (e *Engine) armEngine() {
	e.w(selArm, opResetHead)
	e.w(selArm, opResetHead)
	e.w(selWrPtr, 0x0001)
	e.w(selWrPtr, 0x0000)
	e.clk.Sleep(e.armSettle)
	e.w(selArm, opGo)
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
	fill0 := e.r(selFill) & fillMask
	for {
		s := e.r(selStatus)
		if s&statTrig != 0 && !sawTrig {
			sawTrig = true
			trigPos = int(e.r(selTrigHi))<<8 | int(e.r(selTrigLo)&0xff)
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
			// Spec 04 §8.1/§8.2/§8.3: native-fast halts when the free-run fill
			// COMPLETES — bit2(done) AND fill ≥ LatchAt, both of which assert on the
			// untriggered free-run — NOT on bit1(trig), which "can lag or never
			// assert" (§8.3). Gating on bit1 (sawTrig) waits for a trigger that never
			// comes → the budget times out on a half-filled buffer whose unwritten
			// tail drains as a flat dead repeat. Halt is unconditional; content
			// discrimination (§8.2) then decides publish vs hold.
			if anchored && filled {
				return
			}
		} else {
			if anchored && filled {
				// Decimated NORM also waits for a dense buffer (see above); other
				// bands return as soon as the post-trigger record is filled.
				if !denseWait || e.clk.Now().Sub(start) >= time.Duration(denseNs) {
					return // triggered, post-trigger record filled (and dense in NORM)
				}
			}
			if !norm && fill >= fillFull {
				return // AUTO free-run: the record saturated, drain it now
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

// halt latches the frozen record (0xC8) and confirms the fill froze. Freezing
// a free-running (untriggered AUTO) buffer can take a few bus cycles to
// settle, so poll a handful of times and accept the first pair of equal reads
// rather than demand the very first back-to-back pair match — a strict
// double-read spuriously fails on ~1/5 of AUTO frames and holds them.
func (e *Engine) halt() bool {
	e.w(selArm, opHalt)
	prev := e.r(selFill) & fillMask
	for i := 0; i < 5; i++ {
		cur := e.r(selFill) & fillMask
		if cur == prev {
			return true
		}
		prev = cur
	}
	return false
}

// drain reads the frozen deep record into the producer slot: sample i comes
// from port 0x30+(i mod 5); each word packs C1 in the high byte, C2 low.
func (e *Engine) drain(f *Frame, cols int) {
	for i := 0; i < cols; i++ {
		w := e.b.DrainRead(uint16(drainBase + i%5))
		f.C1[i] = uint8(w >> 8)
		f.C2[i] = uint8(w)
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
	e.stats.ValidDepth = validDepth(f.C1[:cols])
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
