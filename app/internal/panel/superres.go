// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"time"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// Device super-res (spec: reference-locked stack-and-crunch), the ACQ tab's
// SUPER-RES rows: Super-res On locks the current frame as the match
// reference and stacks matching frames (freeze a frame you like with Single
// first); Off cancels. Show switches the live trace and the stacked review,
// the Gate rows set the stacked span. Stacking runs on its own goroutine,
// OFF the render lock — the crunch may lag the engine and that is fine: the
// bits/stacks stop targets are acquisition-rate independent (the device
// converges to the same stack as the web).

// srArm locks the CURRENT frame as the match reference and starts stacking.
// It fails (the status says why) when no usable frame is on screen.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srArm() error {
	c.mu.Lock()
	c.srFocus = 0                 // watch
	c.srManLo, c.srManHi = -1, -1 // auto gate
	fpga := c.srStopMode >= srStopFPGA
	c.mu.Unlock()
	if fpga { // Stop on an FPGA mode: the FPGA stacks, not the software stack with its depth limit
		c.srFPGARun()
		return nil
	}
	// The live record on screen too deep for the software stack: refused at
	// once (stopped, the seed resumes RUN and stacks the live frames).
	if cols := c.srSampledCols(); cols > 0 && c.eng.Snapshot().Running {
		c.mu.Lock()
		k := c.srK
		c.mu.Unlock()
		if cols*k > srSoftBinsMax {
			why := fmt.Sprintf("too deep to stack (%dk): Stop on: FPGA rec, or a faster S/DIV", (cols+500)/1000)
			c.srSetStatus(why)
			return fmt.Errorf("super-res: %s", why)
		}
	}
	// the seed takes seconds off the panel goroutine: say so, not the last
	// session's "cancelled"
	c.srSetStatus("finding the reference...")
	if !c.srKick() {
		c.mu.Lock()
		why := c.srStatus
		c.mu.Unlock()
		return fmt.Errorf("super-res: %s", why)
	}
	c.pushLEDs()
	return nil
}

// srSampledCols is the samples of the sampled frame on screen, 0 for none
// (an envelope, or no frame).
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSampledCols() int {
	cols := 0
	if c.frameFn == nil { // no frame source (as every other reader allows)
		return 0
	}
	c.frameFn(func(f *engine.Frame) {
		if f != nil && !f.IsEnv && !f.PeakDetect {
			cols = min(max(f.Valid, 0), len(f.C1))
		}
	})
	return cols
}

// srKick asks for a fresh stack from the current frame with the current
// channel, grid and gate. The period search and the seed take seconds on the
// ARM: on the panel goroutine they would hold every key 2-4 s (each gate
// edge's knob detent 1-2 s), so the panel loop hands them to a seeder
// goroutine, where the latest request
// wins; without the loop (tests) the seed runs here. It reports false when a
// seed run here failed.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srKick() bool {
	c.mu.Lock()
	c.srSeedGen++
	gen := c.srSeedGen
	c.srSeeding = true
	c.srRunAt = c.runReq.Load()
	start := !c.srSeederOn
	if c.looping.Load() && start {
		c.srSeederOn = true
	}
	c.mu.Unlock()
	if !c.looping.Load() {
		ok := c.srSeedAndStartGen(gen)
		c.mu.Lock()
		if c.srSeedGen == gen {
			c.srSeeding = false
		}
		c.mu.Unlock()
		return ok
	}
	if start {
		go c.srSeeder()
	}
	return true
}

// srSeeder seeds stacks until no newer request waits; a failed seed says why.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSeeder() {
	for {
		c.mu.Lock()
		if !c.srSeeding { // Off came before this seed began: nothing to seed
			c.srSeederOn = false
			c.mu.Unlock()
			return
		}
		gen := c.srSeedGen
		c.mu.Unlock()
		ok := c.srSeedAndStartGen(gen)
		c.mu.Lock()
		if c.srSeedGen != gen && c.srSeeding {
			c.mu.Unlock()
			continue // a newer request (a gate edit, a new channel) came meanwhile
		}
		c.srSeeding, c.srSeederOn = false, false
		why, current := c.srStatus, c.srSeedGen == gen
		c.mu.Unlock()
		if !ok && current {
			c.toast("Super-res: "+why, 2400*time.Millisecond)
		}
		return
	}
}

