// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// COMPARE (REQ-SDS-218): rows MASTER, REF A, REF B, MASK and MATH with a
// live difference readout. COMPARE again stacks a master: it averages N
// captures of the trigger source with the engine's Average mode, stores the
// result as REF A, builds the mask from it, restores the acquisition so live
// captures are tested raw, and turns the test on with zeroed counters. A
// master is refused while the trigger source does not repeat from capture
// to capture (the mask build's own rule), with what to change.

// Compare rows.
const (
	cmpMaster = iota
	cmpRefA
	cmpRefB
	cmpMask
	cmpMath
	numCmpRows
)

// stackState is a master being stacked (guarded by Controller.mu).
type stackState struct {
	n, src            int
	prevMode, prevAvg int
	prevMask          int
	seq0              uint64
	frames            int // captures seen while stacking
	got               int // captures the average holds (the frame says)
	pending           bool
}

// stabCols is the repeat test's resolution: screen columns compared.
const stabCols = 400

// screenDivs maps a channel's window onto cols columns in divisions at the
// live scale (column extremes' midpoint, as the trace draws them).
// TRLC-LINKS: REQ-SDS-218
func screenDivs(f *engine.Frame, s *sv, ch, cols int) []float64 {
	valid := framecalc.Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return nil
	}
	g := geom{valid: valid, winCols: f.WinCols, edgeX: f.EdgeX, sampleS: f.SampleS}
	left, win := windowOf(g, s)
	sig := framecalc.Channel(f, ch, valid)
	if s.cpl[ch] != analog.CplDC {
		sig = analog.CoupleDisplay(sig, s.cpl[ch])
	}
	z := float64(max(analog.Detents[s.vidx[ch]].Zoom, 1))
	out := make([]float64, cols)
	// Column x ends where x+1 starts: one floor a column, and from a
	// non-negative left the floor is the conversion's truncation (no float
	// compare, which stalls this core's pipeline).
	pos := func(x int) float64 { return left + float64(x)*float64(win)/float64(cols) }
	trunc := left >= 0 && pos(cols) < 1<<31
	next := floorInt(pos(0))
	for x := range out {
		start := next
		if trunc {
			next = int(pos(x + 1))
		} else {
			next = floorInt(pos(x + 1))
		}
		lo := clampInt(start, 0, valid-1)
		hi := clampInt(next-1, lo, valid-1)
		mn, mx := sig[lo], sig[lo]
		for _, v := range sig[lo : hi+1] {
			mn, mx = min(mn, v), max(mx, v)
		}
		out[x] = (float64(mn)+float64(mx))/2 - 128
		out[x] = out[x] * z / 25
	}
	return out
}

// floorInt is int(math.Floor(v)) for the sample position of a screen
// column: math.Floor is a software routine on this ARM and this runs per
// column per frame. Out of int32 range, or NaN, it defers to math.Floor.
// TRLC-LINKS: REQ-SDS-218
func floorInt(v float64) int {
	if !(v > -1<<31 && v < 1<<31) {
		return int(math.Floor(v))
	}
	i := int(v)
	if float64(i) > v {
		i--
	}
	return i
}

// screenPoints is channel ch of f at the LCD's RefCols screen columns, in
// divisions of the analog range about the centre, sampled as a reference
// stores them (screenColumns: the record at each column's start, linear when
// the frame interpolates): a reference stored from the capture on screen is
// 0 apart from it, and the web page's difference readout, which samples the
// live record at the reference's columns, reads what the LCD reads.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-227
func screenPoints(f *engine.Frame, s *sv, ch int) []float64 {
	valid := framecalc.Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return nil
	}
	g := geom{valid: valid, winCols: f.WinCols, edgeX: f.EdgeX, sampleS: f.SampleS}
	left, win := windowOf(g, s)
	sig := framecalc.Channel(f, ch, valid)
	if s.cpl[ch] != analog.CplDC {
		sig = analog.CoupleDisplay(sig, s.cpl[ch])
	}
	z := float64(max(analog.Detents[s.vidx[ch]].Zoom, 1))
	out := make([]float64, RefCols)
	for x := range out {
		pos := min(max(left+float64(x)*float64(win)/RefCols, 0), float64(valid-1))
		i := int(pos)
		v := float64(sig[i])
		if f.Interp && i+1 < valid {
			v += (float64(sig[i+1]) - v) * (pos - float64(i))
		}
		out[x] = (v - 128) * z / 25
	}
	return out
}

