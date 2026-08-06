package panel

import (
	"fmt"
	"time"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/superres"
)

// The finalize rbf (HEAD 41ebc84) hardwires the COMBINE grid to 64*4 = 256 bins
// (3072 = combine.DrainWords(64,4) words). Device mode requests exactly this, NOT srK.
const srDevGridL, srDevK = 64, 4

// Device super-res (spec: reference-locked stack-and-crunch). UTILITY toggles it
// like SINGLE: press to arm (lock the current frozen frame as the match reference
// and stack matching frames), press again to cancel. The ADJUST/intensity knob-
// push toggles the stacked-trace review view. Stacking runs on its own goroutine,
// OFF the render lock — the crunch may lag the engine and that is fine: the
// bits/stacks stop targets are acquisition-rate independent (the device converges
// to the same stack as the web).

// srToggle is the UTILITY handler: arm ⇄ cancel.
func (c *Controller) srToggle() {
	c.mu.Lock()
	active := c.srActive
	c.mu.Unlock()
	if active {
		c.srCancel("cancelled")
		return
	}
	c.srArm()
}

// srArm locks the CURRENT frame as the match reference and starts stacking, then
// maps the softkeys to the super-res page. (Freeze a frame you like with SINGLE
// first.) UTILITY handler for the arm direction.
func (c *Controller) srArm() {
	c.mu.Lock()
	c.srFocus = 0                 // watch
	c.srManLo, c.srManHi = -1, -1 // auto gate
	c.mu.Unlock()
	if !c.srSeedAndStart() {
		return
	}
	c.openMenu(pgSuperres) // map the softkeys to the super-res page while active
	c.pushLEDs()
}

// srRearm rebuilds the stack from the current frame with the current Channel/K —
// used when those config slots change mid-stack. Keeps the menu page + highlight.
func (c *Controller) srRearm() {
	c.mu.Lock()
	active := c.srActive
	c.srManLo, c.srManHi = -1, -1 // Channel/K change → auto gate for the new stack
	c.mu.Unlock()
	if active {
		c.srSeedAndStart()
	}
}

// srGateAdjust moves the super-res gate edge under the ADJUST knob while a gate
// edge is focused (intensity button selects start/end). Re-seeds the stack with
// the new manual gate. Returns true if it consumed the knob step.
func (c *Controller) srGateAdjust(delta int) bool {
	c.mu.Lock()
	if !c.srActive || (c.srFocus != 1 && c.srFocus != 2) || c.srStack == nil {
		c.mu.Unlock()
		return false
	}
	if c.srManLo < 0 { // first edit: seed the manual gate from the auto gate
		c.srManLo, c.srManHi = c.srStack.GateLo, c.srStack.GateHi
	}
	n := c.srStack.N
	step := delta * 4 // samples per accel-step
	const minW = 8
	if c.srFocus == 2 {
		c.srManHi += step
	} else {
		c.srManLo += step
	}
	if c.srManLo < 0 {
		c.srManLo = 0
	}
	if c.srManHi > n {
		c.srManHi = n
	}
	if c.srManHi-c.srManLo < minW { // keep a minimum width, pushing the moved edge
		if c.srFocus == 2 {
			c.srManHi = c.srManLo + minW
		} else {
			c.srManLo = c.srManHi - minW
		}
	}
	c.mu.Unlock()
	c.srSeedAndStart() // rebuild the stack on the new gate
	return true
}

