// ENGMODEL-OWNER-UNIT: FU-APP-PANEL

package panel

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/labeth/open-dso-core/analysis"
	"github.com/labeth/open-dso-core/dsp"
	"github.com/labeth/open-sds1000cml/app/internal/analog"
	"github.com/labeth/open-sds1000cml/app/internal/engine"
	"github.com/labeth/open-sds1000cml/app/internal/panel/framecalc"
	"github.com/labeth/open-sds1000cml/app/internal/panel/wsview"
)

// COMPARE (REQ-SDS-218): rows MASTER, REF A, REF B, MASK and MATH with a
// live difference readout. COMPARE again stacks a master: it averages N
// captures of the trigger source with the engine's Average mode, stores the
// result as REF A, restores the acquisition, builds the mask from the live
// raw captures after it (as the MASK row does) and turns the test on with
// zeroed counters. A
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

// screenExtremes maps a channel's window onto cols columns in divisions at
// the live scale: each column's lowest and highest sample, as the trace draws
// them. The repeat test holds each capture's column midpoints against the
// band the other's extremes cover (analysis.RepeatsBand): a probe's
// few-nanosecond ringing after an edge, caught at another depth or in the
// next column by another capture, widens the band and moves no midpoint out.
// TRLC-LINKS: REQ-SDS-218
func screenExtremes(f *engine.Frame, s *sv, ch, cols int) (lows, highs []float64) {
	valid := framecalc.Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return nil, nil
	}
	g := geom{valid: valid, winCols: f.WinCols, edgeX: f.EdgeX, sampleS: f.SampleS}
	left, win := windowOf(g, s)
	sig := framecalc.Channel(f, ch, valid)
	if s.cpl[ch] != analog.CplDC {
		sig = analog.CoupleDisplay(sig, s.cpl[ch])
	}
	z := float64(max(analog.Detents[s.vidx[ch]].Zoom, 1)) / 25
	lows, highs = make([]float64, cols), make([]float64, cols)
	// Column x ends where x+1 starts: one floor a column, and from a
	// non-negative left the floor is the conversion's truncation (no float
	// compare, which stalls this core's pipeline).
	pos := func(x int) float64 { return left + float64(x)*float64(win)/float64(cols) }
	trunc := left >= 0 && pos(cols) < 1<<31
	next := floorInt(pos(0))
	for x := range lows {
		start := next
		if trunc {
			next = int(pos(x + 1))
		} else {
			next = floorInt(pos(x + 1))
		}
		lo := clampInt(start, 0, valid-1)
		hi := clampInt(next-1, lo, valid-1)
		mn, mx := dsp.MinMax8(sig[lo : hi+1])
		lows[x], highs[x] = (float64(mn)-128)*z, (float64(mx)-128)*z
	}
	return lows, highs
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
	return stabK{
		src: s.st.TrigSource & 1, trigType: s.st.TrigType, acq: s.st.AcqMode, zoom: s.zoom, serial: s.st.SerialMode,
		vidx: s.vidx, cpl: s.cpl, offV: [2]uint64{b(s.offV[0]), b(s.offV[1])}, probe: [2]uint64{b(s.probe[0]), b(s.probe[1])},
		tdiv: b(s.st.TdivS), pos: b(s.st.TrigPosFrac), zoomOff: b(s.zoomOff), holdoff: b(s.st.HoldoffS),
		rising: s.st.TrigRising, trigCode: s.st.TrigCode,
	}
}