// stabK is what the repeat test depends on, compared as a value (floats by
// their bits): built each frame, it formats nothing.
type stabK struct {
	src, trigType, acq, zoom, serial int
	vidx, cpl                        [2]int
	offV, probe                      [2]uint64
	tdiv, pos, zoomOff, holdoff      uint64
	rising                           bool
	trigCode                         uint16
}

// stabKey is the repeat test's dependencies now.
// TRLC-LINKS: REQ-SDS-218
func stabKey(s *sv) stabK {
	b := math.Float64bits
	return stabK{src: s.st.TrigSource & 1, trigType: s.st.TrigType, acq: s.st.AcqMode, zoom: s.zoom, serial: s.st.SerialMode,
		vidx: s.vidx, cpl: s.cpl, offV: [2]uint64{b(s.offV[0]), b(s.offV[1])}, probe: [2]uint64{b(s.probe[0]), b(s.probe[1])},
		tdiv: b(s.st.TdivS), pos: b(s.st.TrigPosFrac), zoomOff: b(s.zoomOff), holdoff: b(s.st.HoldoffS),
		rising: s.st.TrigRising, trigCode: s.st.TrigCode}
}

// stabFrame tests the trigger source's capture against the previous one.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stabFrame(a *anaState, s *sv, f *engine.Frame) {
	src := s.st.TrigSource & 1
	if s.st.AcqMode == engine.AcqAverage || !s.chDisp[src] || f.IsEnv {
		a.stabPrev = nil
		return
	}
	if k := stabKey(s); k != a.stabKey {
		a.stabKey, a.stabPrev, a.stabRes, a.stabFed = k, nil, nil, frameKey{}
	}
	now := c.now()
	if d := now.Sub(a.stabAt); a.stabPrev != nil && d >= 0 && d < stabEvery {
		return // tested a moment ago
	}
	if seen(&a.stabFed, keyOf(f)) {
		return // tested already
	}
	a.stabAt = now
	cur := screenDivs(f, s, src, stabCols)
	if cur == nil {
		return
	}
	if a.stabPrev != nil {
		win := max(f.WinCols, 1)
		slack := max(1, int(math.Round(6*float64(stabCols)/float64(win))))
		a.stabRes = append(a.stabRes, anaRepeats(a.stabPrev, cur, 0.25, slack))
		if len(a.stabRes) > stabN {
			a.stabRes = a.stabRes[len(a.stabRes)-stabN:]
		}
	}
	a.stabPrev = cur
}

// stabVerdict is how many of the last captures repeated; ok is false while
// there are too few to tell.
// TRLC-LINKS: REQ-SDS-218
func stabVerdict(a *anaState) (nOK, n int, stable, ok bool) {
	n = len(a.stabRes)
	if n < stabMin {
		return 0, n, false, false
	}
	for _, r := range a.stabRes {
		if r {
			nOK++
		}
	}
	return nOK, n, float64(nOK) >= 0.75*float64(n), true
}

