package engine

import (
	"math"
	"time"
)

func (e *Engine) SetTrigType(t int) {
	if t < int(TrigEdge) || t > int(TrigVideo) {
		t = int(TrigEdge)
	}
	e.mu.Lock()
	e.tp.typ = TrigType(t)
	e.stats.TrigType = t
	e.mu.Unlock()
}

func (e *Engine) SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int) {
	e.mu.Lock()
	e.tp.pulseLvlFrac = clampFrac(lvlFrac)
	e.tp.pulseWMinNs, e.tp.pulseWMaxNs = wMinNs, wMaxNs
	e.tp.pulseCond = cond & 3
	e.mu.Unlock()
}

func (e *Engine) SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int) {
	e.mu.Lock()
	e.tp.slopeLoFrac = clampFrac(loFrac)
	e.tp.slopeHiFrac = clampFrac(hiFrac)
	e.tp.slopeTMinNs, e.tp.slopeTMaxNs = tMinNs, tMaxNs
	e.tp.slopeCond = cond & 3
	e.mu.Unlock()
}

func (e *Engine) SetVideoParams(std, line int, neg bool) {
	if std != 1 {
		std = 0
	}
	if line < 0 {
		line = 0
	}
	e.mu.Lock()
	e.tp.videoStd, e.tp.videoLine, e.tp.videoNeg = std, line, neg
	e.mu.Unlock()
}