// srAutoGate goes back to the automatic gate (the screen window).
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srAutoGate() error {
	c.mu.Lock()
	active := c.srActive
	c.srManLo, c.srManHi = -1, -1
	c.mu.Unlock()
	if active {
		c.srKick()
	}
	return nil
}

// srSetGate moves a gate edge to sample v and restacks on the new gate.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSetGate(end bool, v int) error {
	c.mu.Lock()
	if !c.srActive || c.srStack == nil {
		c.mu.Unlock()
		return fmt.Errorf("super-res is off")
	}
	if c.srManLo < 0 { // first edit: seed the manual gate from the auto gate
		c.srManLo, c.srManHi = c.srStack.GateLo, c.srStack.GateHi
	}
	n := c.srStack.N
	const minW = 8
	if end {
		c.srManHi = clampInt(v, minW, n)
		c.srManLo = min(c.srManLo, c.srManHi-minW)
	} else {
		c.srManLo = clampInt(v, 0, n-minW)
		c.srManHi = max(c.srManHi, c.srManLo+minW)
	}
	c.mu.Unlock()
	c.srKick() // rebuild the stack on the new gate
	return nil
}

// srRearm rebuilds the stack from the current frame with the current Channel/K —
// used when those config slots change mid-stack. Keeps the menu page + highlight.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srRearm() {
	c.mu.Lock()
	active := c.srActive
	c.srManLo, c.srManHi = -1, -1 // Channel/K change → auto gate for the new stack
	c.mu.Unlock()
	if active {
		c.srKick()
	}
}

// srPeriodSpanMax bounds the span searched for a period when arming.
const srPeriodSpanMax = 16384

// srSoftBinsMax bounds the software stack: 10 float64 per bin, ~21 MB.
const srSoftBinsMax = 1 << 18

// srFocusName returns the label for the current intensity-cycle focus.
// TRLC-LINKS: REQ-SDS-140
func srFocusName(f int) string {
	switch f {
	case 1:
		return "gate start"
	case 2:
		return "gate end"
	case 3:
		return "review"
	default:
		return "watch"
	}
}

// srSeedAndStart grabs the current frame, builds a fresh stack sized for the
// current srK/srCh, seeds it as the locked reference, resumes RUN and (re)starts
// the stacker goroutine. Any existing stacker is stopped first (its old Stack is
// a captured local, so no race with the new one). Returns false if there is no
// usable frame or the reference is unusable.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSeedAndStart() bool {
	c.mu.Lock()
	gen := c.srSeedGen
	c.mu.Unlock()
	return c.srSeedAndStartGen(gen)
}