// refDiffFrame compares each stored reference with the live trace.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) refDiffFrame(a *anaState, s *sv, f *engine.Frame) {
	src := s.st.TrigSource & 1
	c.mu.Lock()
	refs := c.refs
	c.mu.Unlock()
	for i, r := range refs {
		d := refDiffResult{}
		switch {
		case !r.has:
		case !s.chDisp[src]:
			d.why = fmt.Sprintf("C%d is off", src+1)
		case r.tdiv > 0 && math.Abs(r.tdiv-s.st.TdivS) > r.tdiv*1e-6:
			d.why = "stored at " + si(r.tdiv, "s") + "/div"
		case f.IsEnv:
			d.why = "no sampled frame"
		default:
			codes := r.c1
			if src == 1 {
				codes = r.c2
			}
			live := screenPoints(f, s, src)
			if len(codes) != RefCols || live == nil {
				d.why = "no trace"
				break
			}
			vdivLive := analog.AnalogVdiv(s.vidx[src])
			offLive := s.offV[src]
			// compared at the probe tip, each through its own probe (as the
			// web's difference): the reference's tip volts at the live probe
			probes := max(r.probe[src], 1) / max(s.probe[src], 1)
			ref := make([]float64, RefCols)
			for x, v := range codes {
				volts := ((float64(v)-128)*r.vdiv[src]/25 - r.off[src]) * probes
				ref[x] = (volts + offLive) / vdivLive
			}
			for x := range live { // live in divisions of the analog range
				live[x] = live[x] / float64(max(analog.Detents[s.vidx[src]].Zoom, 1))
			}
			mx, rms, pct := anaRefDiff(live, ref, 0.5)
			vt := vdivLive * s.probe[src]
			d = refDiffResult{ok: !math.IsNaN(mx), max: mx * vt, rms: rms * vt, pct: pct}
		}
		a.refDiff[i] = d
	}
}

// ---- the master ----

var blockAvgRE = regexp.MustCompile(`\baverage (\d+)/(\d+)`)

// stackStart is COMPARE again (and the sheet's Stack master): average the
// trigger source into a master, unless it does not repeat. A refusal's
// error is the notice the LCD shows, so a remote says it in the same words.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stackStart() error {
	s := c.snap()
	src := s.st.TrigSource & 1
	if !s.chDisp[src] {
		err := fmt.Errorf("No master: the trigger source CH%d is off, and the master is stacked from it", src+1)
		c.toast(err.Error(), 1800*time.Millisecond)
		return err
	}
	c.mu.Lock()
	stacking := c.stack != nil
	c.mu.Unlock()
	if stacking {
		return nil // already stacking (in its own Average)
	}
	// The master is tested against raw live captures, and whether the source
	// repeats is judged on raw captures: in Average neither holds. A master
	// started there would average a source that does not repeat and restore
	// Average, so its mask never comes, or fails every capture once the
	// acquisition goes back to Normal.
	if s.st.AcqMode == engine.AcqAverage {
		err := fmt.Errorf("No master: the acquisition is Average - set it to Normal, so the master can check that C%d repeats", src+1)
		c.toast(err.Error(), 2800*time.Millisecond)
		return err
	}
	c.amu.Lock()
	nOK, n, stable, known := stabVerdict(&c.ana)
	c.amu.Unlock()
	if known && !stable {
		err := fmt.Errorf("No master: C%d differs between captures (%d/%d repeat) - set a stable trigger first", src+1, nOK, n)
		c.toast(err.Error(), 2800*time.Millisecond)
		return err
	}
	c.mu.Lock()
	if c.stack != nil {
		c.mu.Unlock()
		return nil
	}
	st := &stackState{n: c.stackN, src: src, prevMode: s.st.AcqMode, prevAvg: s.st.AvgCount, prevMask: s.st.MaskMode, seq0: s.st.Seq}
	c.stack = st
	c.mu.Unlock()
	if s.st.MaskMode != 0 {
		c.eng.SetMaskMode(0) // the averaged captures are not tested
	}
	c.eng.SetAvgCount(st.n)
	c.eng.SetAcqMode(engine.AcqAverage)
	if !s.st.Running {
		c.setRun(0)
	}
	c.toast(fmt.Sprintf("Stacking %d captures of C%d for the master - F5 again cancels", st.n, src+1), 2000*time.Millisecond)
	return nil
}

// stackCount is how many captures the frame's average holds.
// TRLC-LINKS: REQ-SDS-218
func stackCount(f *engine.Frame, st *stackState) int {
	if m := blockAvgRE.FindStringSubmatch(f.Filter); m != nil {
		k, _ := strconv.Atoi(m[1])
		return k
	}
	return st.frames
}