// srFocusName returns the label for the current intensity-cycle focus.
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
func (c *Controller) srSeedAndStart() bool {
	if c.frameFn == nil {
		c.srSetStatus("no frame source")
		return false
	}
	var c1, c2 []uint8
	var edgeX, sampleS float64
	var cols, winCols int
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv {
			return
		}
		v := f.Valid
		if v < 1 || v > len(f.C1) {
			v = len(f.C1)
		}
		if v < 8 {
			return
		}
		cols, edgeX, sampleS, winCols = v, f.EdgeX, f.SampleS, f.WinCols
		c1 = append([]uint8(nil), f.C1[:v]...)
		if len(f.C2) >= v {
			c2 = append([]uint8(nil), f.C2[:v]...)
		} else {
			c2 = c1
		}
	})
	if c1 == nil {
		c.srSetStatus("no frame - can't arm")
		return false
	}
	c.mu.Lock()
	k, ch, mlo, mhi := c.srK, c.srCh, c.srManLo, c.srManHi
	device := c.srDevice
	c.mu.Unlock()
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
	if mlo >= 0 {
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
	if device {
		// The finalize rbf fixes the fabric grid at srDevGridL*srDevK bins; device
		// mode requests exactly that, NOT the host srK. Keep the reference seed (it
		// fixes Align + SampleS + gives a usable frame gate) on the device geometry.
		k = srDevK
		st = superres.New(cols, k)
		st.Align, st.SampleS = ch, sampleS
	}
	if !st.SeedRefGate(c1, c2, edgeX, stackLo, stackHi) { // lo<0 → auto gate; else manual
		c.srSetStatus("ref unusable (flat/clipped) - freeze a cleaner frame")
		return false
	}
	stop := make(chan struct{})
	c.mu.Lock()
	if c.srStop != nil { // stop the previous stacker before swapping the Stack
		close(c.srStop)
	}
	c.srStack, c.srActive, c.srStop = st, true, stop // srFocus preserved (gate-edit)
	c.srT0, c.srStatus, c.srMean, c.srMean2, c.srBits, c.srResetReq = time.Now(), "stacking...", nil, nil, 0, false
	c.srFrames, c.srRejected = st.Hits, 0 // fresh counts (not stale from a prior arm)
	if device {
		c.srWinLo, c.srWinHi, c.srPeriod = 0, srDevGridL, 0 // render the crunched grid directly
	} else {
		c.srWinLo, c.srWinHi, c.srPeriod = mlo, mhi, period // review span + tile period
	}
	c.running, c.single = true, false
	c.mu.Unlock()
	c.eng.SetRunning(!device)     // host: resume normal frames; device: STOP (combine drains own the FSM)
	go c.srLoop(stop, st, device) // pass the Stack in — the loop is its sole owner, no shared read
	return true
}

// srCancel stops the stacker and leaves the mode (UTILITY re-press). The stack is
// dropped; the last review the user wanted is already on screen if they froze it.
func (c *Controller) srCancel(why string) {
	c.mu.Lock()
	if c.srStop != nil {
		close(c.srStop)
		c.srStop = nil
	}
	c.srActive, c.srFocus = false, 0
	if why != "" {
		c.srStatus = why
	}
	closeMenu := c.menuPage == pgSuperres
	c.mu.Unlock()
	if closeMenu {
		c.openMenu(pgNone)
	}
	c.pushLEDs()
}

// srCancelForAcq cancels DEVICE super-res when the user takes manual acquisition
// control (RUN or SINGLE). Device super-res STOPS the engine FSM (srSeedAndStart
// does SetRunning(false)) and lets the CombineDrain owner-goroutine drive the
// arm→dwell→halt→drain→restore recipe; a concurrent RUN/SINGLE would re-drive the
// SAME core (normal capture / single-shot) and the two paths would contend on the
// FSM + GPMC. Cancelling drops srActive + stops srLoop before the acquisition call
// proceeds, so the two owners can never run at once. HOST-drizzle super-res only
// reads finished frames (frameFn) and never owns the FSM, so it is left untouched —
// its behaviour under RUN/SINGLE is unchanged. Returns true iff it cancelled.
func (c *Controller) srCancelForAcq() bool {
	c.mu.Lock()
	contend := c.srActive && c.srDevice
	c.mu.Unlock()
	if !contend {
		return false
	}
	c.srCancel("cancelled - acquisition")
	return true
}

// srFocusCycle (ADJUST/intensity push) advances the super-res focus:
// watch → gate-start → gate-end → review → watch. In the gate-edit foci the
// ADJUST knob moves that edge; review shows the stacked trace.
func (c *Controller) srFocusCycle() {
	c.mu.Lock()
	if c.srActive {
		c.srFocus = (c.srFocus + 1) % 4
	}
	c.mu.Unlock()
}

// srReqReset (Reset softkey) asks the stacker to start the accumulation over,
// keeping the same locked reference. srLoop performs it so only that goroutine
// ever mutates the Stack.
func (c *Controller) srReqReset() {
	c.mu.Lock()
	if c.srActive {
		c.srResetReq = true
	}
	c.mu.Unlock()
}

