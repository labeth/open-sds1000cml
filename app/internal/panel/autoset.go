package panel

import (
	"math"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

const divX = 10 // horizontal graticule divisions (spec 07)

// settleMs is how long autoset waits after each scale change before re-measuring
// — long enough for the offset DAC to move AND a fresh frame to publish (~13 fps)
// so the trigger level is read off the trace we actually settled on.
const settleMs = 220

// Cycle counts for the two autoset scales: fit the vertical over many cycles so
// Vpp is read on the full swing (measCycles), then show a few cycles (dispCycles).
const (
	measCycles = 30
	dispCycles = 4
)

// SetFrameSource wires the latest-frame accessor (fo.WithFrame) so autoset can
// measure the live signal. Optional — without it, AUTO falls back to plain run.
func (c *Controller) SetFrameSource(fn func(func(*engine.Frame))) { c.frameFn = fn }

// autoset is the AUTO button: it starts a background sweep that fits the whole
// scope to the live signal. A second AUTO press while it's running CANCELS it
// (the LCD shows "AUTOSET…" with that hint). Multi-frame — measuring at the old
// scale then applying a big scale change never translates (offset/trigger land
// off-screen), so it settles and RE-MEASURES between steps.
func (c *Controller) autoset() {
	if c.frameFn == nil { // no frame source: best-effort AUTO/run
		c.norm, c.running = false, true
		c.eng.SetNorm(false)
		c.eng.SetRunning(true)
		c.pushLEDs()
		return
	}
	c.mu.Lock()
	if c.autosetBusy { // second press cancels
		if c.autosetStop != nil {
			close(c.autosetStop)
			c.autosetStop = nil
		}
		c.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	c.autosetBusy, c.autosetStop, c.autosetMsg = true, stop, "AUTOSET..."
	c.mu.Unlock()
	c.pushLEDs()
	go c.runAutoset(stop)
}

func (c *Controller) runAutoset(stop chan struct{}) {
	defer func() {
		c.mu.Lock()
		c.autosetBusy = false
		if c.autosetStop == stop {
			c.autosetStop = nil
		}
		c.mu.Unlock()
		c.pushLEDs()
	}()
	cancelled := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	// Snapshot the entry scale so a no-signal sweep can restore it instead of
	// abandoning the user on the coarse sweep state.
	entry := c.eng.Snapshot()
	entryTdiv := entry.TdivS
	var entryVidx [2]int
	var entryOff [2]float64
	if c.fe != nil {
		entryVidx, _ = c.fe.Snapshot()
		entryOff[0] = analog.OffsetVolts(0, entry.OffC1)
		entryOff[1] = analog.OffsetVolts(1, entry.OffC2)
	}
	restore := func() {
		if entryTdiv > 0 {
			c.setTdivNearest(entryTdiv)
		}
		if c.fe != nil {
			for ch := 0; ch < 2; ch++ {
				c.setVdiv(ch, entryVidx[ch])
				c.fe.SetOffset(ch, entryOff[ch])
			}
		}
	}
	// No-signal exit: restore the scale and show a brief "no signal" banner.
	noSignal := func() {
		restore()
		c.mu.Lock()
		c.autosetMsg = "AUTOSET: no signal"
		c.mu.Unlock()
		c.pushLEDs()
		c.waitFrame(stop) // hold the note briefly (cancelable)
		c.waitFrame(stop)
	}
	c.eng.SetNorm(false)
	c.eng.SetRunning(true)
	c.mu.Lock()
	c.norm, c.running = false, true // keep the shadows in step (RUN/STOP + LEDs)
	c.mu.Unlock()

	// 1. Coarse vertical first so nothing is railed (a clipped trace measures as
	//    ~0 Vpp and can't be fit), then sweep the timebase fast→slow to find the
	//    signal (an unknown frequency needs a scale where a few cycles are on
	//    screen and native — not decimated — so the frequency reads true).
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			c.setVdiv(ch, nearestDetent(2.0)) // 2 V/div: ±8 V unclipped
			c.fe.SetOffset(ch, 0)
		}
	}
	// If we START on an envelope/roll band (≥5 ms/div), the direct jump to
	// 1 µs/div in the sweep transitions through a slow, envelope-publishing
	// state and the first measurable frame can arrive aliased. Drop to a
	// decimated band FIRST and wait for a real per-sample frame, so the sweep
	// begins from a clean decimated state (autoset from ≥5 ms/div used to
	// converge on a low aliased frequency).
	if entry.TdivS >= 5e-3 {
		ss := c.setTdivNearest(500e-6)
		if !c.waitBandFrame(stop, ss) {
			return
		}
	}
	sweep := []float64{1e-6, 1e-5, 1e-4, 1e-3, 1e-2}
	var found *measure.Result
	var foundCh int
	for _, td := range sweep {
		if cancelled() {
			return
		}
		ss := c.setTdivNearest(td)
		if !c.waitBandFrame(stop, ss) {
			return
		}
		m, _ := c.measureChans()
		// "Found" requires a measurable FREQUENCY at this band, not just
		// amplitude: a low-duty pulse train (e.g. a 400 µs-period, 100 µs-wide
		// pulse) shows only a brief edge at a fast band — enough amplitude to
		// trip a Vpp test but NO visible period, so autoset would lock a far-
		// too-fast timebase and read no frequency. Requiring Freq>0 makes the
		// sweep continue to a band where the repetition is actually resolved.
		// The LAST (slowest) sweep step falls back to amplitude-only so a
		// genuinely aperiodic-looking capture is still fitted rather than lost.
		lastStep := td == sweep[len(sweep)-1]
		fq := func(r *measure.Result) bool { return has(r) && r.Freq > 0 }
		if fq(m[0]) || fq(m[1]) || (lastStep && (has(m[0]) || has(m[1]))) {
			foundCh = strongerCh(m)
			if fq(m[0]) || fq(m[1]) { // prefer the channel that actually has timing
				foundCh = 0
				if fq(m[1]) && (!fq(m[0]) || m[1].Vpp > m[0].Vpp) {
					foundCh = 1
				}
			}
			found = m[foundCh]
			break
		}
	}
	if found == nil || found.Freq <= 0 {
		noSignal() // restore the user's scale + tell them nothing was found
		return
	}

	// 2. Fit the VERTICAL from a window holding MANY cycles: a short capture of an
	//    aperiodic/serial signal (UART, a bursty stream) can miss the full swing,
	//    under-reading Vpp — which then picks a too-sensitive V/div that CLIPS.
	//    Measuring over ~30 cycles captures the true min/max regardless of pattern.
	ss := c.setTdivNearest((measCycles / found.Freq) / divX)
	if !c.waitBandFrame(stop, ss) {
		return
	}
	m, _ := c.measureChans()
	if !has(m[0]) && !has(m[1]) {
		noSignal()
		return
	}

	// 3. Vertical: fit Vpp into <6 of 8 divisions, centred on the MIDPOINT
	//    (Vmax+Vmin)/2 — NOT the mean, which for a duty-cycled signal (UART idles
	//    high) skews toward a rail and would push the other rail off-screen.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if !has(m[ch]) {
				continue
			}
			p := c.fe.ProbeFactor(ch)
			c.setVdiv(ch, detentForVpp(m[ch].Vpp/p))
			c.fe.SetOffset(ch, -(m[ch].Vmax+m[ch].Vmin)/2/p)
		}
		if !c.waitFrame(stop) {
			return
		}
	}

	// 3b. Close the loop on centring: the offset-DAC volts→code model drifts across
	//     detents (up to a couple divisions), so read where the trace ACTUALLY
	//     landed and nudge the offset to bring the mean to screen centre. This is
	//     model-independent — it works off the raw codes.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if !has(m[ch]) {
				continue
			}
			errDiv := c.rawCenterErr(ch) // +ve => trace sits above centre
			if errDiv > 0.4 || errDiv < -0.4 {
				snap, _ := c.fe.Snapshot()
				vdiv := analog.Detents[snap[ch]].VdivV
				c.fe.SetOffset(ch, c.fe.OffsetReqV(ch)-errDiv*vdiv)
			}
		}
		if !c.waitFrame(stop) {
			return
		}
	}

	// 4. Trigger: re-measure the now-centred trace and put an EDGE trigger at its
	//    midpoint on the stronger channel (this is why it must be measured AFTER
	//    the vertical settles — else the level lands off-screen).
	m, _ = c.measureChans()
	src := strongerCh(m)
	if !has(m[src]) {
		return
	}
	p := 1.0
	if c.fe != nil {
		p = c.fe.ProbeFactor(src)
	}
	mid := (m[src].Vmax + m[src].Vmin) / 2
	code := 31434 - int(938*mid/p+0.5) // inverse of engine.TrigLevelVolts
	if code < engine.TrigCodeMin {
		code = engine.TrigCodeMin
	}
	if code > engine.TrigCodeMax {
		code = engine.TrigCodeMax
	}
	c.eng.SetTrigLevelCode(uint16(code))
	c.mu.Lock()
	c.trigCode = uint16(code) // guarded — the panel goroutine also touches trigCode
	c.mu.Unlock()
	c.eng.SetTrigSource(src)
	c.eng.SetTrigType(0) // EDGE

	// 5. Finally, set the DISPLAY timebase to a few cycles of the (accurately
	//    measured) signal for a clean, well-resolved view. The trigger level is in
	//    volts, so this timebase change does not disturb it.
	if f := m[src].Freq; f > 0 {
		c.setTdivNearest((dispCycles / f) / divX)
	}
}