// stackProgress follows a stack after each frame; it reports when the
// master is complete. A changed acquisition stops it, keeping the change.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stackProgress(f *engine.Frame) bool {
	st := c.eng.Snapshot()
	c.mu.Lock()
	sk := c.stack
	if sk == nil || sk.pending {
		c.mu.Unlock()
		return false
	}
	if st.AcqMode != engine.AcqAverage || st.AvgCount != sk.n {
		c.mu.Unlock()
		c.stackCancel("Master stopped: the acquisition was changed", true)
		return false
	}
	if f.Seq != sk.seq0 {
		sk.frames++
		sk.seq0 = f.Seq
	}
	sk.got = stackCount(f, sk)
	done := sk.got >= sk.n
	if done {
		sk.pending = true
	}
	c.mu.Unlock()
	return done
}

// stackStatus is the stack's progress for the pane: captures so far.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stackStatus() (n, of, src int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stack == nil {
		return 0, 0, 0, false
	}
	return min(c.stack.got, c.stack.n), c.stack.n, c.stack.src, true
}

// masterSrc is a finished master waiting for a live capture to build its
// mask in (masterMaskFrame).
type masterSrc struct {
	sig     []uint8 // the trigger source's averaged samples
	edge    float64 // the master's trigger position, in its samples
	sampleS float64
	ch      int
	mode    int       // the test mode to start: count, or stop on fail
	until   time.Time // no live capture by then: REF A stays without a mask
	gen     uint64    // the mask clears when it was made: a clear since drops it
	geom    string    // the live captures' geometry so far, and in how many captures in a row
	seen    int
}

// masterMaskWait is how long a finished master waits for a live capture.
const masterMaskWait = 5 * time.Second

// masterSteady is how many live captures in a row must share a geometry
// before the master's mask is built in it: a capture made while Average was
// switched off can come from the filtered record (another geometry) without
// naming its average, and a mask in its geometry skipped every live capture.
const masterSteady = 3

// stackFinish stores the averaged frame as REF A and restores the
// acquisition; the mask follows in the geometry of the next live capture
// (masterMaskFrame). The averaged captures are not that geometry: on the
// SRAM images they are a filtered record at another sample interval, where
// the live captures are peak-detect readouts, and a mask in theirs had every
// live capture skipped as not comparable.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stackFinish(f *engine.Frame) {
	c.mu.Lock()
	st := c.stack
	c.stack = nil
	c.mu.Unlock()
	if st == nil {
		return
	}
	c.captureRefFrom(f, 0)
	var src *masterSrc
	if c.eng.Snapshot().TrigSource&1 == st.src && f.EdgeX >= 0 && f.SampleS > 0 && !f.IsEnv {
		sig := f.C1
		if st.src == 1 {
			sig = f.C2
		}
		if n := min(f.Valid, len(sig)); n > 0 {
			mode := 1
			if st.prevMask == 2 {
				mode = 2
			}
			src = &masterSrc{sig: append([]uint8(nil), sig[:n]...), edge: f.EdgeX, sampleS: f.SampleS, ch: st.src, mode: mode}
		}
	}
	c.eng.SetAcqMode(st.prevMode)
	c.eng.SetAvgCount(st.prevAvg)
	c.mu.Lock()
	c.ui.hasMaster, c.ui.masterN, c.ui.masterSrc, c.ui.masterT = true, st.n, st.src, c.now()
	c.ui.cmpRow = cmpMask
	msg := fmt.Sprintf("Master in REF A (%d captures) - building its mask from the next live capture", st.n)
	if src != nil {
		src.until, src.gen = c.now().Add(masterMaskWait), c.maskGen
		c.masterPend = src
	} else {
		msg = fmt.Sprintf("Master in REF A (%d captures); no mask (the trigger source changed)", st.n)
	}
	c.toastLocked(msg, 2600*time.Millisecond)
	c.mu.Unlock()
	c.maskSetMsg("")
	c.publish()
}

