package engine

import (
	"time"

	"open-sds/app/internal/bus"
)

// Slow-envelope and roll band paths (spec 04). Both display a per-column
// (min,max) band whose every value is a real ADC sample; solidity comes from
// PHASE SCATTER — the per-sample interval is a good fraction of the signal
// period, accumulated over a ring of frames/snapshots.

const (
	selRollC1 = 0x41 // roll FIFO C1: each ioctl read pops one live sample
	selRollC2 = 0x59 // roll FIFO C2 (same mux-selected source, byte-replicated)
	opLatch   = 0x00cb
)

// interrupted reports whether the owner must bail out of a long capture loop
// NOW: shutdown, STOP, or a staged band/mode/ETS change (spec 09 §3.1 —
// otherwise a knob step waits out a multi-hundred-ms frame).
func (e *Engine) interrupted() bool {
	if e.stopReq.Load() || !e.running.Load() {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pendSet || e.norm != e.lastNorm || e.etsWant != e.etsOn
}

// clearCrossFrame drops every cross-frame accumulation on a band change so
// the new band's output is not polluted (spec 04 §4.2, spec 09 §2.2:
// uniformity, envelope, roll, ETS phase and average rings all clear).
func (e *Engine) clearCrossFrame() {
	e.flatHeld = 0
	e.envRingCnt, e.envRingPos = 0, 0
	e.rollSnaps1, e.rollSnaps2 = e.rollSnaps1[:0], e.rollSnaps2[:0]
	e.rollPos = 0
	e.etsReset()
	e.uni.reset()
	e.avgKey.width = 0 // forces the average ring to reset on next use
	e.resetDeadRuns()
}

// transition applies a staged band/mode/ETS change at the frame boundary.
func (e *Engine) transition(norm, etsWant bool) {
	newKind := e.band.Kind()
	// Leaving envelope/roll for a real-time band: drop the latched free-run
	// state FIRST, or the next armed arm mis-inits (spec 04 §4.2 step 1).
	if (e.prevKind == KindEnvelope || e.prevKind == KindRoll) &&
		newKind != KindEnvelope && newKind != KindRoll {
		e.w(selArm, opResetHead)
		e.w(selArm, opResetHead)
	}
	e.rollArmed = false
	e.etsOn = etsWant
	e.lastNorm = norm
	e.clearCrossFrame()
	e.bringUp()
	if newKind == KindRoll {
		e.rollBringUp()
	}
	e.prevKind = newKind
}

// ---- slow envelope (5–50 ms/div) ----

// envFrame runs one envelope frame: arm → modest fill wait (no status gate;
// a partial fill is fine, the ring accumulates scatter) → halt → drain the
// window → re-arm → ring push + 800-column min/max reduction → publish.
// Publishes EVERY frame in both AUTO and NORM (phase-independent band).
func (e *Engine) envFrame(norm bool) {
	start := e.clk.Now()
	e.armEngine()

	target := e.band.EnvFillTarget()
	// Give the fill time to actually REACH the (margin-inclusive) target: the
	// capture takes target·CaptureInterval, so grow the deadline past the
	// responsiveness floor when the centring margin makes the capture longer (the
	// slower envelope bands). Fast bands stay snappy; 20 ms/div gets to complete
	// its margin instead of dead-tailing it.
	capNs := float64(target) * e.band.CaptureIntervalNs()
	deadline := start.Add(envFillFloorMs * time.Millisecond)
	if grow := start.Add(time.Duration(capNs * envFillSlack)); grow.After(deadline) {
		deadline = grow
	}
	fill0 := e.r(selFill) & fillMask
	fillMoved := false
	for i := 0; ; i++ {
		if e.interrupted() {
			return // bail unpublished; the boundary applies the change
		}
		fill := e.r(selFill) & fillMask
		if fill != fill0 {
			fillMoved = true
		}
		if fill >= target || !e.clk.Now().Before(deadline) {
			break
		}
		if (i+1)%16 == 0 {
			e.serviceCommands() // mid-frame pump: fill-wait is not a halt window
			e.beatN.Add(1)
		}
		e.clk.Sleep(200 * time.Microsecond)
	}

	haltOK := e.halt()
	f := e.arena.Write()
	capCols := e.band.DrainCols() // display span + deadline-gated centring margin
	winCols := e.band.WinCols()   // the 10-division display span
	e.drain(f, capCols)
	e.armEngine() // refill during the reduction

	disc := f.C1[:capCols]
	if int(e.trigSrc.Load()) == 1 {
		disc = f.C2[:capCols]
	}
	_, _, p := ptp(disc)

	// TRIGGER the envelope band when the drained record carries a confident edge
	// on a real, well-sampled signal: publish it as a normal edge-centred trace
	// (the same window() path the decimated bands use). The record carries a
	// centring margin (EnvCaptureCols > WinCols on the fast bands), so window()
	// re-centres the anchor onto real captured samples instead of repeat-extending
	// the screen edges — a STABLE triggered waveform across the whole width. This
	// replaces the free-running min/max band a repetitive signal used to show
	// (untriggered acquisitions accumulate at random phase). Fall back to the
	// envelope min/max SCATTER when there is no trigger to be had — an aliased
	// (few-samples/period) signal, a flat/quiet screen, or the level off the
	// signal — which keeps a live display and the glitch-catching envelope exactly
	// where triggering is impossible.
	td := e.trigDispLevel(int(e.trigSrc.Load()))
	edgeX := -1.0
	if td >= 0 && signalPresent(disc, float64(e.tuneSigK.Load())) {
		edgeX = centerCross(disc, td, e.trigRising.Load())
	}
	triggered := edgeX >= 0

	f.Valid = capCols
	f.WinCols = winCols
	f.Interp = false
	f.Degraded = false
	f.Ptp = p
	f.TrigPos = 0
	f.Coherent = haltOK && fillMoved
	f.HaltOK = haltOK
	f.RollCodes = false
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm
	if triggered {
		f.EdgeX = edgeX
		f.IsEnv = false
		f.EnvCols = 0
		f.Trigd = true
	} else {
		// Scatter the CENTRAL display span (not the margin) so the untriggered
		// envelope keeps the intended 10-division width.
		off := (capCols - winCols) / 2
		e.envPush(f, off, winCols)
		e.envReduce(f)
		f.EdgeX = -1
		f.IsEnv = true
		f.EnvCols = envDisplayCols
		f.Trigd = false
	}

	e.commitStats(f.Coherent, haltOK, p, 0, 0, 0)
	e.zoneMaskUncomparable() // envelope-origin frames don't participate in zone/mask
	e.commitPublish(f)

	if !fillMoved && p < 3 {
		e.deadEvidence(false)
	} else {
		e.resetDeadRuns()
	}
	e.pace(start)
}

// envPush copies the central [off:off+n] slice of the drained record into the
// phase-scatter ring (off skips the centring margin captured for the triggered
// path, so the untriggered envelope keeps the intended display width).
func (e *Engine) envPush(f *Frame, off, n int) {
	if e.envRing1 == nil {
		e.envRing1 = make([][]uint8, envRingN)
		e.envRing2 = make([][]uint8, envRingN)
		for i := range e.envRing1 {
			e.envRing1[i] = make([]uint8, 0, envMaxWin)
			e.envRing2[i] = make([]uint8, 0, envMaxWin)
		}
	}
	if off < 0 {
		off = 0
	}
	i := e.envRingPos
	e.envRing1[i] = append(e.envRing1[i][:0], f.C1[off:off+n]...)
	e.envRing2[i] = append(e.envRing2[i][:0], f.C2[off:off+n]...)
	e.envRingPos = (e.envRingPos + 1) % envRingN
	if e.envRingCnt < envRingN {
		e.envRingCnt++
	}
}

// envReduce reduces the ring into per-column (min,max): every ring sample i
// bins to col = i·800/len; never-seen columns copy the nearest SEEN
// neighbour (real amplitude, never invented).
func (e *Engine) envReduce(f *Frame) {
	reduce := func(ring [][]uint8, mn, mx []uint8) {
		var seen [envDisplayCols]bool
		for c := 0; c < envDisplayCols; c++ {
			mn[c], mx[c] = 0xff, 0
		}
		for r := 0; r < e.envRingCnt; r++ {
			s := ring[r]
			n := len(s)
			if n == 0 {
				continue
			}
			for i, v := range s {
				c := i * envDisplayCols / n
				seen[c] = true
				if v < mn[c] {
					mn[c] = v
				}
				if v > mx[c] {
					mx[c] = v
				}
			}
		}
		last := -1
		for c := 0; c < envDisplayCols; c++ {
			if seen[c] {
				last = c
				continue
			}
			if last >= 0 {
				mn[c], mx[c] = mn[last], mx[last]
			}
		}
		// Leading never-seen columns copy the first seen one.
		first := -1
		for c := 0; c < envDisplayCols; c++ {
			if seen[c] {
				first = c
				break
			}
		}
		if first > 0 {
			for c := 0; c < first; c++ {
				mn[c], mx[c] = mn[first], mx[first]
			}
		}
	}
	reduce(e.envRing1[:], f.EnvMin, f.EnvMax)
	reduce(e.envRing2[:], f.EnvMin2, f.EnvMax2)
}

// ---- roll (≥100 ms/div) ----

// rollBringUp arms the free-running roll engine ONCE (spec 04 §2.3): single
// reset-head, write-pointer pulse, go — then a first latched pop pre-fills
// the whole ring with a real sample so unpopulated columns never draw a
// false 0-rail bar. The divisor (fixed 7400) was programmed by bringUp().
func (e *Engine) rollBringUp() {
	if e.rollRing1 == nil {
		e.rollRing1 = make([]uint8, rollWin)
		e.rollRing2 = make([]uint8, rollWin)
	}
	e.w(selArm, opResetHead)
	e.w(selWrPtr, 0x0001)
	e.w(selWrPtr, 0x0000)
	e.w(selArm, opGo)
	e.clk.Sleep(3 * time.Millisecond)
	e.w(selArm, opLatch)
	w1, err1 := e.b.Read(bus.PlaneCS1, selRollC1)
	w2, err2 := e.b.Read(bus.PlaneCS1, selRollC2)
	s1, s2 := uint8(w1>>8), uint8(w2>>8)
	if err1 != nil {
		s1 = 128
	}
	if err2 != nil {
		s2 = 128
	}
	for i := range e.rollRing1 {
		e.rollRing1[i] = s1
		e.rollRing2[i] = s2
	}
	e.rollPos = 0
	e.rollArmed = true
}

// rollUpdate runs one roll update (~220 ms of paced FIFO pops), then pushes
// a scroll snapshot and publishes the 24-snapshot min/max reduction plus the
// raw scrolling ring. NEVER halts (0xC8 freezes the free-run) and NEVER
// reads un-armed (GPMC WAIT wedge, power-cycle only).
func (e *Engine) rollUpdate(norm bool) {
	if !e.rollArmed {
		e.rollBringUp()
	}
	deadline := e.clk.Now().Add(rollBudgetMs * time.Millisecond)
	pace := time.Duration(RollPaceNs())
	errRun := 0
	prev := uint8(0)
	havePrev := false
	for i := 0; i < rollBatch; i++ {
		if e.interrupted() {
			return // bail unpublished; port stays armed (that is safe)
		}
		e.w(selArm, opLatch) // re-snapshot so the FIFO advances
		w1, err := e.b.Read(bus.PlaneCS1, selRollC1)
		if err != nil {
			e.busErr(err)
			if errRun++; errRun >= 8 {
				e.deadEvidence(false)
				return
			}
			// A read error must still be PACED — an unpaced burst of latch+
			// read pairs is exactly the roll-FIFO wedge hazard (spec 04 §10).
			e.clk.Sleep(pace)
			continue
		}
		errRun = 0
		s1 := uint8(w1 >> 8)
		if havePrev && s1 == prev {
			// Dwell: the FIFO re-latched the SAME sample (its output changes far slower than
			// we can read it). Storing dwells stacks a long single-rail run and a thin band —
			// skip it so the ring holds only fresh, phase-advancing samples (a solid band).
			prev = s1
			if !e.clk.Now().Before(deadline) {
				break
			}
			e.clk.Sleep(pace)
			continue
		}
		prev, havePrev = s1, true
		e.rollRing1[e.rollPos] = s1
		w2, err2 := e.b.Read(bus.PlaneCS1, selRollC2)
		if err2 == nil {
			e.rollRing2[e.rollPos] = uint8(w2 >> 8)
		} else {
			e.busErr(err2) // C2 errors count toward the wedge signal too
		}
		e.rollPos = (e.rollPos + 1) % rollWin
		if (i+1)%16 == 0 {
			e.serviceCommands() // safe mid-frame: free-running, no halt window
		}
		if !e.clk.Now().Before(deadline) {
			break
		}
		e.clk.Sleep(pace)
	}

	// Copy the ring in scroll order (oldest first) into the frame, snapshot
	// it, and reduce the snapshot deque to the 800-column band.
	f := e.arena.Write()
	for i := 0; i < rollWin; i++ {
		j := (e.rollPos + i) % rollWin
		f.C1[i] = e.rollRing1[j]
		f.C2[i] = e.rollRing2[j]
	}
	e.rollSnap(f)
	e.rollReduce(f)

	_, _, p := ptp(f.C1[:rollWin])

	f.Valid = rollWin
	f.WinCols = rollWin
	f.EdgeX = -1
	f.Interp = false
	f.IsEnv = true
	f.Degraded = false
	f.EnvCols = envDisplayCols
	f.Ptp = p
	f.Trigd = false
	f.TrigPos = 0
	f.Coherent = true // paced pops succeeded; there is no halt to confirm
	f.HaltOK = true
	f.RollCodes = true
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm

	e.commitStats(true, true, p, 0, 0, 0)
	e.zoneMaskUncomparable() // roll frames free-run untriggered: zone/mask can't run
	e.commitPublish(f)
	e.resetDeadRuns()
}

// rollSnap pushes a scroll snapshot (full ring copy) onto the deque, keeping
// the last envRingN. Slabs are reused once the deque is full.
func (e *Engine) rollSnap(f *Frame) {
	push := func(deque *[][]uint8, src []uint8) {
		var slab []uint8
		if len(*deque) == envRingN {
			slab = (*deque)[0][:0]
			*deque = (*deque)[1:]
		} else {
			slab = make([]uint8, 0, rollWin)
		}
		*deque = append(*deque, append(slab, src...))
	}
	push(&e.rollSnaps1, f.C1[:rollWin])
	push(&e.rollSnaps2, f.C2[:rollWin])
}

// rollReduce reduces the snapshot deque to per-column (min,max): sample i
// bins to col = i·800/4096. Every snapshot is at a different scroll phase,
// so the band is solid and stable.
func (e *Engine) rollReduce(f *Frame) {
	reduce := func(snaps [][]uint8, mn, mx []uint8) {
		for c := 0; c < envDisplayCols; c++ {
			mn[c], mx[c] = 0xff, 0
		}
		for _, s := range snaps {
			for i, v := range s {
				c := i * envDisplayCols / rollWin
				if v < mn[c] {
					mn[c] = v
				}
				if v > mx[c] {
					mx[c] = v
				}
			}
		}
		// Solidity (spec 04 §5.3). The roll FIFO reads slowly and its per-0xCB phase step
		// aliases the signal, so per-column binning leaves many columns single-rail during the
		// (multi-second) ring fill. But a signal at a roll timebase (≥100 ms/div) is far faster
		// than the sweep — its TRUE display is a solid rail-to-rail band, every column spanning
		// the full excursion. So when most columns are single-rail yet the GLOBAL excursion over
		// real samples is large (a fast signal aliased thin), fill every column to the real
		// [gmn, gmx] — real ADC min/max, never invented. A genuinely slow signal (columns already
		// show their excursion, few thin) keeps its per-column shape. Never-seen columns draw a
		// mid-line, never a false 0-rail bar.
		gmn, gmx := uint8(0xff), uint8(0)
		thin := 0
		for c := 0; c < envDisplayCols; c++ {
			if mx[c] >= mn[c] { // seen
				if mn[c] < gmn {
					gmn = mn[c]
				}
				if mx[c] > gmx {
					gmx = mx[c]
				}
				if int(mx[c])-int(mn[c]) < 20 {
					thin++
				}
			}
		}
		if gmx > gmn && int(gmx)-int(gmn) >= nativeEdgeMinPtp && thin > envDisplayCols/3 {
			for c := 0; c < envDisplayCols; c++ {
				mn[c], mx[c] = gmn, gmx
			}
		} else {
			for c := 0; c < envDisplayCols; c++ {
				if mn[c] > mx[c] { // never-seen
					mn[c], mx[c] = 128, 128
				}
			}
		}
	}
	reduce(e.rollSnaps1, f.EnvMin, f.EnvMax)
	reduce(e.rollSnaps2, f.EnvMin2, f.EnvMax2)
}

// ---- shared publish/stat helpers ----

// commitStats records the per-frame telemetry mirror.
func (e *Engine) commitStats(coherent, haltOK bool, p, trigPos int, armToLatch, drainMs float64) {
	e.mu.Lock()
	if coherent {
		e.stats.Coherent++
	}
	if haltOK {
		e.stats.HaltConfirm++
	}
	e.stats.LastPtp = p
	e.stats.LastTrigPos = trigPos
	if armToLatch > 0 {
		e.stats.ArmToLatch = armToLatch
	}
	if drainMs > 0 {
		e.stats.DrainMs = drainMs
	}
	e.mu.Unlock()
}

// commitPublish stamps the sequence number and hands the frame to the arena.
func (e *Engine) commitPublish(f *Frame) {
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
}