// srSeedAndStartGen is srSeedAndStart for request gen: a newer request or Off
// meanwhile discards it (its status, and the stack it built).
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSeedAndStartGen(gen uint64) bool {
	fail := func(why string) bool {
		c.mu.Lock()
		if c.srSeedGen == gen {
			c.srStatus = why
		}
		c.mu.Unlock()
		return false
	}
	if c.frameFn == nil {
		return fail("no frame source")
	}
	c.srWaitRaw()
	var c1, c2 []uint8
	var edgeX, sampleS, seedTdiv float64
	var cols, winCols int
	var seq uint64
	grab := func() {
		c1, c2 = nil, nil
		c.frameFn(func(f *engine.Frame) {
			if f == nil || f.IsEnv || f.PeakDetect {
				return
			}
			v := f.Valid
			if v < 1 || v > len(f.C1) {
				v = len(f.C1)
			}
			if v < 8 {
				return
			}
			cols, edgeX, sampleS, winCols, seedTdiv, seq = v, f.EdgeX, f.SampleS, f.WinCols, f.TdivS, f.Seq
			c1 = append([]uint8(nil), f.C1[:v]...)
			if len(f.C2) >= v {
				c2 = append([]uint8(nil), f.C2[:v]...)
			} else {
				c2 = c1
			}
		})
	}
	grab()
	c.mu.Lock()
	k, ch, mlo, mhi := c.srK, c.srCh, c.srManLo, c.srManHi
	c.mu.Unlock()
	// A stopped scope shows its full record (a million samples on the SRAM
	// band), which is never shallow enough to stack in software, so SINGLE
	// then UTILITY would always fail. The stack is fed live raw frames anyway:
	// resume and seed from the first one, as a running arm does.
	if c1 != nil && cols*k > srSoftBinsMax && !c.eng.Snapshot().Running && c.srRunUntouched() {
		stale := seq
		c.eng.SetRunning(true)
		for i := 0; i < 60; i++ {
			c.srLeaseRaw()
			time.Sleep(50 * time.Millisecond)
			fresh := false
			c.frameFn(func(f *engine.Frame) { fresh = f != nil && f.Seq != stale && !f.IsEnv && !f.PeakDetect })
			if fresh {
				break
			}
		}
		grab()
		if c1 == nil || cols*k > srSoftBinsMax {
			c.eng.SetRunning(false) // leave it stopped, as the user had it
		}
	}
	if c1 == nil {
		return fail("sampled frame required - Stop on: FPGA rec, or a faster timebase")
	}
	// The software stack holds ten float64 accumulators per fine bin; a deep
	// window (156k samples at 0.5 ms/div x32 = ~400 MB) cannot fit the device.
	if cols*k > srSoftBinsMax {
		// short enough for the ACQ tab's status line with its remedy (77 characters)
		return fail(fmt.Sprintf("too deep to stack (%dk): Stop on: FPGA rec, or a faster S/DIV", (cols+500)/1000))
	}
	// Default gate = the ON-SCREEN window (super-res exactly what's displayed —
	// winCols samples centred on the trigger edge), so you never stack a random
	// feature elsewhere in the deep record. A manual gate-edit (ADJUST markers,
	// srManLo≥0) overrides this with the user's exact span.
	if mlo < 0 && winCols > 0 && winCols < cols && edgeX >= 0 {
		// Anchor exactly like the display window(): the edge sits at the trigger-
		// position fraction of the screen (not always centred) — the review then
		// reproduces the frozen view phase-for-phase.
		mlo = int(edgeX - float64(winCols)*c.trigPos())
		mhi = mlo + winCols
		if mlo < 0 {
			mlo = 0
		}
		if mhi > cols {
			mhi = cols
		}
	}
	// The REVIEW must show exactly the selected span (the view is not changed).
	// When the span holds multiple periods of the same wave, CHEAT for speed:
	// stack ONE period (small grid, every cycle is a hit → converges fast) and
	// let the renderer TILE the stacked period back across the span.
	stackLo, stackHi, period := mlo, mhi, 0
	// Period search is roughly quadratic in the span and runs on the panel
	// goroutine; on deep windows (e.g. 156k samples at 0.5 ms/div) it froze
	// every key for minutes. Tiling is only a speed-up, so skip it there.
	if mlo >= 0 && mhi-mlo <= srPeriodSpanMax {
		alignSig := c1
		if ch == 1 {
			alignSig = c2
		}
		if p := superres.DetectPeriodU8(alignSig, mlo, mhi); p >= 16 && p < mhi-mlo {
			period, stackHi = p, mlo+p
		}
	}
	st := superres.New(cols, k)
	st.Align = ch
	st.SampleS = sampleS
	if !st.SeedRefGate(c1, c2, edgeX, stackLo, stackHi) { // lo<0 → auto gate; else manual
		return fail("ref unusable (flat/clipped) - freeze a cleaner frame")
	}
	stop := make(chan struct{})
	c.mu.Lock()
	if c.srSeedGen != gen { // a newer request or Off came while this one seeded
		c.mu.Unlock()
		return false
	}
	if c.srStop != nil { // stop the previous stacker before swapping the Stack
		close(c.srStop)
	}
	c.srStack, c.srActive, c.srStop = st, true, stop // srFocus preserved (gate-edit)
	c.srT0, c.srStatus, c.srMean, c.srMean2, c.srBits, c.srResetReq = time.Now(), "stacking...", nil, nil, 0, false
	c.srFrames, c.srRejected = st.Hits, 0               // fresh counts (not stale from a prior arm)
	c.srWinLo, c.srWinHi, c.srPeriod = mlo, mhi, period // review span + tile period
	// resume so matching frames flow in; a Run/Stop/Single pressed while the
	// seed ran off the panel goroutine stands (the seed's end must not put a
	// Stop pressed meanwhile back to Run)
	resume := c.runReq.Load() == c.srRunAt
	if resume {
		c.running, c.single = true, false
	}
	c.mu.Unlock()
	if resume {
		c.eng.SetRunning(true)
	}
	go c.srLoop(stop, st, seedTdiv) // pass the Stack in — the loop is its sole owner, no shared read
	return true
}