// masterMaskFrame builds a finished master's mask in the geometry of the
// live captures after it (once masterSteady of them share it), starts the
// test and zeroes its counts. A clear since drops the master's mask; with no
// live captures within masterMaskWait, REF A stays without one.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-139
func (c *Controller) masterMaskFrame(f *engine.Frame) {
	c.mu.Lock()
	src := c.masterPend
	if src != nil && src.gen != c.maskGen {
		c.masterPend, src = nil, nil
	}
	tol := maskTols[c.maskTol%len(maskTols)]
	c.mu.Unlock()
	if src == nil {
		return
	}
	st := c.eng.Snapshot()
	var m *engine.Mask
	if st.AcqMode != engine.AcqAverage && !blockAvgRE.MatchString(f.Filter) && f.EdgeX >= 0 && f.SampleS > 0 && !f.IsEnv {
		g := fmt.Sprint(f.WinCols, min(f.Valid, len(f.C1)), f.TdivS, f.SampleS, f.PeakDetect)
		c.mu.Lock()
		if g == src.geom {
			src.seen++
		} else {
			src.geom, src.seen = g, 1
		}
		steady := src.seen >= masterSteady
		c.mu.Unlock()
		if steady {
			posFrac := st.TrigPosFrac
			if math.IsNaN(posFrac) || posFrac < 0 || posFrac > 1 {
				posFrac = 0.5
			}
			m = masterMaskOnto(src, f, posFrac, tol[0], tol[1])
		}
	}
	if m == nil && !c.now().After(src.until) {
		return // the averaged captures still in flight: wait for a live one
	}
	c.mu.Lock()
	if c.masterPend != src { // cleared meanwhile
		c.mu.Unlock()
		return
	}
	c.masterPend = nil
	c.mu.Unlock()
	if m == nil {
		c.toast("The master is in REF A, but no live capture came to build its mask in", 2600*time.Millisecond)
		c.publish()
		return
	}
	c.eng.SetMask(m)
	c.noteMaskSetup()
	c.eng.SetMaskMode(src.mode)
	c.eng.ClearMaskFails()
	c.toast("Mask built from the master - testing live captures", 2000*time.Millisecond)
	c.publish()
}

// masterMaskOnto maps the averaged master onto a live capture's geometry by
// time from the trigger, column by column as the engine's test reads the
// capture: each column's bounds are the master's min and max over the
// column's span, dilated by the tolerance (the web page's masterMask does the
// same). Columns the master does not cover always pass.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-139
func masterMaskOnto(src *masterSrc, live *engine.Frame, posFrac float64, tolT, tolV int) *engine.Mask {
	sig := live.C1
	if src.ch == 1 {
		sig = live.C2
	}
	valid := min(live.Valid, len(sig))
	win := live.WinCols
	if win <= 0 || win > valid {
		win = valid
	}
	n := len(src.sig)
	if win <= 0 || n == 0 || !(src.sampleS > 0) {
		return nil
	}
	lo, hi := make([]uint8, win), make([]uint8, win)
	left := math.Round(live.EdgeX - float64(win)*posFrac)
	r := math.Max(0.5, live.SampleS/src.sampleS/2)
	for j := range lo {
		lo[j], hi[j] = 255, 0
		x := src.edge + (left+float64(j)-live.EdgeX)*live.SampleS/src.sampleS
		if x < 0 || x > float64(n-1) {
			continue
		}
		mn, mx := uint8(255), uint8(0)
		for k := max(0, int(math.Floor(x-r))); k <= min(n-1, int(math.Ceil(x+r))); k++ {
			mn, mx = min(mn, src.sig[k]), max(mx, src.sig[k])
		}
		lo[j], hi[j] = mn, mx
	}
	m := engine.BuildMaskFromEnvelope(lo, hi, win, tolT, tolV, src.ch)
	if m == nil {
		return nil
	}
	m.FrameIdent, m.TdivS, m.SampleS, m.PeakDetect = true, live.TdivS, live.SampleS, live.PeakDetect
	return m
}