func clampFrac(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func (e *Engine) SetAcqMode(m int) {
	if m < AcqNormal || m > AcqPeak {
		m = AcqNormal
	}
	e.acqMode.Store(int32(m))
	e.avgGen.Add(1) // mode change clears the average ring (spec 09 §2.2)
	e.mu.Lock()
	e.stats.AcqMode = m
	e.mu.Unlock()
}

func (e *Engine) SetAvgCount(n int) {
	if n < 1 {
		n = 1
	}
	if n > 256 {
		n = 256
	}
	e.avgCount.Store(int32(n))
	e.avgGen.Add(1)
	e.mu.Lock()
	e.stats.AvgCount = n
	e.mu.Unlock()
}

func (e *Engine) SetEresLen(l int) {
	l = clampEresLen(l)
	e.eresLen.Store(int32(l))
	e.mu.Lock()
	e.stats.EresLen = l
	e.mu.Unlock()
}

// SetSiggen enables/disables the in-fabric FAST-SIGNAL GENERATOR (fast_siggen.v,
// RUN[6] enable / RUN[7] shape) — a synthetic repetitive triangle (ramp=false) or ramp
// (ramp=true) in the 200 MHz interleave domain that drives BOTH the normal capture
// record (so the host reference-lock engages) AND the sr_accum align input (so the
// device COMBINE grid is coherent). It proves the whole super-res chain LIVE with NO
// bench signal. Off by default => byte-for-byte identical fabric.
//
// The per-frame arm (armEngineQuiet) writes ONLY OP_GO — it carries NO RUN write — so
// storing siggenEn alone would never reach selRun during normal free-run acquisition
// (the capture record would stay flat and the host reference-lock could never seed).
// selRun is a persistent fabric register and every runWord() write already ORs the
// current siggen state, so ONE re-assert on the toggle is sufficient: raise siggenDirty
// and let the bus-owner boundary (serviceCommands) write selRun=runWord() once. Off =>
// runWord() is byte-identical, so the re-write is a no-op change to the fabric.
func (e *Engine) SetSiggen(on, ramp bool) {
	e.siggenEn.Store(on)
	e.siggenRamp.Store(ramp)
	e.siggenDirty.Store(true)
}

func (e *Engine) SetRunning(on bool) {
	e.running.Store(on)
	e.singleArmed.Store(false) // an explicit RUN or STOP both cancel a pending single-shot
	if on {
		e.maskStopped.Store(false) // resuming releases the stop-on-fail latch
	}
	e.mu.Lock()
	e.stats.Running = on
	e.stats.Single = false
	e.mu.Unlock()
}

// SetSingle arms a true single-shot (spec 05 §3 note): NORM-armed, and the
// engine STOPs itself after the next triggered frame publishes. RUN cancels
// it. This is the "capture one and hold" behaviour a scope SINGLE button
// gives — unlike plain NORM, which keeps re-publishing triggered frames.
func (e *Engine) SetSingle() {
	e.SetNorm(true)
	e.running.Store(true)
	e.singleArmed.Store(true)
	e.mu.Lock()
	e.stats.Running, e.stats.Single = true, true
	e.mu.Unlock()
}

// SetChannelVdiv records a channel's V/div and its active per-detent trigger
// cal (Zero, CPV — from the analog front end) so the trigger level maps to a
// display code for level-anchored centring at the CORRECT per-detent slope.
// zero/cpv ≤ 0 fall back to the global fit.
func (e *Engine) SetChannelVdiv(ch int, vdivV, zero, cpv float64) {
	if vdivV <= 0 {
		vdivV = 1
	}
	e.chVdivBits[ch&1].Store(math.Float64bits(vdivV))
	if cpv <= 0 {
		zero, cpv = trigZeroDefault, trigCPVDefault
	}
	e.trigZero[ch&1].Store(math.Float64bits(zero))
	e.trigCPV[ch&1].Store(math.Float64bits(cpv))
}

// TrigVoltsAt converts a trigger DAC code to (un-probed) input volts at the
// given source channel's active per-detent cal — the per-detent replacement for
// the package-level TrigLevelVolts (which assumes the global fit).
func (e *Engine) TrigVoltsAt(code uint16, srcCh int) float64 { return e.trigVolts(code, srcCh) }

// SetChannelOffsetV records a channel's applied input-referred offset volts
// (from the analog front end) so trigDispLevel places the discrimination level
// on the same offset-shifted reference as the drained samples and the markers.
func (e *Engine) SetChannelOffsetV(ch int, offV float64) {
	e.trigOffV[ch&1].Store(math.Float64bits(offV))
}

// trigVolts converts a trigger DAC code to (un-probed) input volts at the given
// source channel, using that channel's active per-detent cal.
func (e *Engine) trigVolts(code uint16, srcCh int) float64 {
	zero := math.Float64frombits(e.trigZero[srcCh&1].Load())
	cpv := math.Float64frombits(e.trigCPV[srcCh&1].Load())
	if cpv <= 0 {
		zero, cpv = trigZeroDefault, trigCPVDefault
	}
	return (zero - float64(code)) / cpv
}

// SetTrigPosFrac sets where the trigger sits horizontally on screen: 0=left,
// 0.5=centre (default), 1=right. Pure software — the display window is offset
// so the anchor lands at this fraction (spec 05 §8: position is software).
func (e *Engine) SetTrigPosFrac(frac float64) {
	if math.IsNaN(frac) {
		frac = 0.5 // a NaN would silently un-map every mask column downstream
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	e.trigPosFrac.Store(math.Float64bits(frac))
	e.mu.Lock()
	e.stats.TrigPosFrac = frac
	e.mu.Unlock()
}

func (e *Engine) chVdivV(ch int) float64 {
	b := e.chVdivBits[ch&1].Load()
	if b == 0 {
		return 1
	}
	return math.Float64frombits(b)
}

// trigDispLevel maps the HW trigger level (DAC code) to a display code
// (0..255) at the source channel's V/div — the same mapping the on-screen
// level line uses. Returns -1 when no level is set (boot comparator), so
// centring falls back to the mid-level crossing.
func (e *Engine) trigDispLevel(srcCh int) int {
	e.mu.Lock()
	code := e.trigCode
	e.mu.Unlock()
	if code == 0 {
		return -1
	}
	// Include the source channel's offset: the drained samples are shifted by
	// the offset DAC, and the HW comparator fires on that shifted signal (the
	// firing code moves ~cpv codes per offset-volt, measured). So the level the
	// samples cross is (trigVolts + offset) — omitting it puts the anchor (and
	// the markers) at the wrong height, so the trigger never lines up with the
	// wave. Probe cancels: both trigVolts and offset are BNC-referred, matching
	// the raw ADC codes.
	offV := math.Float64frombits(e.trigOffV[srcCh&1].Load())
	dc := int(math.Round(128 + (e.trigVolts(code, srcCh)+offV)*25/e.chVdivV(srcCh)))
	if dc < 0 {
		dc = 0
	}
	if dc > 255 {
		dc = 255
	}
	return dc
}

func (e *Engine) SetNorm(on bool) {
	e.mu.Lock()
	e.norm = on
	e.stats.Norm = on
	e.mu.Unlock()
	e.hintReset.Store(true)
}

// SetTdiv stages a timebase change; it is applied at the next frame boundary
// with a full bring-up. Returns the resolved band, or ok=false if the value
// is not a v1 ladder detent.
func (e *Engine) SetTdiv(tdivS float64) (Band, bool) {
	b, ok := PlanTdiv(tdivS)
	if !ok {
		return Band{}, false
	}
	e.mu.Lock()
	e.pendBand, e.pendSet = b, true
	e.mu.Unlock()
	e.hintReset.Store(true)
	return b, true
}

// SetMemDepth sets the decimated drain depth in samples — the fps↔data knob.
// Shallow (down to one screen, decimWin) = highest frame rate; deep (up to the
// physical deepRecord) = more captured record to scroll, at a lower frame rate
// (a deeper record spans proportionally more capture time). Clamped to a valid
// range; native-fast/envelope/roll are unaffected.
func (e *Engine) SetMemDepth(samples int) int {
	if samples < decimWin {
		samples = decimWin
	}
	if samples > deepRecord {
		samples = deepRecord
	}
	e.memDepth.Store(int32(samples))
	return samples
}

// SetFramePeriod sets the publish pacing floor in milliseconds. 0 = run
// captures back-to-back at the hardware rate (the stream/stitch basis). Returns
// the applied value (clamped to [0, 1000] ms).
func (e *Engine) SetFramePeriod(ms int) int {
	if ms < 0 {
		ms = 0
	}
	if ms > 1000 {
		ms = 1000
	}
	e.framePeriodNs.Store(int64(ms) * int64(time.Millisecond))
	return ms
}

// SetHoldoff sets the trigger holdoff in seconds: after a triggered frame the
// engine waits at least this long before re-arming, so it re-triggers on the
// same event in a complex/bursty waveform instead of an intermediate edge.
// 0 disables it. Clamped to [0, 10] s. Returns the applied value.
func (e *Engine) SetHoldoff(sec float64) float64 {
	if sec < 0 {
		sec = 0
	}
	if sec > 10 {
		sec = 10
	}
	e.holdoffNs.Store(int64(sec * float64(time.Second)))
	e.mu.Lock()
	e.stats.HoldoffS = sec
	e.mu.Unlock()
	return sec
}

// paceHold is pace() with the trigger holdoff folded in: after a genuinely
// triggered publish it raises the inter-frame floor to the holdoff, delaying
// the next arm. Untriggered/AUTO frames pace at the normal floor.
func (e *Engine) paceHold(start time.Time, triggered bool) {
	floor := time.Duration(e.framePeriodNs.Load())
	if triggered {
		if h := time.Duration(e.holdoffNs.Load()); h > floor {
			floor = h
		}
	}
	if d := floor - e.clk.Now().Sub(start); d > 0 {
		e.sleepBeating(d) // holdoff can be 10 s — must beat through it
	}
}

// SetStreamMode toggles the stitched high-bandwidth streaming decode mode: the
// FSM captures back-to-back deep records with a PURE TIMED wait (no trigger /
// saturation poll — that wait was the recoverable overhead), publishing every
// window with continuity metadata so the client stitches them. It forces the
// deep record, un-paces publishing, and only runs on decimated bands (native-fast
// is burst-only via SINGLE; roll/envelope are their own paths). Returns the
// applied state.
func (e *Engine) SetStreamMode(on bool) bool {
	if on {
		e.memDepth.Store(deepRecord)
		e.SetFramePeriod(0)
	} else {
		e.SetFramePeriod(50)
	}
	e.streamMode.Store(on)
	e.mu.Lock()
	e.stats.Stream = on
	e.mu.Unlock()
	return on
}

// effDrainCols is how many samples oneFrame actually drains: the configured
// memory depth on decimated bands, the band's own drain elsewhere. A SINGLE
// capture always drains the FULL deep record so the one frame you keep carries
// everything to zoom out into — frame rate is irrelevant for a single shot.
func (e *Engine) effDrainCols() int {
	switch e.band.Kind() {
	case KindDecimated:
		if e.singleArmed.Load() {
			return deepRecord
		}
		d := int(e.memDepth.Load())
		if d < decimWin {
			d = decimWin
		}
		if d > deepRecord {
			d = deepRecord
		}
		return d
	case KindRoll:
		// Roll drains rollBatch samples per time-boxed update (rollUpdate), NOT
		// the rollWin display ring. Sizing the fabric record to rollBatch keeps
		// pretrig (= rollBatch/2) reachable within the roll budget so the record
		// coheres — a rollWin-sized record has pretrig=rollWin/2 that the budget
		// can't fill, leaving the band a flat coherent-gated blank.
		return rollBatch
	}
	return e.band.DrainCols()
}

// capDepth is effDrainCols() clamped to the largest programmable record
// (REC_DEPTH-MARGIN, the exact-window invariant). It is the pre/post record size
// bringUp programs AND the value the loop compares against lastCapCols — using
// the SAME clamped quantity on both sides is what keeps them in agreement. (A
// prior version stored the clamped value but compared the unclamped effDrainCols,
// so at max depth 20480 != 20478 fired the re-program guard every frame.)
func (e *Engine) capDepth() int {
	c := e.effDrainCols()
	if c > deepRecord-2 {
		c = deepRecord - 2
	}
	return c
}

// SetTrigLevelCode stages a trigger-level DAC recommit. Codes clamp to the
// operational window. Compare-on-change with an init flag so the first set
// applies even if equal to the default. Code 0 means "keep the boot-inherited
// comparator" (spec 05): nothing is staged and 0 is returned.
func (e *Engine) SetTrigLevelCode(code uint16) uint16 {
	if code == 0 {
		return 0
	}
	if code < TrigCodeMin {
		code = TrigCodeMin
	}
	if code > TrigCodeMax {
		code = TrigCodeMax
	}
	e.mu.Lock()
	changed := !e.trigInit || code != e.trigCode
	if changed {
		e.trigCode, e.trigDirty, e.trigInit = code, true, true
		e.stats.TrigCode = code
	}
	e.mu.Unlock()
	if changed {
		e.hintReset.Store(true)
	}
	return code
}

// SetOffsetDAC stages a vertical-offset DAC write for a channel (0=C1,
// 1=C2). Codes are producer-clamped (analog.OffsetCode); the shadow is
// last-write-wins with no compare-on-change — redundant-traffic suppression
// is the producer's job (spec 09 §1.3).
func (e *Engine) SetOffsetDAC(ch int, code uint16) {
	if ch != 1 {
		ch = 0
	}
	e.mu.Lock()
	e.offCode[ch], e.offDirty[ch] = code, true
	if ch == 0 {
		e.stats.OffC1 = code
	} else {
		e.stats.OffC2 = code
	}
	e.mu.Unlock()
}

func (e *Engine) SetTrigSlope(rising bool) {
	e.trigRising.Store(rising)
	e.mu.Lock()
	e.stats.TrigRising = rising
	e.mu.Unlock()
	e.hintReset.Store(true)
}

// SetETS stages the equivalent-time opt-in (spec 04 §3: never auto-routed;
// only effective at tdiv ≤ 50 ns). Applied at the frame boundary like a band
// change.
func (e *Engine) SetETS(on bool) {
	e.mu.Lock()
	e.etsWant = on
	e.stats.ETS = on
	e.mu.Unlock()
}

func (e *Engine) SetTrigSource(ch int) {
	if ch != 1 {
		ch = 0
	}
	e.trigSrc.Store(int32(ch))
	e.mu.Lock()
	e.stats.TrigSource = ch
	e.mu.Unlock()
	e.hintReset.Store(true)
}
