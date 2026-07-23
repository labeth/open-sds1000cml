package engine

import (
	"math"
	"runtime"
	"runtime/debug"
	"time"

	"open-sds/app/internal/iface"
)

// Run is the engine owner loop. It must be the only goroutine that ever
// touches the Bus. A panic is contained: logged, marked wedged, owner parks
// (no process exit — a fast crash-loop would trigger slot rollback, and the
// inherited fd must survive).
func (e *Engine) Run() {
	defer close(e.done)
	defer func() {
		if r := recover(); r != nil {
			e.logf("engine: PANIC (wedged, parked): %v", r)
			e.mu.Lock()
			e.stats.Wedged = true
			e.mu.Unlock()
			// Park servicing nothing: health stops advancing, the agent
			// relaunches us on the still-live fd.
			for !e.stopReq.Load() {
				e.clk.Sleep(100 * time.Millisecond)
			}
		}
	}()

	// Build-ID handshake (spec 03 §6): refuse to drive a fabric whose build-ID
	// or VERSION magic differs from the compiled iface — a mispaired build, not
	// a negotiation. Boot already verified/reconfigured the fabric (fpgaload.
	// Bringup — bus.New itself no longer verifies); the engine re-checks here so a
	// wedged/re-flashed fabric is caught before the first arm.
	if err := iface.Verify(e.b.Read); err != nil {
		e.logf("engine: fabric identity gate failed (%v) — refusing to drive", err)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		for !e.stopReq.Load() {
			e.clk.Sleep(100 * time.Millisecond)
		}
		return
	}

	e.bringUp()
	e.lastNorm = e.normNow()

	// Realtime GC policy (device only). The arm-settle busy-wait (armEngine) holds
	// the single core for ~2ms so no goroutine corrupts the capture setup — but an
	// automatic GC can async-preempt that tight loop to reach its STW safepoint,
	// breaking the hold. So disable proportional auto-GC and run it ourselves at
	// the top of the loop — a safe window with no settle or drain in flight. The
	// memory limit is the backstop: on a leak, GC resumes rather than OOM the unit.
	// Gated on armBusy so fake-clock tests keep the stock collector.
	const gcEvery = 8
	gcTick := 0
	lastGcCtl := false
	if e.armBusy {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		debug.SetMemoryLimit(int64(ms.Sys) + 32<<20)
	}

	for !e.stopReq.Load() {
		e.serviceCommands()
		e.bumpFrames() // heartbeat advances every iteration, stopped or not
		if e.armBusy {
			if gcCtl := e.tuneGcCtl.Load(); gcCtl != lastGcCtl {
				lastGcCtl = gcCtl
				if gcCtl {
					debug.SetGCPercent(-1)
				} else {
					debug.SetGCPercent(100)
				}
			}
			if lastGcCtl {
				if gcTick++; gcTick >= gcEvery {
					gcTick = 0
					runtime.GC()
				}
			}
		}

		if lvl := e.reinitReq.Swap(0); lvl > 0 {
			e.doReinit(lvl) // staged debug/recovery re-init; loop boundary = no capture in flight
		}

		if !e.running.Load() {
			// STOP keeps the FSM alive and servicing; it never parks the
			// engine in a halted state (spec 03 §8).
			e.clk.Sleep(50 * time.Millisecond)
			continue
		}

		// Apply staged band/mode/ETS changes at the boundary.
		e.mu.Lock()
		bandChange := e.pendSet
		if e.pendSet {
			e.band = e.pendBand
			e.pendSet = false
			e.syncBandStatsLocked()
		}
		norm := e.norm
		etsWant := e.etsWant
		e.mu.Unlock()
		if bandChange || norm != e.lastNorm || etsWant != e.etsOn {
			e.transition(norm, etsWant)
		} else if e.effDrainCols() != e.lastCapCols {
			// memDepth / stream / SINGLE changed the effective drain without a band
			// change: re-program the fabric record so the capture depth still matches
			// the drain (otherwise the drain over-reads a dead tail). Safe here — a
			// loop boundary with no capture in flight.
			e.bringUp()
		}

		switch {
		case e.streamMode.Load() && e.band.Kind() == KindDecimated:
			e.stitchFrame(norm)
		case e.band.Kind() == KindRoll:
			e.rollUpdate(norm)
		case e.band.Kind() == KindEnvelope:
			e.envFrame(norm)
		case e.etsOn && e.band.ETSEligible():
			e.etsFrame(norm)
		default:
			e.oneFrame(norm)
		}
	}
}