// stackCancel stops a stack: the acquisition is restored unless the user
// changed it (keepAcq), the mask test mode is put back.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stackCancel(why string, keepAcq bool) {
	c.mu.Lock()
	st := c.stack
	c.stack = nil
	c.mu.Unlock()
	if st == nil {
		return
	}
	if !keepAcq {
		c.eng.SetAcqMode(st.prevMode)
		c.eng.SetAvgCount(st.prevAvg)
	} else if c.eng.Snapshot().AcqMode != engine.AcqAverage {
		c.eng.SetAvgCount(st.prevAvg) // their new mode stays; the stack's depth goes
	}
	if c.eng.Snapshot().MaskMode != st.prevMask {
		c.eng.SetMaskMode(st.prevMask)
	}
	if why == "" {
		why = "Master cancelled - acquisition restored"
	}
	if st.prevMask != 0 {
		why += ", mask counts restarted"
	}
	c.toast(why, 2000*time.Millisecond)
}

// ---- rows ----

// mathNext is the MATH row's order: Off, C1-C2, C2-C1, C1+C2, C1xC2.
var mathNext = map[int]int{0: 2, 2: 3, 3: 1, 1: 4, 4: 0}

// mathNames name the math functions.
var mathNames = []string{"Off", "C1+C2", "C1-C2", "C2-C1", "C1xC2"}

// maskNext is the MASK row push once a mask exists: a mask built with its
// test off counts, counting stops on a fail; after stop on fail the push
// clears the mask (the sheet's Mask test turns a test off and keeps it).
var maskNext = map[int]int{0: 1, 1: 2}

// cmpAdjust is Adjust in COMPARE: the row.
// TRLC-LINKS: REQ-SDS-218, REQ-SDS-213
func (c *Controller) cmpAdjust(dir int) {
	c.mu.Lock()
	c.ui.cmpRow = clampInt(c.ui.cmpRow+dir, 0, numCmpRows-1)
	c.mu.Unlock()
}

// cmpAction is the Adjust push in COMPARE: the row's action.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) cmpAction() {
	s := c.snap()
	switch s.ui.cmpRow {
	case cmpMaster:
		if s.stacking {
			c.toast("Stacking the master: F5 again cancels", 1400*time.Millisecond)
			return
		}
		next := stackDepths[0]
		for i, n := range stackDepths {
			if n == s.stackN {
				next = stackDepths[(i+1)%len(stackDepths)]
			}
		}
		c.mu.Lock()
		c.stackN = next
		c.toastLocked(fmt.Sprintf("Master depth %d captures - F5 again stacks it", next), 1400*time.Millisecond)
		c.mu.Unlock()
	case cmpRefA, cmpRefB:
		i := s.ui.cmpRow - cmpRefA
		if !s.refHas[i] {
			if err := c.storeRef(i); err != nil {
				c.toast(err.Error(), 1500*time.Millisecond)
			}
			return
		}
		c.showRef(i, !s.refShow[i])
		c.toast(fmt.Sprintf("REF %c %s", 'A'+i, map[bool]string{true: "hidden", false: "shown"}[s.refShow[i]]), 900*time.Millisecond)
	case cmpMask:
		if s.st.MaskSet {
			next, ok := maskNext[s.st.MaskMode]
			if !ok { // after stop on fail
				c.clearMaskAction()
				return
			}
			c.setMaskMode(next)
			c.toast("Mask test: "+[]string{"off", "counting (pass/fail)", "stops on the first fail"}[next], 1400*time.Millisecond)
			return
		}
		src := s.st.TrigSource & 1
		if !s.chDisp[src] {
			c.toast(fmt.Sprintf("The trigger source CH%d is off", src+1), 1500*time.Millisecond)
			return
		}
		c.mu.Lock()
		c.maskAutoTest = true
		c.mu.Unlock()
		c.maskBuildStart()
	case cmpMath:
		next := mathNext[s.mathMode]
		c.setMath(next)
		c.toast("Math "+mathNames[next], 900*time.Millisecond)
	}
}