// waitFrame sleeps ~one publish interval so a frame at the new scale exists,
// returning false if autoset was cancelled during the wait.
func (c *Controller) waitFrame(stop chan struct{}) bool {
	select {
	case <-stop:
		return false
	case <-time.After(settleMs * time.Millisecond):
		return true
	}
}

// measureChans reads the latest frame and computes each channel's measurement in
// tip-referred volts (using the CURRENT V/div + offset), plus whether it holds
// real signal.
func (c *Controller) measureChans() ([2]*measure.Result, bool) {
	st := c.eng.Snapshot()
	var m [2]*measure.Result
	ok := false
	c.frameFn(func(f *engine.Frame) {
		if f == nil || len(f.C1) == 0 || f.IsEnv {
			return
		}
		ok = true
		valid := f.Valid
		if valid < 1 {
			valid = 1
		}
		if valid > len(f.C1) {
			valid = len(f.C1)
		}
		off := [2]uint16{st.OffC1, st.OffC2}
		for ch := 0; ch < 2; ch++ {
			var sig []uint8
			if ch == 0 {
				sig = f.C1[:valid]
			} else if len(f.C2) >= valid {
				sig = f.C2[:valid]
			} else {
				continue
			}
			idx := analog.BootDetent
			probe, offV := 1.0, 0.0
			if c.fe != nil {
				snap, _ := c.fe.Snapshot()
				idx = snap[ch]
				probe = c.fe.ProbeFactor(ch)
				offV = c.fe.OffsetVolts(ch, off[ch]) // calibrated per-detent zero, not the fixed fallback
			}
			vdiv := analog.Detents[idx].VdivV
			m[ch] = measure.Compute(sig, vdiv/32*probe, offV*probe, f.SampleS)
		}
	})
	return m, ok
}