func (e *Engine) normNow() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.norm
}

func (e *Engine) bumpFrames() {
	e.beatN.Add(1)
	e.mu.Lock()
	e.stats.Frames++
	e.mu.Unlock()
}

// oneFrame runs one owned-FSM iteration: arm → wait-on-real-DONE → halt →
// burst-drain → re-arm → discriminate → publish (spec 03 §5). The vendor
// half-record re-capture / maturation / force / frame-tail machinery is gone —
// the owned fabric's trustworthy trigger + static-freeze record make a single
// clean drain authoritative.
func (e *Engine) oneFrame(norm bool) {
	start := e.clk.Now()
	if e.hintReset.Swap(false) {
		e.lastEdgeX = -1 // config changed → drop the stale phase hint
	}
	nativeFast := e.band.NativeFast()
	if nativeFast {
		// Keep every competing CPU/memory burst out of the complete fast-band
		// acquisition, including the fill window between OP_GO and OP_HALT
		// (Phase-E-retired; see armEngineQuiet).
		e.quiet.Lock()
	}

	e.armEngineQuiet(nativeFast)
	anchored, sawTrig, filled, fillMoved, trigPos := e.waitCapture(norm)
	armToLatch := e.clk.Now().Sub(start)
	if e.stopReq.Load() {
		if nativeFast {
			e.quiet.Unlock()
		}
		return
	}

	// Decimated readiness: NORM requires a real trigger with the post-trigger
	// record filled. AUTO always drains — either the fast triggered path, or,
	// after the budget, the frozen free-running buffer. This is what makes an
	// untriggered AUTO display update at the full rate instead of holding.
	ready := (anchored && filled) || !norm
	if !nativeFast && !ready {
		e.holdFrame(fillMoved, norm)
		e.pace(start)
		return
	}

	haltOK := e.halt()
	f := e.arena.Write()
	cols := e.effDrainCols()
	drainStart := e.clk.Now()
	if nativeFast {
		e.drainQuiet(f, cols)
	} else {
		e.drain(f, cols)
	}
	drainMs := e.clk.Now().Sub(drainStart)
	e.armEngineQuiet(nativeFast) // re-arm immediately (spec 03 §5.1 RE-ARM)
	if nativeFast {
		e.quiet.Unlock()
	}

	// ERES boxcar runs on the whole record BEFORE any discrimination, so the
	// anchor and the display see the same enhanced samples (spec 03 §7.4).
	mode := int(e.acqMode.Load())
	if mode == AcqEres {
		if l := clampEresLen(int(e.eresLen.Load())); l > 1 {
			eresBoxcar(f.C1[:cols], l, e.eresScratch)
			eresBoxcar(f.C2[:cols], l, e.eresScratch)
		}
	}

	// Set or clear ALL metadata — the arena frames are reused in place. A
	// decimated frame is coherent when triggered (anchored+filled) OR an
	// AUTO free-run full record; that is the `ready` gate that let us drain.
	coherent := haltOK && (nativeFast || ready)
	disc := f.C1[:cols]
	discIsC2 := int(e.trigSrc.Load()) == 1
	if discIsC2 {
		disc = f.C2[:cols]
	}
	lo, hi, p := ptp(disc)
	rising := e.trigRising.Load()

	// "Signal present" evidence. A large signal (ptp ≥ nativeEdgeMinPtp) always
	// qualifies; on DECIMATED bands a SMALL signal also qualifies when its ptp
	// clears k·noiseFloor, distinguishing a real sub-division signal from a noisy
	// flat rail. Native-fast keeps raw ptp only (record spans < 1 period).
	sigPresent := p >= nativeEdgeMinPtp
	if !nativeFast && !sigPresent {
		sigPresent = signalPresent(disc, float64(e.tuneSigK.Load()))
	}

	// Qualifier dispatch (spec 05): PULSE/SLOPE/VIDEO REPLACE the EDGE
	// pipeline; their own polarity/monotonicity logic is the validation.
	e.mu.Lock()
	tp := e.tp
	e.mu.Unlock()
	interval := e.band.CaptureIntervalNs()
	edgeX := -1.0
	lvlOffSig := false // EDGE: trigger level is set but sits off the signal band
	switch tp.typ {
	case TrigPulse:
		edgeX = qualifyPulse(disc, interval, tp, rising)
	case TrigSlope:
		edgeX = qualifySlope(disc, interval, tp, rising)
	case TrigVideo:
		edgeX = qualifyVideo(disc, tp)
	default:
		// EDGE: anchor on the user's HW trigger level (WYSIWYG). A lock requires a
		// right-slope crossing AT that level. If the level is SET but off the
		// signal band, NO trigger is possible — do NOT fall back to a mid-level
		// crossing and fabricate a lock (spec 05 §5.1). The mid-level fallback
		// survives only for an UNSET (boot) level, to keep the first frames stable.
		if td := e.trigDispLevel(int(e.trigSrc.Load())); td >= 0 {
			edgeX = centerCross(disc, td, rising)
			if sigPresent { // a real signal the level can sit outside of
				margin := (hi - lo) / 16
				lvlOffSig = td < lo-margin || td > hi+margin
			}
		} else {
			edgeX = centerCross(disc, (lo+hi)/2, rising) // == midLevel(disc); reuse the scan
		}
	}

	f.Valid = cols
	f.WinCols = e.band.WinCols()
	f.Interp = nativeFast
	f.IsEnv, f.EnvCols = false, 0
	f.Ptp = p
	f.Trigd = sawTrig
	f.TrigPos = trigPos
	f.Coherent = coherent
	f.Degraded = false // owned fabric drains a clean full record — never a half-capture
	f.HaltOK = haltOK
	f.RollCodes = false
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm

	// Publish policy — ONLY DISPLAY FRAMES THAT HAVE A LOCK (spec 03 §7.4, spec 05 §4,
	// spec 04 §8.2). A "lock" is a validated triggered event on the captured CONTENT:
	// a right-slope crossing (edgeX ≥ 0) on a REAL signal (ptp ≥ nativeEdgeMinPtp
	// rejects a flat rail / noise), plus a COHERENT capture on decimated bands. No
	// lock → HOLD the last locked frame; a genuinely flat / no-signal screen keeps
	// live with one honest flat capture (EdgeX = -1) every nativeFlatFallbck holds.
	qualifier := tp.typ != TrigEdge

	lock := edgeX >= 0 && (qualifier || sigPresent)
	if !nativeFast {
		lock = lock && coherent
	}

	publish := false
	switch {
	case lock:
		// A triggered / qualified edge is present — publish it centred.
		publish = true
		e.flatHeld = 0
	case !norm && !qualifier && lvlOffSig && coherent:
		// AUTO, EDGE: the trigger level is off the signal entirely — a lock is
		// impossible, so never claim a trig and never freeze. FREE-RUN an unlocked
		// live capture at the record centre. NORM instead HOLDs via the default.
		publish = true
		edgeX = -1
		f.Trigd = false
		e.flatHeld = 0
	case nativeFast && !norm && !qualifier && !sawTrig:
		// AUTO native-fast, comparator did NOT fire within the budget (untriggered):
		// FREE RUN a live refresh at the record centre (spec 04 §11) instead of
		// holding. Uncentred (EdgeX = -1): no software anchor on noise.
		publish = true
		edgeX = -1
		e.flatHeld = 0
	case (nativeFast || !norm) && !qualifier && !sigPresent:
		// NORM native-fast flat (trigger-hold with an honest refresh), or AUTO
		// decimated flat: publish one honest flat capture every nativeFlatFallbck holds.
		e.flatHeld++
		if e.flatHeld >= nativeFlatFallbck {
			edgeX = -1 // one honest flat capture; never fabricate an edge
			publish = true
			e.flatHeld = 0
		}
	default:
		// NORM decimated quiet screen, an un-fired qualifier, or AUTO decimated
		// signal-present-but-not-locked this frame → HOLD the last locked frame.
		// AUTO LIVENESS: "not locked THIS frame" can be PERSISTENT (a fast signal
		// aliased by a slow band can have no crossing of the requested slope) —
		// give the default arm an honest unlocked refresh every nativeFlatFallbck
		// holds (or after autoLivenessMaxWait), so AUTO can never freeze while the
		// signal is alive. NORM keeps holding strictly.
		publish = false
		if !norm {
			e.flatHeld++
			stale := !e.lastPubAt.IsZero() && e.clk.Now().Sub(e.lastPubAt) >= autoLivenessMaxWait
			if e.flatHeld >= nativeFlatFallbck || stale {
				edgeX = -1 // honest unlocked refresh; never fabricate an edge
				f.Trigd = false
				publish = true
				e.flatHeld = 0
			}
		}
	}

	// SERIAL TRIGGER (serialtrig.go): decode the captured record and publish only
	// frames whose UART/I2C/SPI stream contains the armed pattern, re-anchoring on
	// the match. Resolved FIRST so the match anchor is the ONE anchor the zone/mask/
	// average/uniformity/Bode consumers all use. NOT gated on `lock` (async UART in
	// AUTO never edge-locks). NORM holds non-matching frames; AUTO shows one
	// unmatched LIVENESS frame every serialFallback holds (displayed, not observed).
	serialMatched := true // true when serial is not armed → everything observes normally
	if e.serialMode.Load() == SerialTrigger {
		serialMatched = false
		if publish { // only test frames that would otherwise publish
			if matched, anchor := e.serialQualify(f, cols, f.SampleS); matched {
				serialMatched = true
				e.serialHeld = 0
				e.serialMatches.Add(1)
				if anchor >= 0 && anchor < cols {
					edgeX = float64(anchor) // centre the matched byte
					f.Trigd = true
				}
			} else {
				e.serialHeld++
				if norm || e.serialHeld < serialFallback {
					publish = false
				} else {
					e.serialHeld = 0 // AUTO liveness: display it, but do not observe it
				}
			}
		}
	}
	observeOK := serialMatched // gate the "observe every locked frame" consumers

	// ZONE TRIGGER (zonemask.go): a locked frame must also QUALIFY against the
	// zones. NORM holds non-qualifying frames strictly; AUTO publishes one
	// unqualified liveness frame every zoneFallback holds.
	if publish && e.zoneMode.Load() == ZoneTrigger {
		qualified := lock && e.zonesQualify(f, cols, edgeX, f.SampleS)
		if qualified {
			e.zoneHeld = 0
		} else {
			e.zoneHeld++
			if norm || e.zoneHeld < zoneFallback {
				publish = false
			} else {
				e.zoneHeld = 0 // AUTO liveness: let one un(qualified) frame through
			}
		}
	}

	// MASK TEST (zonemask.go): every LOCKED frame is tested and counted, at the
	// full acquisition rate, published or held. Stop-on-fail freezes acquisition
	// ON the offending frame. observeOK excludes serial-REJECTED frames.
	liveDepth := 0
	if lock && observeOK && e.maskMode.Load() != MaskOff {
		liveDepth = validDepthP(disc, p)
		posFrac := math.Float64frombits(e.trigPosFrac.Load())
		if failed, stop := e.maskEval(f, cols, liveDepth, edgeX, f.SampleS, posFrac); failed && stop {
			e.maskStopped.Store(true)
			e.running.Store(false)
			publish = true // show the failing frame itself
			e.mu.Lock()
			e.stats.Running = false
			e.mu.Unlock()
		}
	}

	// FRA / Bode (bode.go): accumulate a transfer-function point between the
	// reference and DUT channels on every locked frame. It observes, not gates.
	if lock && observeOK && e.bodeMode.Load() == BodeOn {
		bd := validDepthP(disc, p)
		if bd <= 0 || bd > cols {
			bd = cols
		}
		e.bodeEval(f, bd, f.SampleS)
	}

	if !publish {
		edgeX = -1 // held frames never leave the arena; keep metadata sane
	}
	f.EdgeX = edgeX

	// AVERAGE (spec 03 §7.4): only published, coherent, edge-aligned frames
	// enter the ring; the published samples become the ring mean. The ring
	// clears on acq-mode/depth/band/NORM changes (avgKey tracks all four).
	if publish && observeOK && mode == AcqAverage && coherent && edgeX >= 0 {
		if n := int(e.avgCount.Load()); n > 1 {
			gen := e.avgGen.Load()
			width := e.band.WinCols()
			if e.avgKey.gen != gen || e.avgKey.width != width || e.avgKey.norm != norm {
				e.avg.reset(n, width)
				e.avgKey.gen, e.avgKey.width, e.avgKey.norm = gen, width, norm
			}
			e.avg.push(f, edgeX)
			e.avg.meanInto(f) // rewrites samples, Valid and EdgeX (centre)
			edgeX = f.EdgeX
		}
	}

	// Cross-frame uniformity telemetry over published frames (spec 03 §11).
	if publish && observeOK {
		e.uni.push(disc, e.band.WinCols(), edgeX)
		std, raw, worst := e.uni.stats()
		e.mu.Lock()
		e.stats.WinColStd, e.stats.WinColRaw, e.stats.WinColMax = std, raw, worst
		e.mu.Unlock()
	}

	vd := validDepthP(disc, p)
	armToLatchMs := float64(armToLatch) / float64(time.Millisecond)
	e.mu.Lock()
	e.stats.LastPtp = p
	e.stats.LastTrigPos = trigPos
	e.stats.ValidDepth = vd
	e.stats.MemDepth = int(e.memDepth.Load())
	e.stats.ArmToLatch = armToLatchMs
	e.stats.DrainMs = float64(drainMs) / float64(time.Millisecond)
	if coherent {
		e.stats.Coherent++
	}
	if haltOK {
		e.stats.HaltConfirm++
	}
	// Acquisition telemetry ring (instrumentation only): record every halt+drain
	// frame so a short/flat record can be traced to its wait/halt state.
	e.acqRing[e.acqHead] = AcqSample{
		Seq:          e.stats.Frames,
		Band:         e.stats.BandKind,
		ValidDepth:   vd,
		Cols:         cols,
		FillAtHalt:   e.lastFillAtHalt,
		HaltOK:       haltOK,
		SawTrig:      sawTrig,
		Anchored:     anchored,
		Filled:       filled,
		ArmToLatchMs: armToLatchMs,
		TrigPos:      trigPos,
		Half:         vd*10 < cols*6,
		Published:    publish,
		Norm:         norm,
		TdivS:        e.band.TdivS,
	}
	e.acqHead = (e.acqHead + 1) % len(e.acqRing)
	e.mu.Unlock()

	if publish {
		e.seq++
		f.Seq = e.seq
		e.arena.Publish()
		e.mu.Lock()
		e.stats.Published++
		e.stats.Seq = e.seq
		e.lastPubAt = e.clk.Now()
		e.pubTimes = append(e.pubTimes, e.lastPubAt)
		if len(e.pubTimes) > 64 {
			e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
		}
		e.mu.Unlock()
		// True single-shot: a real triggered frame just published — stop and
		// hold it. Gate on `lock`, NOT `coherent`: a quiet screen's honest FLAT
		// refresh must never consume the single-shot.
		if e.singleArmed.Load() && lock {
			e.singleArmed.Store(false)
			e.running.Store(false)
			e.mu.Lock()
			e.stats.Running, e.stats.Single = false, false
			e.mu.Unlock()
			e.logf("single-shot captured seq=%d — stopped", e.seq)
		}
	} else {
		e.mu.Lock()
		e.stats.Held++
		e.mu.Unlock()
	}

	// Wedge evidence must survive the drain path too (spec 03 §11): a frozen
	// fill fakes both the halt confirmation and "coherent", so reset the ladder
	// only on genuine activity — fill advancing or a non-flat drain.
	if fillMoved || p >= nativeEdgeMinPtp {
		e.resetDeadRuns()
	} else {
		e.deadEvidence(false)
	}
	e.paceHold(start, publish && f.Trigd) // holdoff extends the floor after a real trigger
}

// holdFrame accounts a decimated hold and feeds the wedge ladder: a quiet
// NORM keeps the fill advancing; a dead bus does not. A frozen SMALL fill at
// a decimated band cannot be counter saturation (a saturated counter would
// have set the filled gate), so it is certain wedge evidence.
func (e *Engine) holdFrame(fillMoved, norm bool) {
	e.mu.Lock()
	e.stats.Held++
	e.mu.Unlock()
	if fillMoved {
		e.resetDeadRuns()
		return
	}
	e.deadEvidence(true)
}

// pace enforces the ~50 ms frame-period floor (spec 03 §5.3): faster starves
// the single shared ARM core and lowers delivered fps.
func (e *Engine) pace(start time.Time) {
	if d := time.Duration(e.framePeriodNs.Load()) - e.clk.Now().Sub(start); d > 0 {
		e.sleepBeating(d)
	}
}