// cmpActionText is what the selected row's push does, at its right end.
// TRLC-LINKS: REQ-SDS-218
func cmpActionText(s *sv, row int) string {
	switch row {
	case cmpMaster:
		if s.stacking {
			return "F5: cancel"
		}
		next := stackDepths[0]
		for i, n := range stackDepths {
			if n == s.stackN {
				next = stackDepths[(i+1)%len(stackDepths)]
			}
		}
		return fmt.Sprintf("push: depth %d", next)
	case cmpRefA, cmpRefB:
		i := row - cmpRefA
		if !s.refHas[i] {
			return "push: store"
		}
		return "push: " + map[bool]string{true: "hide", false: "show"}[s.refShow[i]]
	case cmpMask:
		if s.st.MaskSet {
			return "push: " + map[int]string{1: "stop on fail", 2: "clear mask", 0: "count"}[s.st.MaskMode]
		}
		return fmt.Sprintf("push: build from C%d", s.st.TrigSource&1+1)
	}
	return "push: math " + mathNames[mathNext[s.mathMode]]
}

// cmpAgainText is what COMPARE again does now.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) cmpAgainText(s *sv) string {
	if s.stacking {
		return "cancel stack"
	}
	c.amu.Lock()
	_, _, stable, known := stabVerdict(&c.ana)
	c.amu.Unlock()
	if known && !stable && s.st.AcqMode != engine.AcqAverage {
		return "needs a stable trigger"
	}
	return "stack master"
}

// RepeatCaption is COMPARE's word on the trigger source: whether it repeats
// capture after capture (a master or a mask needs it), and warn when it does
// not or is off. The web's COMPARE shows the same line.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) RepeatCaption() (string, bool) {
	cap, col := c.repeatCaption(c.snap())
	return cap, col == wsview.Warn
}

// repeatCaption is RepeatCaption's text and colour.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) repeatCaption(s *sv) (string, wsview.Col) {
	src := s.st.TrigSource & 1
	c.amu.Lock()
	nOK, n, stable, known := stabVerdict(&c.ana)
	c.amu.Unlock()
	cap, capCol := fmt.Sprintf("trigger source C%d", src+1), wsview.Text
	switch {
	case !s.chDisp[src]:
		cap, capCol = cap+" is off", wsview.Warn
	case s.stacking || s.st.AcqMode == engine.AcqAverage:
		cap += ": averaging"
	case !s.st.Running && !known:
		cap += ": stopped"
	case !known:
		cap += ": checking that it repeats..."
	case stable:
		cap += fmt.Sprintf(" repeats (%d/%d captures fit the last one)", nOK, n)
	default:
		cap, capCol = cap+fmt.Sprintf(" does not repeat (%d/%d): a master would smear", nOK, n), wsview.Warn
	}
	return cap, capCol
}

