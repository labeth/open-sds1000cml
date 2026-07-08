package engine

import (
	"math"
	"open-sds/app/internal/bus"
	"time"
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

	if v, err := e.b.Read(bus.PlaneCS1, selVersion); err != nil || v != bus.VersionMagic {
		e.logf("engine: version gate failed (v=%#04x err=%v) — refusing to drive", v, err)
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

	for !e.stopReq.Load() {
		e.serviceCommands()
		e.bumpFrames() // heartbeat advances every iteration, stopped or not

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

// oneFrame runs one arm→wait→halt→drain→re-arm→publish iteration.
func (e *Engine) oneFrame(norm bool) {
	start := e.clk.Now()

	e.armEngine()
	anchored, sawTrig, filled, fillMoved, trigPos := e.waitCapture(norm)
	armToLatch := e.clk.Now().Sub(start)
	if e.stopReq.Load() {
		return
	}

	nativeFast := e.band.NativeFast()
	// Decimated readiness: NORM requires a real trigger with the post-trigger
	// record filled (0x46 counts post-trigger samples, so it only advances
	// after a comparator edge). AUTO always drains — either the fast
	// triggered path, or, after the budget, the frozen free-running buffer
	// (which holds a coherent 2048-sample snapshot of the live signal). This
	// is what makes an untriggered AUTO display update at the full rate
	// instead of holding on every frame that fails to trigger.
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
	e.drain(f, cols)
	drainMs := e.clk.Now().Sub(drainStart)
	e.armEngine() // re-arm immediately: filling again before publish/render

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
	if int(e.trigSrc.Load()) == 1 {
		disc = f.C2[:cols]
	}
	lo, hi, p := ptp(disc)
	rising := e.trigRising.Load()

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
		// EDGE: anchor on the user's HW trigger level (WYSIWYG — the display
		// crosses where the level is set). A lock requires a right-slope crossing
		// AT that level (centerCross returns ONLY a crossing of the requested
		// slope, so edgeX ≥ 0 already means "a right-slope crossing exists").
		// If the level is SET but off the signal band, NO trigger is possible —
		// we must NOT fall back to a mid-level crossing and fabricate a lock the
		// user never asked for (spec 05 §5.1). Leaving edgeX = -1 means no lock:
		// AUTO free-runs unlocked, NORM holds. The mid-level fallback survives
		// only for an UNSET (boot) level, to keep the very first frames stable.
		if td := e.trigDispLevel(int(e.trigSrc.Load())); td >= 0 {
			edgeX = centerCross(disc, td, rising)
			if p >= nativeEdgeMinPtp { // a real signal the level can sit outside of
				margin := (hi - lo) / 16
				lvlOffSig = td < lo-margin || td > hi+margin
			}
		} else {
			edgeX = centerCross(disc, midLevel(disc), rising)
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
	f.HaltOK = haltOK
	f.RollCodes = false
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm

	// Publish policy — ONLY DISPLAY FRAMES THAT HAVE A LOCK (spec 03 §7.4, spec 05 §4,
	// spec 04 §8.2). A "lock" is a validated triggered event on the captured CONTENT:
	//   native-fast: real edge content (ptp ≥ nativeEdgeMinPtp) AND a right-slope crossing —
	//                the done-gate is unreliable here so content decides (spec 03 §6, §8.2)
	//   decimated:   a COHERENT capture AND a right-slope crossing (spec 05 §4.2)
	//   qualifier:   a qualifying PULSE/SLOPE/VIDEO event (its own edgeX) on the same basis
	// The gate is a right-slope crossing (edgeX ≥ 0) on a REAL signal (ptp ≥ nativeEdgeMinPtp
	// rejects a flat rail / noise), plus a COHERENT capture on decimated bands. We deliberately
	// do NOT fold in windowSlopeMatches here: that plateau test is reliable only while winCols/4
	// stays within one signal period, so at a dense multi-period window (1–2 ms) landing on a
	// non-integer phase it false-rejects a genuine edge and silently freezes the band. The
	// right-slope crossing + amplitude + coherence already define the lock. No lock → HOLD the
	// last locked frame; never flash a jittery un-anchored capture. A genuinely flat / no-signal
	// screen has no lock to be had: AUTO (and native-fast in either mode, spec §8.2) keeps it
	// live with one honest flat capture (EdgeX = -1) every nativeFlatFallbck held frames.
	// HOLDING — not free-running — between fallbacks re-presents the last edge, so an
	// intermittent-edge sub-period band (2–20 µs) shows a stable held edge, never an edge↔flat
	// flicker.
	qualifier := tp.typ != TrigEdge

	lock := edgeX >= 0 && (qualifier || p >= nativeEdgeMinPtp)
	if !nativeFast {
		lock = lock && coherent
	}

	publish := false
	switch {
	case lock:
		// A triggered / qualified edge is present — the native-fast comparator fired (sawTrig)
		// so the edge is in the record, or a coherent slope-valid decimated capture — publish
		// it centred.
		publish = true
		e.flatHeld = 0
	case !norm && !qualifier && lvlOffSig && coherent:
		// AUTO, EDGE: the trigger level is off the signal entirely — a lock is
		// impossible, so never claim a trig and never freeze. FREE-RUN an unlocked
		// live capture at the record centre (EdgeX = -1, Trigd = false). NORM
		// instead HOLDs (waits for a trigger that cannot come) via the default.
		publish = true
		edgeX = -1
		f.Trigd = false
		e.flatHeld = 0
	case nativeFast && !norm && !qualifier && !sawTrig:
		// AUTO native-fast, comparator did NOT fire within the budget (untriggered): FREE RUN a
		// live refresh at the record centre (spec 04 §3 routing + §11) instead of holding. This
		// is the different technique the ≤200 ns bands need — there the record spans ≪ one
		// period so the edge rarely aligns and a catch-and-HOLD would freeze (the ~0 fps case);
		// it keeps any quiet native-fast screen live at ~20 fps. Uncentred (EdgeX = -1, the
		// record centre where a caught edge is HW-positioned): no software anchor on noise.
		publish = true
		edgeX = -1
		e.flatHeld = 0
	case (nativeFast || !norm) && !qualifier && p < nativeEdgeMinPtp:
		// NORM native-fast flat (trigger-hold with an honest 60-frame refresh), or AUTO
		// decimated flat: publish one honest flat capture every nativeFlatFallbck held frames.
		e.flatHeld++
		if e.flatHeld >= nativeFlatFallbck {
			edgeX = -1 // one honest flat capture; never fabricate an edge
			publish = true
			e.flatHeld = 0
		}
	default:
		// NORM decimated quiet screen, an un-fired qualifier, or AUTO decimated signal-present-
		// but-not-locked this frame (it would jitter) → HOLD the last locked frame.
		publish = false
	}
	// SERIAL TRIGGER (serialtrig.go): decode the captured record and publish only
	// frames whose UART/I2C/SPI stream contains the armed byte/address pattern,
	// re-anchoring the display on the match. Resolved FIRST so the match anchor is
	// the ONE consistent anchor the zone gate, mask, average and uniformity below
	// all use (resolving it BETWEEN gates split them onto different anchors). NOT
	// gated on `lock` — async UART in AUTO never edge-locks, so decode every
	// publish candidate. NORM holds non-matching frames strictly; AUTO shows one
	// unmatched LIVENESS frame every serialFallback holds. A liveness frame is
	// DISPLAYED but not OBSERVED: with serial armed, only MATCHED frames feed the
	// mask/average/uniformity/Bode consumers, so a non-match can never trip
	// stop-on-fail or smear an average against a wandering anchor.
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
	// zones — a graphical software trigger in the same publish policy. NORM
	// holds non-qualifying frames strictly; AUTO publishes one unqualified
	// liveness frame every zoneFallback holds (same idea as the flat fallback).
	// Un-locked AUTO publishes (free-run refreshes, EdgeX=-1) cannot be zone-
	// tested at all — throttle them by the same counter so a broken/absent lock
	// doesn't stream unfiltered frames past an active zone trigger.
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

	// MASK TEST (zonemask.go): every LOCKED frame is tested and counted, at
	// the full acquisition rate, published or held. The dead tail of a deep
	// drain is excluded (validDepth). Stop-on-fail freezes acquisition ON the
	// offending frame — it force-publishes so the screen shows the failure,
	// not whatever a zone hold left there. observeOK excludes serial-REJECTED
	// frames so a non-match can never trip stop-on-fail.
	liveDepth := 0
	if lock && observeOK && e.maskMode.Load() != MaskOff {
		liveDepth = validDepth(disc)
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
	// reference and DUT channels on every locked frame. Independent of the
	// publish policy — it observes the signal, does not gate it.
	if lock && observeOK && e.bodeMode.Load() == BodeOn {
		bd := validDepth(disc)
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
	// enter the ring; the published samples become the ring mean. Flat
	// fallbacks publish RAW. The ring clears on acq-mode/depth/band/NORM
	// changes (avgKey tracks all four).
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
	// observeOK excludes serial liveness frames whose anchor differs from a match.
	if publish && observeOK {
		e.uni.push(disc, e.band.WinCols(), edgeX)
		std, raw, worst := e.uni.stats()
		e.mu.Lock()
		e.stats.WinColStd, e.stats.WinColRaw, e.stats.WinColMax = std, raw, worst
		e.mu.Unlock()
	}

	e.mu.Lock()
	if coherent {
		e.stats.Coherent++
	}
	if haltOK {
		e.stats.HaltConfirm++
	}
	e.stats.LastPtp = p
	e.stats.LastTrigPos = trigPos
	e.stats.ValidDepth = validDepth(disc)
	e.stats.MemDepth = int(e.memDepth.Load())
	e.stats.ArmToLatch = float64(armToLatch) / float64(time.Millisecond)
	e.stats.DrainMs = float64(drainMs) / float64(time.Millisecond)
	e.mu.Unlock()

	if publish {
		e.seq++
		f.Seq = e.seq
		e.arena.Publish()
		e.mu.Lock()
		e.stats.Published++
		e.stats.Seq = e.seq
		e.pubTimes = append(e.pubTimes, e.clk.Now())
		if len(e.pubTimes) > 64 {
			e.pubTimes = e.pubTimes[len(e.pubTimes)-64:]
		}
		e.mu.Unlock()
		// True single-shot: a real triggered frame just published — stop and
		// hold it. `coherent` here is a NORM/qualifier-gated capture (single
		// forces NORM), so this is a genuine trigger, not a free-run frame.
		if e.singleArmed.Load() && coherent {
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
	// fill fakes both the halt confirmation (equal double-read) and
	// "coherent", so reset the ladder only on genuine activity — fill
	// advancing or a non-flat drain.
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