func (c *Controller) srSetStatus(s string) {
	c.mu.Lock()
	c.srStatus = s
	c.mu.Unlock()
}

// srCompMeans de-embeds the measured analog falloff from the crunched review
// means — the SAME curve and gating the web applies when it builds its review
// frame (app_superres.js srMakeViewFrame + superres_comp.js): the auto target
// sized by THIS stack's measured bit budget at the web's default spend (0.8),
// applied per channel on the fine grid dt = SampleS/K, so every review view
// (Y-T/FFT/X-Y) inherits the recovered bandwidth. Compensate itself preserves
// the −1 gap sentinels and returns the input untouched when the gates say no
// (dt ≤ 0, < 8 bins, all-gap) — identical to the JS. Runs on the stacker
// goroutine, off the render lock.
func srCompMeans(st *superres.Stack, res superres.Result) (mean, mean2 []float32) {
	mean, mean2 = res.Mean, res.Mean2
	k := st.K
	if k < 1 {
		k = 1
	}
	dt := st.SampleS / float64(k)
	if !(dt > 0) { // web gate: comp applies only with a real fine-grid dt
		return
	}
	rawNyq := 250e6 // web fallback (dt > 0 ⇒ SampleS > 0, so normally taken)
	if st.SampleS > 0 {
		rawNyq = 1 / (2 * st.SampleS)
	}
	o := superres.CompAuto(res.BitsGained, rawNyq, 0.8)
	if mean != nil {
		mean = superres.Compensate(mean, dt, o)
	}
	if mean2 != nil {
		mean2 = superres.Compensate(mean2, dt, o)
	}
	return
}

// srReachReview crunches the final mean and switches to the review view. Called
// from srLoop when a stop target is hit or the geometry changes.
func (c *Controller) srReachReview(st *superres.Stack, status string) {
	full := st.Result(false, 1)
	mean, mean2 := srCompMeans(st, full)
	c.mu.Lock()
	c.srMean, c.srMean2 = mean, mean2
	c.srBits, c.srFocus, c.srStatus = full.BitsGained, 3, status // 3=review
	c.srFrames, c.srRejected = st.Hits, st.Rejected
	c.mu.Unlock()
}