// rawCenterErr reports how far a channel's signal MIDPOINT ((min+max)/2) sits from
// screen centre, in divisions (+ve = above centre), read straight off the ADC codes
// (128 = centre, 32 codes/div). Uses 1%-trimmed rails so a stray spike can't skew it,
// and the midpoint (not the mean) so duty cycle doesn't matter. Model-independent, so
// it corrects any offset-DAC calibration drift.
func (c *Controller) rawCenterErr(ch int) float64 {
	var errDiv float64
	c.frameFn(func(f *engine.Frame) {
		if f == nil {
			return
		}
		sig := f.C1
		if ch == 1 {
			sig = f.C2
		}
		valid := f.Valid
		if valid < 1 || valid > len(sig) {
			valid = len(sig)
		}
		if valid < 8 {
			return
		}
		var h [256]int
		for _, v := range sig[:valid] {
			h[v]++
		}
		trim := valid / 100 // ignore the extreme 1% each end
		lo, hi, acc := 0, 255, 0
		for i := 0; i < 256; i++ {
			if acc += h[i]; acc > trim {
				lo = i
				break
			}
		}
		acc = 0
		for i := 255; i >= 0; i-- {
			if acc += h[i]; acc > trim {
				hi = i
				break
			}
		}
		errDiv = (float64(lo+hi)/2 - 128) / 32
	})
	return errDiv
}

