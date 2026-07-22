package engine

import (
	"time"

	"open-sds/app/internal/iface"
)

// Slow-envelope and roll band paths (spec 04). Both display a per-column
// (min,max) band. The owned fabric folds min/max on the LIVE stream into an
// envelope result channel (ENV_DATA/ENV_COUNT, fpga doc §6): the primary path
// consumes those records; the software min/max reducer over phase-scattered
// drained windows is kept as the CPU fallback (spec 03 §3).

// envMaxRecords caps a single envelope-channel drain (2 channels × the display
// columns, with headroom for the overflow re-read).
const envMaxRecords = 2 * envDisplayCols

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
	// Leaving envelope/roll for a real-time band: idle the capture FSM FIRST so
	// the next armed capture starts clean (spec 04 §4.2 step 1).
	if (e.prevKind == KindEnvelope || e.prevKind == KindRoll) &&
		newKind != KindEnvelope && newKind != KindRoll {
		e.w(selOpcode, opReset)
		e.w(selOpcode, opReset)
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

// envFrame runs one envelope frame: arm → modest fill wait → halt → burst-drain
// the window → re-arm → publish. A repetitive, well-sampled signal triggers as a
// normal edge-centred trace (window() re-centres on the captured margin); an
// aliased / flat / off-level signal falls back to the min/max band, taken from
// the fabric's envelope channel (primary) or the software reducer (fallback).
// Publishes EVERY frame in both AUTO and NORM (phase-independent band).
func (e *Engine) envFrame(norm bool) {
	start := e.clk.Now()
	e.armEngine()

	target := e.band.EnvFillTarget()
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
	// on a real, well-sampled signal: publish it as a normal edge-centred trace.
	// Otherwise fall back to the envelope min/max band where triggering is
	// impossible (aliased, flat, or the level off the signal).
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
		// Min/max band: prefer the fabric's envelope channel; fall back to the
		// software reducer over phase-scattered drained windows.
		if !e.envConsumeChannel(f) {
			off := (capCols - winCols) / 2
			e.envPush(f, off, winCols)
			e.envReduce(f)
		}
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

// envConsumeChannel fills the frame's per-column (min,max) band from the
// fabric's envelope result channel (ENV_COUNT record count + ENV_DATA packed
// records, fpga doc §6). Returns false when the channel is empty, so the caller
// runs the software reducer fallback. An overflow flag is honored implicitly:
// the newest records win the fold.
func (e *Engine) envConsumeChannel(f *Frame) bool {
	cw := e.r(selEnvCount)
	n := int(iface.EnvCountCount(cw))
	if n <= 0 {
		return false
	}
	if n > envMaxRecords {
		n = envMaxRecords
	}
	const recW = 3 // envelope record is 3 words (iface.EnvelopeRecord)
	need := n * recW
	if len(e.envChanBuf) < need {
		e.envChanBuf = make([]uint16, need)
	}
	e.b.ChannelInto(selEnvData, e.envChanBuf[:need], need)

	for c := 0; c < envDisplayCols; c++ {
		f.EnvMin[c], f.EnvMax[c] = 0xff, 0
		f.EnvMin2[c], f.EnvMax2[c] = 0xff, 0
	}
	var rec iface.EnvelopeRecord
	for i := 0; i < n; i++ {
		rec.Unpack(e.envChanBuf[i*recW : i*recW+recW])
		c := int(rec.Col)
		if c < 0 || c >= envDisplayCols {
			continue
		}
		mn, mx := f.EnvMin, f.EnvMax
		if rec.Ch == 1 {
			mn, mx = f.EnvMin2, f.EnvMax2
		}
		if rec.Min < mn[c] {
			mn[c] = rec.Min
		}
		if rec.Max > mx[c] {
			mx[c] = rec.Max
		}
	}
	fillUnseen := func(mn, mx []uint8) {
		for c := 0; c < envDisplayCols; c++ {
			if mn[c] > mx[c] { // never-seen column
				mn[c], mx[c] = 128, 128
			}
		}
	}
	fillUnseen(f.EnvMin, f.EnvMax)
	fillUnseen(f.EnvMin2, f.EnvMax2)
	return true
}

// envPush copies the central [off:off+n] slice of the drained record into the
// phase-scatter ring (the software min/max fallback).
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

// rollBringUp initializes the scroll ring. The owned fabric halts and re-arms
// cleanly (spec 03 §5, no vendor free-run freeze), so roll is just a slow,
// scrolled capture band: each rollUpdate captures a fresh batch and scrolls it
// into the ring. No roll FIFO, no latch strobe, no never-halt constraint.
func (e *Engine) rollBringUp() {
	if e.rollRing1 == nil {
		e.rollRing1 = make([]uint8, rollWin)
		e.rollRing2 = make([]uint8, rollWin)
	}
	for i := range e.rollRing1 {
		e.rollRing1[i] = 128
		e.rollRing2[i] = 128
	}
	e.rollPos = 0
	e.rollArmed = true
}

// rollUpdate runs one roll update: capture a fresh batch on the halt engine,
// scroll it into the ring, and publish the 24-snapshot min/max reduction plus
// the raw scrolling ring.
func (e *Engine) rollUpdate(norm bool) {
	if !e.rollArmed {
		e.rollBringUp()
	}
	if e.interrupted() {
		return // bail unpublished; boundary applies the change
	}

	e.armEngine()
	// Pace the fill so the batch spans real capture time; the boundary-style
	// service pump runs mid-fill (no halt window yet).
	deadline := e.clk.Now().Add(rollBudgetMs * time.Millisecond)
	pace := time.Duration(RollPaceNs())
	for i := 0; ; i++ {
		if e.interrupted() {
			return
		}
		if !e.clk.Now().Before(deadline) {
			break
		}
		if (i+1)%16 == 0 {
			e.serviceCommands()
			e.beatN.Add(1)
		}
		e.clk.Sleep(pace)
	}

	haltOK := e.halt()
	if e.rollScratch1 == nil {
		e.rollScratch1 = make([]uint8, rollBatch)
		e.rollScratch2 = make([]uint8, rollBatch)
	}
	e.b.BurstInto(e.rollScratch1, e.rollScratch2, rollBatch)
	e.armEngine() // re-arm during the reduction

	// Scroll the fresh batch into the ring, skipping frozen repeats so the band
	// stays solid (a dwell would stack a single-rail run).
	prev := uint8(0)
	havePrev := false
	for i := 0; i < rollBatch; i++ {
		s1 := e.rollScratch1[i]
		if havePrev && s1 == prev {
			continue
		}
		prev, havePrev = s1, true
		e.rollRing1[e.rollPos] = s1
		e.rollRing2[e.rollPos] = e.rollScratch2[i]
		e.rollPos = (e.rollPos + 1) % rollWin
	}

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
	f.Coherent = haltOK
	f.HaltOK = haltOK
	f.RollCodes = true
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.DisplayedSdivS()
	f.SampleS = e.band.CaptureIntervalNs() * 1e-9
	f.Norm = norm

	e.commitStats(true, haltOK, p, 0, 0, 0)
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
		// Solidity (spec 04 §5.3): a fast signal aliased thin at a roll timebase
		// has a large GLOBAL excursion but many single-rail columns — fill every
		// column to the real [gmn, gmx] (real ADC min/max, never invented). A
		// genuinely slow signal keeps its per-column shape; never-seen columns
		// draw a mid-line, never a false 0-rail bar.
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