// srRunUntouched reports that no Run, Stop or Single came since the seed was
// asked for: only then does a seed change Run/Stop for its frames.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srRunUntouched() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runReq.Load() == c.srRunAt
}

// srCancel stops the stacker and leaves the mode (Super-res Off). The stack
// is dropped; the last review the user wanted is already on screen if they
// froze it.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srCancel(why string) {
	c.mu.Lock()
	if c.srFPGACancel != nil {
		c.srFPGACancel()
		c.srFPGACancel = nil
		c.srFPGAGen++
	}
	if c.srStop != nil {
		close(c.srStop)
		c.srStop = nil
	}
	c.srSeedGen++ // a seed in flight is discarded
	c.srActive, c.srFocus, c.srSeeding = false, 0, false
	if why != "" {
		c.srStatus = why
	}
	c.mu.Unlock()
	c.pushLEDs()
}

// srReqReset (Reset softkey) asks the stacker to start the accumulation over,
// keeping the same locked reference. srLoop performs it so only that goroutine
// ever mutates the Stack.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srReqReset() {
	c.mu.Lock()
	if c.srActive {
		c.srResetReq = true
	}
	c.mu.Unlock()
}

// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srSetStatus(s string) {
	c.mu.Lock()
	c.srStatus = s
	c.mu.Unlock()
}

// srReachReview crunches the final mean and switches to the review view. Called
// from srLoop when a stop target is hit or the geometry changes.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srReachReview(st *superres.Stack, status string) {
	c.mu.Lock()
	current := c.srActive && !c.srFPGABusy && c.srStack == st
	c.mu.Unlock()
	if !current {
		return
	}
	full := st.Result(false, 1)
	mean, mean2 := full.Mean, full.Mean2
	c.mu.Lock()
	if !c.srActive || c.srFPGABusy || c.srStack != st {
		c.mu.Unlock()
		return
	}
	c.srMean, c.srMean2 = mean, mean2
	c.srBits, c.srFocus, c.srStatus = full.BitsGained, 3, status // 3=review
	c.srFrames, c.srRejected = st.Hits, st.Rejected
	c.mu.Unlock()
}

// SuperresStatus is the read-only device super-res snapshot for /api/status.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) SuperresStatus() (active, review bool, bits float64, frames, rejected int, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.srActive, c.srFocus == 3, c.srBits, c.srFrames, c.srRejected, c.srStatus
}