// SuperresStatus is the read-only device super-res snapshot for /api/status.
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
func (c *Controller) srLoop(stop chan struct{}, st *superres.Stack, device bool) {
	var lastSeq uint64
	var lastMean time.Time
	reached := false // stop target hit (or geometry changed): stack is final, idle
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		// Reset request (Reset softkey) — done here so only this goroutine touches
		// the Stack. Clears the accumulation, keeps the locked reference.
		c.mu.Lock()
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
		if device {
			c.srDeviceTick(st, &reached)
			continue
		}
		// ---- host drizzle path below: UNCHANGED (Feed) ----
		var c1, c2 []uint8
		var edgeX float64
		var seq uint64
		var cols int
		var sampleS float64
		c.frameFn(func(f *engine.Frame) {
			if f == nil || f.IsEnv || f.Seq == lastSeq {
				return
			}
			v := f.Valid
			if v < 1 || v > len(f.C1) {
				v = len(f.C1)
			}
			if v < 8 {
				return
			}
			seq, cols, edgeX, sampleS = f.Seq, v, f.EdgeX, f.SampleS
			c1 = append([]uint8(nil), f.C1[:v]...)
			if len(f.C2) >= v {
				c2 = append([]uint8(nil), f.C2[:v]...)
			} else {
				c2 = c1
			}
		})
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
		if cols < st.N {
			continue // short frame: can't fill the n·K grid this time, skip it
		}
		st.Feed(c1, c2, edgeX)
		res := st.Result(true, 0) // stats-only: cheap, for status + stop
		c.mu.Lock()
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
			mean, mean2 := srCompMeans(st, full) // falloff comp, exactly like the web review
			c.mu.Lock()
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

// srDeviceTick pulls one in-fabric COMBINE grid, injects it, and runs the SAME
// status/stop/review logic as the host Feed path (keyed off st.Hits / res.BitsGained).
// It is the device analogue of one srLoop host iteration. Only this goroutine touches st.
func (c *Controller) srDeviceTick(st *superres.Stack, reached *bool) {
	out, ok := c.eng.CombineDrain(engine.CombineReq{GridL: srDevGridL, K: srDevK, DwellMs: 20})
	if !ok {
		return // timeout / overflow / queue-full → skip this tick, never crunch a partial grid
	}
	// A real band/tdiv change rescales the record — freeze the stack for review, like host.
	if st.SampleS != 0 && out.SampleS != 0 && out.SampleS != st.SampleS {
		c.srReachReview(st, "scale changed - stack kept")
		*reached = true
		return
	}
	c.mu.Lock()
	align := c.srCh
	c.mu.Unlock()
	grid, err := combine.Unpack(out.Words, srDevGridL, srDevK, align, false, out.SampleS, 0, out.Frames)
	if err != nil {
		return
	}
	grid.Hits = combineHits(grid) // reps integrated this dwell (max align bin count)
	if err := st.InjectBins(grid); err != nil {
		return
	}
	res := st.Result(true, 0) // stats-only for status + stop
	c.mu.Lock()
	mode, val, t0, review := c.srStopMode, c.srStopVal, c.srT0, c.srFocus == 3
	c.srBits, c.srFrames, c.srRejected = res.BitsGained, st.Hits, 0
	c.srStatus = fmt.Sprintf("%d reps / %d drn  +%.1fb", st.Hits, out.Frames, res.BitsGained)
	c.mu.Unlock()
	if review {
		full := st.Result(false, 1)
		mean, mean2 := srCompMeans(st, full) // falloff comp, exactly like the web review
		c.mu.Lock()
		c.srMean, c.srMean2 = mean, mean2
		c.mu.Unlock()
	}
	done := false
	switch mode {
	case 0: // bits — mean-only ships BitsGained=0, so this never completes (stated ceiling)
		done = val > 0 && res.SigmaStack > 0 && res.BitsGained >= val
	case 1: // stacks = reps (st.Hits)
		done = val > 0 && float64(st.Hits) >= val
	case 2: // time (wall clock)
		done = val > 0 && time.Since(t0).Seconds() >= val
	}
	if done {
		c.srReachReview(st, fmt.Sprintf("done: %d reps", st.Hits))
		*reached = true
	}
}

// combineHits reports the accumulation depth of a drained grid = the max align bin
// count (reps integrated this dwell; the fabric self-caps at 255/bin).
func combineHits(g superres.BinGrid) int {
	m := uint64(0)
	for _, cnt := range g.ACnt {
		if cnt > m {
			m = cnt
		}
	}
	return int(m)
}

// srSetDevice flips the host⇄device super-res source (runs on the panel goroutine).
// If super-res is active it re-seeds so the switch takes effect immediately.
func (c *Controller) srSetDevice(on bool) {
	c.mu.Lock()
	if c.srDevice == on {
		c.mu.Unlock()
		return
	}
	c.srDevice = on
	active := c.srActive
	c.mu.Unlock()
	if active {
		c.srSeedAndStart() // rebuild the stack for the new source (stops the old srLoop)
	}
}

// SetSuperresDevice / SuperresSetArmed are the REMOTE (web/SCPI) control surface. They
// marshal onto the panel goroutine via c.inject (like InjectButton) so they never race
// the panel's own menu ops. Return false if the inject queue is full.
func (c *Controller) SetSuperresDevice(on bool) bool {
	select {
	case c.inject <- func() { c.srSetDevice(on) }:
		return true
	default:
		return false
	}
}

// SuperresSetArmed arms or cancels device super-res remotely (idempotent).
func (c *Controller) SuperresSetArmed(on bool) bool {
	select {
	case c.inject <- func() {
		c.mu.Lock()
		active := c.srActive
		c.mu.Unlock()
		if on && !active {
			c.srArm()
		} else if !on && active {
			c.srCancel("remote")
		}
	}:
		return true
	default:
		return false
	}
}

// SuperresView is the render snapshot (safe from the render goroutine).
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

// SuperresView returns the current super-res state for the LCD overlay/review.
// GateLo/GateHi/N/K/SampleS come off the Stack; they are set once at seed and not
// mutated by Feed, so reading them from the render goroutine is race-free.
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