// stabFrame tests the trigger source's capture against the previous one.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) stabFrame(a *anaState, s *sv, f *engine.Frame) {
	src := s.st.TrigSource & 1
	if s.st.AcqMode == engine.AcqAverage || !s.chDisp[src] || f.IsEnv {
		a.stabLows, a.stabHighs = nil, nil
		return
	}
	if k := stabKey(s); k != a.stabKey {
		a.stabKey, a.stabLows, a.stabHighs, a.stabRes, a.stabFed = k, nil, nil, nil, frameKey{}
	}
	now := c.now()
	if d := now.Sub(a.stabAt); a.stabLows != nil && d >= 0 && d < stabEvery {
		return // tested a moment ago
	}
	if seen(&a.stabFed, keyOf(f)) {
		return // tested already
	}
	a.stabAt = now
	lows, highs := screenExtremes(f, s, src, stabCols)
	if lows == nil {
		return
	}
	if a.stabLows != nil {
		win := max(f.WinCols, 1)
		slack := max(1, int(math.Round(6*float64(stabCols)/float64(win))))
		ok := analysis.RepeatsBand(a.stabLows, a.stabHighs, lows, highs, 0.25, slack)
		a.stabRes = append(a.stabRes, ok)
		if len(a.stabRes) > stabN {
			a.stabRes = a.stabRes[len(a.stabRes)-stabN:]
		}
	}
	a.stabLows, a.stabHighs = lows, highs
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
			vdivLive := analog.Vdiv(s.vidx[src])
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
				live[x] /= float64(max(analog.Detents[s.vidx[src]].Zoom, 1))
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
		err := fmt.Errorf("No master: the trigger source CH%d is off, and the master is stacked from it", src+1) //nolint:staticcheck // ST1005: the error is the notice the LCD shows, a capitalised sentence
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
		err := fmt.Errorf("No master: the acquisition is Average - set it to Normal, so the master can check that C%d repeats", src+1) //nolint:staticcheck // ST1005: the error is the notice the LCD shows, a capitalised sentence
		c.toast(err.Error(), 2800*time.Millisecond)
		return err
	}
	c.amu.Lock()
	nOK, n, stable, known := stabVerdict(&c.ana)
	c.amu.Unlock()
	if known && !stable {
		err := fmt.Errorf("No master: C%d differs between captures by over 0.25 div (%d/%d repeat) - set a stable trigger, or quieten its noise and glitches", src+1, nOK, n) //nolint:staticcheck // ST1005: the error is the notice the LCD shows, a capitalised sentence
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
		_ = c.setRun(0) // cannot fail
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

// stackFinish stores the averaged frame as REF A, restores the acquisition
// and builds the mask from the live raw captures after it, as the MASK row
// does, then starts the test. The average is the reference the eye compares
// with; it is no mask: averaging captures that jitter by a fraction of a
// sample flattens a probe's few-nanosecond ringing, and every raw capture
// would ring outside a mask drawn around it.
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
	same := c.eng.Snapshot().TrigSource&1 == st.src
	c.eng.SetAcqMode(st.prevMode)
	c.eng.SetAvgCount(st.prevAvg)
	c.mu.Lock()
	c.ui.hasMaster, c.ui.masterN, c.ui.masterSrc, c.ui.masterT = true, st.n, st.src, c.now()
	c.ui.cmpRow = cmpMask
	msg := fmt.Sprintf("Master in REF A (%d captures) - building its mask from live captures", st.n)
	if same {
		c.maskAutoTest, c.maskAutoMode = true, choose(st.prevMask == 2, 2, 1)
	} else {
		msg = fmt.Sprintf("Master in REF A (%d captures); no mask (the trigger source changed)", st.n)
	}
	c.toastLocked(msg, 2600*time.Millisecond)
	c.mu.Unlock()
	c.maskSetMsg("")
	if same {
		c.maskBuildStart()
	}
	c.publish()
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
		_ = c.showRef(i, !s.refShow[i]) // the reference is held (checked above)
		c.toast(fmt.Sprintf("REF %c %s", 'A'+i, choose(s.refShow[i], "hidden", "shown")), 900*time.Millisecond)
	case cmpMask:
		if s.st.MaskSet {
			next, ok := maskNext[s.st.MaskMode]
			if !ok { // after stop on fail
				_ = c.clearMaskAction() // it toasts its own result
				return
			}
			_ = c.setMaskMode(next) // cannot fail
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
		_ = c.setMath(next) // cannot fail
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
		return "push: " + choose(s.refShow[i], "hide", "show")
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
	caption, col := c.repeatCaption(c.snap())
	return caption, col == wsview.Warn
}

// repeatCaption is RepeatCaption's text and colour.
// TRLC-LINKS: REQ-SDS-218
func (c *Controller) repeatCaption(s *sv) (string, wsview.Col) {
	src := s.st.TrigSource & 1
	c.amu.Lock()
	nOK, n, stable, known := stabVerdict(&c.ana)
	c.amu.Unlock()
	caption, capCol := fmt.Sprintf("trigger source C%d", src+1), wsview.Text
	switch {
	case !s.chDisp[src]:
		caption, capCol = caption+" is off", wsview.Warn
	case s.stacking || s.st.AcqMode == engine.AcqAverage:
		caption += ": averaging"
	case !s.st.Running && !known:
		caption += ": stopped"
	case !known:
		caption += ": checking that it repeats..."
	case stable:
		caption += fmt.Sprintf(" repeats (%d/%d captures fit the last one)", nOK, n)
	default:
		caption, capCol = caption+fmt.Sprintf(" does not repeat within 0.25 div (%d/%d): trigger, noise or glitches", nOK, n), wsview.Warn
	}
	return caption, capCol
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
	caption, capCol := c.repeatCaption(s)
	p.Caption, p.CapCol = caption, capCol
	sel := clampInt(s.ui.cmpRow, 0, numCmpRows-1)
	for i := range numCmpRows {
		r := wsview.CmpRow{Sel: i == sel, InfoCol: wsview.Text, ExtraCol: wsview.Text}
		switch i {
		case cmpMaster:
			r.Name = "MASTER"
			k, of, ssrc, stackOn := c.stackStatus()
			switch {
			case stackOn:
				r.Info, r.InfoCol = fmt.Sprintf("STACK %d/%d of C%d", k, of, ssrc+1), wsview.Amber
				r.Bar, r.BarN, r.BarOf = true, k, of
				r.BarNote, r.BarNoteCol = "averaging - then REF A + mask, test raw", wsview.Dim
				if !s.st.Running {
					r.BarNote, r.BarNoteCol = "stopped - Run/Stop continues", wsview.Warn
				}
			case known && !stable && s.st.AcqMode != engine.AcqAverage:
				r.Info, r.InfoCol = "needs a stable trigger", wsview.Warn
				r.Extra, r.ExtraCol = "Trig Menu: holdoff or another edge; or a BUS serial trigger", wsview.Warn
			case s.ui.hasMaster:
				r.Info = fmt.Sprintf("REF A: %d x C%d, %.0f s ago", s.ui.masterN, s.ui.masterSrc+1, s.now.Sub(s.ui.masterT).Seconds())
				r.Extra, r.ExtraCol = "mask from the live captures after it", wsview.Dim
			default:
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
				r.Info += choose(s.refShow[k], "  shown", "  hidden")
				d := diffs[k] //nolint:gosec // G602: k is 0 or 1 (REF A, REF B), diffs is [2]
				switch {
				case d.why != "":
					r.Extra = d.why
				case d.ok:
					r.Extra = fmt.Sprintf("max|d| %s  rms %s  %.1f%% in 0.5div", si(d.max, "V"), si(d.rms, "V"), d.pct)
				}
			}
		case cmpMask:
			r.Name = "MASK"
			maskRow(s, &r)
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
	p.Tool = toolLine(s, wsview.Seg{Key: ctlAdjust, Job: "row"}, wsview.Seg{Key: ctlPush, Job: "row action"})
	p.Again = "F5 again: " + c.cmpAgainText(s)
	return p
}

// maskRow is the COMPARE pane's MASK row: none, building, or the test's
// mode and its counts.
// TRLC-LINKS: REQ-SDS-218
func maskRow(s *sv, r *wsview.CmpRow) {
	if !s.st.MaskSet {
		r.Info, r.InfoCol = "none", wsview.Dim
		if s.maskBusy {
			r.Info, r.InfoCol = "building...", wsview.Amber
		}
		return
	}
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