// cmpPane builds the COMPARE pane.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) cmpPane(s *sv) *wsview.CmpPane {
	p := &wsview.CmpPane{}
	p.Title = "COMPARE"
	src := s.st.TrigSource & 1
	c.amu.Lock()
	_, _, stable, known := stabVerdict(&c.ana)
	diffs := c.ana.refDiff
	c.amu.Unlock()
	cap, capCol := c.repeatCaption(s)
	p.Caption, p.CapCol = cap, capCol
	sel := clampInt(s.ui.cmpRow, 0, numCmpRows-1)
	for i := 0; i < numCmpRows; i++ {
		r := wsview.CmpRow{Sel: i == sel, InfoCol: wsview.Text, ExtraCol: wsview.Text}
		switch i {
		case cmpMaster:
			r.Name = "MASTER"
			if k, of, ssrc, ok := c.stackStatus(); ok {
				r.Info, r.InfoCol = fmt.Sprintf("STACK %d/%d of C%d", k, of, ssrc+1), wsview.Amber
				r.Bar, r.BarN, r.BarOf = true, k, of
				r.BarNote, r.BarNoteCol = "averaging - then REF A + mask, test raw", wsview.Dim
				if !s.st.Running {
					r.BarNote, r.BarNoteCol = "stopped - Run/Stop continues", wsview.Warn
				}
			} else if known && !stable && s.st.AcqMode != engine.AcqAverage {
				r.Info, r.InfoCol = "needs a stable trigger", wsview.Warn
				r.Extra, r.ExtraCol = "Trig Menu: holdoff or another edge; or a BUS serial trigger", wsview.Warn
			} else if s.ui.hasMaster {
				r.Info = fmt.Sprintf("REF A: %d x C%d, %.0f s ago", s.ui.masterN, s.ui.masterSrc+1, s.now.Sub(s.ui.masterT).Seconds())
				r.Extra, r.ExtraCol = "mask from the stack; live captures tested raw", wsview.Dim
			} else {
				r.Info, r.InfoCol = fmt.Sprintf("depth %d captures of C%d", s.stackN, src+1), wsview.Dim
				r.Extra, r.ExtraCol = "F5 again: average, store REF A, build the mask, test raw", wsview.Dim
			}
		case cmpRefA, cmpRefB:
			k := i - cmpRefA
			r.Name = fmt.Sprintf("REF %c", 'A'+k)
			if !s.refHas[k] {
				r.Info, r.InfoCol = "empty", wsview.Dim
			} else {
				c.mu.Lock()
				ref := c.refs[k]
				c.mu.Unlock()
				r.Info = fmt.Sprintf("C1+C2 %s %s", si(ref.vdiv[src]*max(ref.probe[src], 1), "V"), si(ref.tdiv, "s"))
				r.Info += map[bool]string{true: "  shown", false: "  hidden"}[s.refShow[k]]
				d := diffs[k]
				switch {
				case d.why != "":
					r.Extra = d.why
				case d.ok:
					r.Extra = fmt.Sprintf("max|d| %s  rms %s  %.1f%% in 0.5div", si(d.max, "V"), si(d.rms, "V"), d.pct)
				}
			}
		case cmpMask:
			r.Name = "MASK"
			if !s.st.MaskSet {
				r.Info, r.InfoCol = "none", wsview.Dim
				if s.maskBusy {
					r.Info, r.InfoCol = "building...", wsview.Amber
				}
			} else {
				r.Info = []string{"built, test off", "counting", "stops on a fail"}[clampInt(s.st.MaskMode, 0, 2)]
				if s.st.MaskMode != 0 {
					tot := s.st.MaskPass + s.st.MaskFail
					r.Extra = fmt.Sprintf("pass %d  fail %d", s.st.MaskPass, s.st.MaskFail)
					if tot > 0 {
						r.Extra += fmt.Sprintf("  (%.1f%% fail)", 100*float64(s.st.MaskFail)/float64(tot))
					}
					// captures the mask cannot be compared with (a V/div,
					// offset or timebase since it was built) count here too,
					// or a test that stopped testing would read as passing
					if s.st.MaskSkip > 0 {
						r.Extra += fmt.Sprintf("  skip %d", s.st.MaskSkip)
						r.ExtraCol = wsview.Amber
					}
				}
				if s.st.MaskFail > 0 {
					r.ExtraCol = wsview.Stop
				}
			}
		case cmpMath:
			r.Name = "MATH"
			r.Info = mathNames[clampInt(s.mathMode, 0, 4)]
			if s.mathMode == 0 {
				r.InfoCol = wsview.Dim
			}
		}
		if r.Sel {
			r.Action = cmpActionText(s, i)
		}
		p.Rows[i] = r
	}
	p.Tool = toolLine(s, wsview.Seg{Key: "Adjust", Job: "row"}, wsview.Seg{Key: "push", Job: "row action"})
	p.Again = "F5 again: " + c.cmpAgainText(s)
	return p
}