// srLoop feeds the stacker: pull the latest raw frame (deduped by Seq), copy it
// out under the render lock, feed OFF-lock, refresh status/bits, check the stop
// target, and keep the review mean fresh when review is on. Only THIS goroutine
// touches srStack, so there is no data race with the renderer (which reads the
// guarded srMean/srStatus/srBits snapshot).
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srLoop(stop chan struct{}, st *superres.Stack, seedTdiv float64) {
	var lastSeq uint64
	var lastMean time.Time
	reached := false // stop target hit (or geometry changed): stack is final, idle
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var buf1, buf2 []uint8 // the frame copies' storage, reused (srFrameCopy)
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		// Reset request (Reset softkey) — done here so only this goroutine touches
		// the Stack. Clears the accumulation, keeps the locked reference.
		c.mu.Lock()
		if !c.srActive || c.srFPGABusy || c.srStack != st {
			c.mu.Unlock()
			return
		}
		if c.srResetReq {
			c.srResetReq = false
			st.ResetKeepRef()
			reached, lastSeq = false, 0
			c.srT0, c.srMean, c.srMean2, c.srBits = time.Now(), nil, nil, 0
			if c.srFocus == 3 { // leave review on reset; keep gate-edit foci
				c.srFocus = 0
			}
			c.srFrames, c.srRejected = st.Hits, 0
			c.srStatus = "reset - stacking..."
		}
		idle := reached
		c.mu.Unlock()
		if idle {
			continue // stacked result is final; wait for Reset or UTILITY-cancel
		}
		// Full-rate raw live frames only while stacking: renewed for a final
		// stack, the lease would read 500 k words a frame at 200 us/div and
		// hold the live view at 1 fps for as long as super-res stays armed.
		c.srLeaseRaw()
		var c1, c2 []uint8
		var edgeX float64
		var seq uint64
		var cols int
		var sampleS, tdiv float64
		var envelope bool
		c.frameFn(func(f *engine.Frame) {
			if f != nil && (f.IsEnv || f.PeakDetect) {
				envelope = true
				return
			}
			if f == nil || f.Seq == lastSeq {
				return
			}
			v := f.Valid
			if v < 1 || v > len(f.C1) {
				v = len(f.C1)
			}
			if v < 8 {
				return
			}
			seq, cols, edgeX, sampleS, tdiv = f.Seq, v, f.EdgeX, f.SampleS, f.TdivS
			c1, c2 = srFrameCopy(f, v, buf1, buf2)
			buf1 = c1
			if len(f.C2) >= v {
				buf2 = c2
			}
		})
		if envelope {
			c.srReachReview(st, "sampled frame required - stack kept")
			reached = true
			continue
		}
		if c1 == nil {
			continue // no new frame yet
		}
		lastSeq = seq
		// A real band/tdiv change (SampleS) rescales the whole record — freeze the
		// stack for review. Per-frame Valid jitter (drain timing routinely varies
		// the drained count by a chunk) is NOT a scale change: a frame too short to
		// cover the grid is simply skipped, no penalty, no cancel.
		if st.SampleS != 0 && sampleS != 0 && sampleS != st.SampleS {
			c.srReachReview(st, "scale changed - stack kept")
			reached = true
			continue
		}
		// Raw SRAM frames keep their 2 ns pitch at every timebase, so a
		// timebase change shows only as TdivS (else the stack keeps feeding
		// 500 k sample frames at 200 us/div).
		if seedTdiv > 0 && tdiv > 0 && math.Abs(tdiv-seedTdiv) > seedTdiv*1e-6 {
			c.srReachReview(st, "timebase changed - stack kept")
			reached = true
			continue
		}
		if cols < st.N {
			continue // short frame: can't fill the n·K grid this time, skip it
		}
		st.Feed(c1, c2, edgeX)
		res := st.Result(true, 0) // stats-only: cheap, for status + stop
		c.mu.Lock()
		if !c.srActive || c.srFPGABusy || c.srStack != st {
			c.mu.Unlock()
			return
		}
		mode, val, t0, review := c.srStopMode, c.srStopVal, c.srT0, c.srFocus == 3
		// Hits (occurrences) is the meaningful stack count — one frame contributes
		// many on a repetitive signal — so status + the "stacks" target key off it.
		c.srBits, c.srFrames, c.srRejected = res.BitsGained, st.Hits, st.Rejected
		if st.Hits <= 1 && st.Rejected >= 12 {
			// Honest no-repeat feedback: the gated content does not recur as a
			// whole — tell the user to move/narrow the gate, don't spin silently.
			c.srStatus = fmt.Sprintf("gate not repeating (%d rej) - adjust gate", st.Rejected)
		} else {
			c.srStatus = fmt.Sprintf("%d stk / %df / %d rej  +%.1fb",
				st.Hits, st.Frames, st.Rejected, res.BitsGained)
		}
		c.mu.Unlock()
		if review && time.Since(lastMean) > 400*time.Millisecond {
			full := st.Result(false, 1)
			mean, mean2 := full.Mean, full.Mean2 // raw mean, matching the browser default
			c.mu.Lock()
			if !c.srActive || c.srFPGABusy || c.srStack != st {
				c.mu.Unlock()
				return
			}
			c.srMean, c.srMean2 = mean, mean2
			c.mu.Unlock()
			lastMean = time.Now()
		}
		done := false
		switch mode {
		case 0: // bits (acquisition-rate independent)
			done = val > 0 && res.SigmaStack > 0 && res.BitsGained >= val
		case 1: // stacks = occurrences (exact)
			done = val > 0 && float64(st.Hits) >= val
		case 2: // time (wall clock)
			done = val > 0 && time.Since(t0).Seconds() >= val
		}
		if done {
			c.srReachReview(st, fmt.Sprintf("done: %d stacked", st.Hits))
			reached = true
		}
	}
}