func has(r *measure.Result) bool { return r != nil && r.Vpp > 0.02 }

func strongerCh(m [2]*measure.Result) int {
	if has(m[1]) && (!has(m[0]) || m[1].Vpp > m[0].Vpp) {
		return 1
	}
	return 0
}

// AutosetBusy reports whether an autoset sweep is in progress (for the LCD hint).
func (c *Controller) AutosetBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autosetBusy
}

// setVdiv applies a vertical detent and keeps the shadow in sync (guarded).
func (c *Controller) setVdiv(ch, idx int) {
	if c.fe == nil {
		return
	}
	_ = c.fe.SetVdiv(ch, idx)
	c.mu.Lock()
	c.vIdx[ch] = idx
	c.mu.Unlock()
}

// setTdivNearest snaps the timebase ladder to the detent nearest target and
// returns the new band's per-sample seconds (0 if no ladder) so the caller can
// wait for a frame that ACTUALLY reflects the new band before measuring.
func (c *Controller) setTdivNearest(target float64) float64 {
	if len(c.tdivs) == 0 {
		return 0
	}
	best, bd := 0, 1e30
	for i, t := range c.tdivs {
		d := t - target
		if d < 0 {
			d = -d
		}
		if d < bd {
			bd, best = d, i
		}
	}
	c.mu.Lock()
	c.tdivIdx = best
	c.mu.Unlock()
	band, _ := c.eng.SetTdiv(c.tdivs[best])
	return band.CaptureIntervalNs() * 1e-9
}

// waitBandFrame waits for a published frame whose sample rate matches the band
// just installed (wantSampleS) — a band change from a SLOW timebase publishes
// its last old-band frame for a while, and measuring THAT aliases the signal
// (autoset landed on the wrong timebase / read a bogus frequency, found by the
// 1 MHz operator workflow from a slow start). Bounded so autoset never hangs;
// on timeout it returns true and the caller measures best-effort.
func (c *Controller) waitBandFrame(stop chan struct{}, wantSampleS float64) bool {
	if wantSampleS <= 0 || c.frameFn == nil {
		return c.waitFrame(stop) // no band info: fall back to the fixed settle
	}
	// The target sweep/fit bands are always per-sample (non-envelope); an
	// envelope/roll START keeps publishing ENVELOPE frames through the
	// transition, and measuring one of those aliases the signal. Require a
	// FRESH, NON-ENVELOPE frame whose sample rate matches the new band before
	// the caller measures (found by the 1 MHz workflow from a 10 ms/div start).
	for i := 0; i < 16; i++ { // ~16×settle ≈ 3.5 s worst case (roll→fast)
		if !c.waitFrame(stop) {
			return false // cancelled
		}
		matched := false
		c.frameFn(func(f *engine.Frame) {
			if f == nil || len(f.C1) == 0 || f.IsEnv {
				return // an envelope/roll frame from the old band aliases the measurement
			}
			// A genuine band change moves SampleS by integer decimation factors,
			// so a 1% window is unambiguous — and since the old band had a
			// different SampleS, a match here is necessarily a post-change frame.
			if f.SampleS > 0 && math.Abs(f.SampleS-wantSampleS) <= wantSampleS*0.01 {
				matched = true
			}
		})
		if matched {
			return true
		}
	}
	return true // best-effort: don't stall autoset on a stubborn transition
}

// detentForVpp picks the most sensitive V/div whose 8-division window still
// fits Vpp within ~6 divisions — so autoset never lands on a range that clips
// (a clipped trace measures a too-small Vpp, which would pick an even more
// sensitive range: a trap). Detents are ascending, so the first that fits wins.
func detentForVpp(vpp float64) int {
	for i := range analog.Detents {
		if vpp/analog.Detents[i].VdivV <= 6.0 {
			return i
		}
	}
	return len(analog.Detents) - 1
}

// nearestDetent returns the vertical detent index whose V/div is nearest the
// (electrical, pre-probe) target.
func nearestDetent(target float64) int {
	best, bd := analog.BootDetent, 1e30
	for i, d := range analog.Detents {
		diff := d.VdivV - target
		if diff < 0 {
			diff = -diff
		}
		if diff < bd {
			bd, best = diff, i
		}
	}
	return best
}