// SuperresView is the render snapshot (safe from the render goroutine).
// TRLC-LINKS: REQ-SDS-140
type SuperresView struct {
	Active         bool
	Focus          int // 0=watch, 1=gate-start, 2=gate-end, 3=review
	Status         string
	Bits           float64
	Mean           []float32 // align-channel crunched trace (review only, Focus==3); nil otherwise
	Mean2          []float32 // the OTHER channel's crunched trace (stacked X-Y / dual FFT)
	Align          int       // which physical channel Mean is (0=C1,1=C2); Mean2 is the other
	K              int
	SampleS        float64
	GateLo, GateHi int // stack gate on the frame (samples) — the live-view overlay
	N              int // frame width (samples)
	WinLo, WinHi   int // the selected span the review renders (= the frozen view)
	Period         int // >0: the stack is one period; the review tiles it over the span
}

// srFrameCopy copies a frame's first v samples of both channels into b1
// and b2, the loop's buffers from frame to frame (Feed reads them, keeps
// none): a raw frame is half a million samples a channel, a fresh copy each
// frame. A short C2 shares C1's copy.
// TRLC-LINKS: REQ-SDS-140
func srFrameCopy(f *engine.Frame, v int, b1, b2 []uint8) (c1, c2 []uint8) {
	c1 = append(b1[:0], f.C1[:v]...)
	if len(f.C2) < v {
		return c1, c1
	}
	return c1, append(b2[:0], f.C2[:v]...)
}

// SuperresView returns the current super-res state for the LCD overlay/review.
// GateLo/GateHi/N/K/SampleS come off the Stack; they are set once at seed and not
// mutated by Feed, so reading them from the render goroutine is race-free.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) SuperresView() SuperresView {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := SuperresView{Active: c.srActive, Focus: c.srFocus, Status: c.srStatus, Bits: c.srBits, K: c.srK}
	if c.srStack != nil {
		v.GateLo, v.GateHi, v.N = c.srStack.GateLo, c.srStack.GateHi, c.srStack.N
		v.SampleS, v.K, v.Align = c.srStack.SampleS, c.srStack.K, c.srStack.Align
		v.WinLo, v.WinHi, v.Period = c.srWinLo, c.srWinHi, c.srPeriod
		if v.WinLo < 0 { // auto gate (no on-screen window): review the gate itself
			v.WinLo, v.WinHi = v.GateLo, v.GateHi
		}
	}
	if c.srFocus == 3 {
		v.Mean, v.Mean2 = c.srMean, c.srMean2
	}
	return v
}

// srLeaseRaw asks the engine for full-rate raw live frames for a few seconds:
// stacking needs every sample, not the live display envelope.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srLeaseRaw() {
	c.leaseRaw(rawForSuperres, 3*time.Second)
}

// srWaitRaw leases raw frames and waits (bounded) until a raw one is
// published, so the stack seeds from samples rather than an envelope.
// TRLC-LINKS: REQ-SDS-140
func (c *Controller) srWaitRaw() {
	c.srLeaseRaw()
	for i := 0; i < 60; i++ {
		raw := false
		c.frameFn(func(f *engine.Frame) { raw = f != nil && !f.IsEnv && !f.PeakDetect })
		if raw {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
