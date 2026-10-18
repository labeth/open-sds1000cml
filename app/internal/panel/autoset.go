// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
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
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-138, REQ-SDS-139, REQ-SDS-140
func (c *Controller) SetFrameSource(fn func(func(*engine.Frame))) { c.frameFn = fn }

// autoset is the AUTO button: it starts a background sweep that fits the whole
// scope to the live signal. A second AUTO press while it's running CANCELS it
// (the LCD shows "AUTOSET…" with that hint). Multi-frame — measuring at the old
// scale then applying a big scale change never translates (offset/trigger land
// off-screen), so it settles and RE-MEASURES between steps.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) autoset() {
	if c.frameFn == nil { // no frame source: best-effort AUTO/run
		c.norm, c.running = false, true
		c.eng.SetNorm(false)
		c.eng.SetRunning(true)
		c.pushLEDs()
		return
	}
	from := c.markChanges() // the settings before the sweep, for the rows it changes
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
	c.autosetRun++
	c.autosetFrom = from
	ws, view := c.autoTargetLocked()
	c.mu.Unlock()
	c.pushLEDs()
	go c.runAutosetFor(stop, ws, view)
}

// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func (c *Controller) runAutoset(stop chan struct{}) {
	c.mu.Lock()
	ws, view := c.ui.ws, c.viewMode
	c.mu.Unlock()
	c.runAutosetFor(stop, ws, view)
}

// runAutosetFor is the sweep for workspace ws showing view.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224, REQ-SDS-226
func (c *Controller) runAutosetFor(stop chan struct{}, ws wsview.WS, view int) {
	defer c.endAutoset(stop)
	c.syncStream(true) // a stream ends (a page's AUTO while BUS streams): the sweep reads live captures
	g := goalFor(ws, view)
	entry := c.autoEntryNow()
	c.autoClearMatch(entry)
	if ws == wsview.Compare {
		if note, ok := c.compareAuto(); ok {
			c.toast(note, autoNoticeFor)
			return
		}
	}
	res, ok := c.autosetSweep(stop, g)
	if !ok {
		c.cancelledAuto(stop, entry)
		return
	}
	if g.spectrum {
		c.spectrumAuto(stop, &res)
	}
	if g.cursors {
		c.measureCursors()
	}
	c.toast(autoNotice(g, res, c.vdivAt), autoNoticeFor)
}

// autoEntry is what AUTO changes, as it stood when AUTO was pressed: a second
// press puts it back. A cancelled sweep left the scope at its 2 V/div and
// 1us/div, the user's setup gone, and said nothing (in BUS the decoder off
// and the trigger an edge too).
type autoEntry struct {
	tdiv          float64
	vidx          [2]int
	off           [2]float64
	trig          int // the TRIG sheet's type: Serial for an armed serial trigger
	src           int
	rising        bool
	code          uint16
	norm, running bool
	proto, mode   int
	auto          autoWatch
	match         []string // the serial trigger's match: AUTO starts without it
	hasMatch      bool
}

// autoEntryNow reads what AUTO may change.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) autoEntryNow() autoEntry {
	st := c.eng.Snapshot()
	e := autoEntry{tdiv: st.TdivSet(), trig: st.TrigType, src: st.TrigSource, rising: st.TrigRising, code: st.TrigCode,
		norm: st.Norm, running: st.Running}
	if st.SerialMode != 0 {
		e.trig = trigSerial
	}
	if c.fe != nil {
		e.vidx, _ = c.fe.Snapshot()
		e.off = [2]float64{c.fe.OffsetReqV(0), c.fe.OffsetReqV(1)}
	}
	c.mu.Lock()
	e.proto, e.mode, e.auto = c.decProto, c.decMode, c.auto
	c.mu.Unlock()
	if se := c.serialTrig(); se != nil {
		e.match, e.hasMatch = tokensOf(se.SerialConfig()), true
	}
	return e
}

// autoClearMatch starts AUTO from a clean setup: the serial trigger's match
// from before goes (a recalled setup's bytes armed BUS's AUTO on them instead
// of the frame start); a cancelled AUTO puts it back.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-223
func (c *Controller) autoClearMatch(e autoEntry) {
	if e.hasMatch && len(e.match) > 0 {
		_ = c.setMatchTokens(nil)
	}
}

// cancelledAuto puts back what AUTO changed when a second press cancelled it
// (a sweep that found no signal restores the scale itself), and says so.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) cancelledAuto(stop chan struct{}, e autoEntry) {
	select {
	case <-stop:
	default:
		return // not cancelled
	}
	if e.tdiv > 0 {
		c.setTdivNearest(e.tdiv)
	}
	if e.hasMatch {
		_ = c.setMatchTokens(e.match)
	}
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			c.setVdiv(ch, e.vidx[ch])
			c.fe.SetOffset(ch, e.off[ch])
		}
	}
	c.mu.Lock()
	restoreBus := c.decProto != e.proto
	if restoreBus {
		c.decProto, c.decMode, c.auto = e.proto, e.mode, e.auto
		c.decGen++
	}
	c.mu.Unlock()
	if restoreBus {
		c.syncDecodeView(true)
	}
	c.eng.SetTrigSource(e.src)
	c.eng.SetTrigSlope(e.rising)
	if e.code != 0 {
		c.eng.SetTrigLevelCode(e.code)
	}
	if st := c.eng.Snapshot(); e.trig != st.TrigType || (e.trig == trigSerial) != (st.SerialMode != 0) {
		c.setTrigTypeIndex(st, e.trig)
	}
	c.eng.SetNorm(e.norm)
	c.eng.SetRunning(e.running)
	c.mu.Lock()
	c.norm, c.running = e.norm, e.running
	c.mu.Unlock()
	c.toast("AUTO cancelled: the settings are as before", autoNoticeFor)
}

// autoGoal is what AUTO frames the signal for in a workspace: the cycles of
// the trigger source across the screen, the most divisions its swing may
// span, and the slowest timebase it may take.
type autoGoal struct {
	tag      string  // the workspace the notice names ("" in SCOPE)
	cycles   float64 // cycles of the signal across the ten divisions
	atLeast  bool    // cycles is a floor (the fastest timebase holding them), else the nearest
	fill     float64 // the most divisions the swing may span (of the eight)
	maxTdiv  float64 // the slowest display timebase (0: any)
	fitTdiv  float64 // the slowest timebase the vertical is fitted at (0: any)
	spectrum bool    // timed on the FFT source, the marker on its fundamental
	centre   bool    // a rising edge at mid-screen
	cursors  bool    // the cursors on one period and the top and base (measureCursors)
}

// sparseLooks is the captures more the sweep's slowest step reads while it
// sees no signal.
const sparseLooks = 2

// The workspaces' AUTO framings (REQ-SDS-224). SCOPE shows the waveform: two
// cycles, the swing on about six of the eight divisions - at most 7.2, as
// the detents step 1-2-5 and a 3.3 V logic swing, read to a code at the
// sweep's 2 V/div, must land on 500 mV/div every time. MEASURE takes ten
// cycles for its frequency and statistics, SPECTRUM fifty for a fine
// resolution (on a sampled timebase: the envelope bands from 5 ms/div hold
// no spectrum); both fit the swing as large as stays on the screen, the
// finest volts per code. BUS fits the vertical as it always has and then
// frames the bus itself (busAuto); its fit reads at 1 ms/div at the
// slowest: a bus found only at 10 ms/div (an IR remote) has its swing from
// the sweep already, and the fit's captures at 5 ms/div took 6 s.
var (
	goalScope    = autoGoal{cycles: 2, fill: 7.2, centre: true}
	goalMeasure  = autoGoal{tag: "MEASURE", cycles: 10, atLeast: true, fill: 7.5, centre: true, cursors: true}
	goalSpectrum = autoGoal{tag: "SPECTRUM", cycles: 50, atLeast: true, fill: 7.5, maxTdiv: 2e-3, spectrum: true, centre: true}
	goalBus      = autoGoal{cycles: dispCycles, fill: 6, fitTdiv: 1e-3}
)

// measureCursors sets MEASURE's cursors up on the signal AUTO framed, so a
// measurement by hand starts from them: the time cursors on one period from
// the trigger's rising edge (one period before it when the next is off the
// screen), the voltage cursors on the trigger source's top and base. A
// cursor stays where it was when the signal gives no period or level, or
// the screen is zoomed.
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-224
func (c *Controller) measureCursors() {
	m, ok := c.measureChans()
	s := c.snap()
	src := s.st.TrigSource & 1
	if !ok || m[src] == nil {
		return
	}
	r := m[src]
	pos := s.st.TrigPosFrac
	if !(pos > 0 && pos <= 1) {
		pos = 0.5
	}
	screen := 10 * s.st.TdivS
	vd, off := s.vdivTip(src), s.offV[src]*s.probe[src]
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.HasTiming && r.Period > 0 && screen > 0 && s.zoom <= 1 {
		p := r.Period / screen
		a, b := pos, pos+p
		if b > 1 {
			a, b = pos-p, pos
		}
		if a >= 0 && b <= 1 {
			c.curT = [2]float64{(a - pos) * screen, (b - pos) * screen}
		}
	}
	if vd > 0 && r.Vtop > r.Vbase {
		y := func(v float64) float64 { return (4 - (v+off)/vd) / 8 } // cursorVoltAt's inverse
		if ya, yb := y(r.Vtop), y(r.Vbase); ya >= 0 && yb <= 1 {
			c.curV, c.curVCh = [2]float64{r.Vtop, r.Vbase}, src
		}
	}
}

// autoNoticeFor is how long AUTO's notice says what it did.
const autoNoticeFor = 3 * time.Second

// goalFor is the framing AUTO takes in workspace ws on view: SPECTRUM's
// Bode sweeps its own source, so it frames as SCOPE does.
// TRLC-LINKS: REQ-SDS-224
func goalFor(ws wsview.WS, view int) autoGoal {
	switch {
	case ws == wsview.Measure:
		return goalMeasure
	case ws == wsview.Spectrum && view != viewBode:
		return goalSpectrum
	case ws == wsview.Bus:
		return goalBus
	case ws == wsview.Compare:
		g := goalScope
		g.tag = "COMPARE"
		return g
	}
	return goalScope
}

// autoResult is what a sweep fitted: the trigger source, both channels'
// measurements, the display timebase, how many detents the clipping check
// stepped coarser, and SPECTRUM's span (0: not read).
type autoResult struct {
	src    int
	m      [2]*measure.Result
	tdiv   float64
	clip   int
	spanHz float64
}

// autoNotice is the notice line after AUTO: the cycles on the screen and the
// V/div it fitted ("AUTO: 2 cycles, C1 500mV/div"), SPECTRUM's span and
// marker, and a coarser V/div taken for clipping.
// TRLC-LINKS: REQ-SDS-224, REQ-SDS-225
func autoNotice(g autoGoal, r autoResult, vdivAt func(ch int) float64) string {
	head := "AUTO"
	if g.tag != "" {
		head += " (" + g.tag + ")"
	}
	parts := []string{}
	if f := r.m[r.src]; has(f) && f.Freq > 0 && r.tdiv > 0 {
		parts = append(parts, g3(f.Freq*r.tdiv*divX)+" cycles")
	}
	if r.spanHz > 0 {
		parts = append(parts, "span "+si(r.spanHz, "Hz"))
	}
	if g.spectrum && has(r.m[r.src]) && r.m[r.src].Freq > 0 {
		parts = append(parts, "marker "+si(r.m[r.src].Freq, "Hz"))
	} else {
		for ch := range r.m {
			if has(r.m[ch]) {
				parts = append(parts, fmt.Sprintf("C%d %s/div", ch+1, si(vdivAt(ch), "V")))
			}
		}
	}
	if r.clip > 0 {
		parts = append(parts, "coarser: it clipped")
	}
	return head + ": " + strings.Join(parts, ", ")
}

// vdivAt is a channel's V/div at the probe tip.
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) vdivAt(ch int) float64 {
	if c.fe == nil {
		return analog.Detents[analog.BootDetent].VdivV
	}
	idx, _ := c.fe.Snapshot()
	return analog.Detents[idx[ch]].VdivV * c.fe.ProbeFactor(ch)
}

// displayTdiv is the ladder timebase g shows a signal of frequency f on: the
// nearest to g's cycles, or for a floor the fastest holding them, no slower
// than g's slowest.
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) displayTdiv(g autoGoal, f float64) float64 {
	target := g.cycles / f / divX
	tds := slices.Sorted(slices.Values(c.tdivs))
	if len(tds) == 0 {
		return target
	}
	td := target
	if g.atLeast {
		td = tds[len(tds)-1]
		for _, t := range tds {
			if t >= target*0.98 {
				td = t
				break
			}
		}
	}
	if g.maxTdiv > 0 && td > g.maxTdiv {
		td = tds[0]
		for _, t := range tds {
			if t <= g.maxTdiv*1.001 {
				td = t
			}
		}
	}
	return td
}

// spectrumAuto finishes AUTO in SPECTRUM: the marker on the fundamental of
// the FFT source and the span the spectrum reaches at the timebase taken.
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) spectrumAuto(stop chan struct{}, r *autoResult) {
	if f := r.m[r.src]; has(f) && f.Freq > 0 {
		c.mu.Lock()
		c.ui.specF, c.ui.specFSet = f.Freq, true
		c.mu.Unlock()
	}
	if e, ok := c.eng.(interface{ LeaseSpectrumView(time.Duration) }); ok {
		e.LeaseSpectrumView(3 * time.Second)
	}
	if !c.waitFrame(stop) || !c.waitFrame(stop) || c.frameFn == nil {
		return
	}
	c.frameFn(func(f *engine.Frame) {
		if f == nil {
			return
		}
		if sp, ok := framecalc.Spectrum(f, r.src, f.SampleS); ok {
			r.spanHz = sp.SpanHz
		}
	})
}

// autosetSweep is the autoset sweep: the signal found, the vertical fitted
// and centred for g, an edge trigger at the midpoint, g's cycles on screen,
// and no trace left clipped. It reports what it fitted, and false for no
// signal or cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224, REQ-SDS-225
func (c *Controller) autosetSweep(stop chan struct{}, g autoGoal) (autoResult, bool) {
	var res autoResult
	cancelled := func() bool {
		select {
		case <-stop:
			return true
		default:
			return false
		}
	}
	if !c.awaitDetection(stop) {
		return res, false
	}
	// Snapshot the entry scale so a no-signal sweep can restore it instead of
	// abandoning the user on the coarse sweep state.
	entry := c.eng.Snapshot()
	entryTdiv := entry.TdivS
	var entryVidx [2]int
	var entryOff [2]float64
	if c.fe != nil {
		entryVidx, _ = c.fe.Snapshot()
		entryOff[0] = c.fe.OffsetReqV(0)
		entryOff[1] = c.fe.OffsetReqV(1)
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
	c.edgeOnGeneralImage() // the sweep measures on the general image, edge-triggered

	// 1. Coarse vertical first so nothing is railed (a clipped trace measures as
	//    ~0 Vpp and can't be fit), then sweep the timebase fast→slow to find the
	//    signal (an unknown frequency needs a scale where a few cycles are on
	//    screen and native — not decimated — so the frequency reads true).
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			// 2 V/div at the probe tip: ±8 V unclipped. The detents are the
			// BNC's: with a 10x probe 2 V/div there was 20 V/div at the tip,
			// a 3.3 V logic swing four codes, and AUTO found no signal.
			c.setVdiv(ch, nearestDetent(2.0/c.fe.ProbeFactor(ch)))
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
			return res, false
		}
	}
	sweep := []float64{1e-6, 1e-5, 1e-4, 1e-3, 1e-2}
	var found *measure.Result
	var foundCh int
	var swing [2]*measure.Result // each channel's widest swing seen in the sweep
	var timed [2]bool            // a capture showed the channel's frequency
	foundTd := 0.0               // the sweep step the signal was found at
	// A signal under a few codes at 2 V/div (a 3.3 V logic line through a 10x
	// probe whose factor reads 1x, as Default setup leaves it) is looked for
	// again at 200 mV/div at the tip before AUTO says there is none.
	for pass, tip := range []float64{2.0, 0.2} {
		if pass > 0 {
			if c.fe == nil || (found != nil && found.Freq > 0) {
				break
			}
			found, swing, timed = nil, [2]*measure.Result{}, [2]bool{}
			for ch := 0; ch < 2; ch++ {
				c.setVdiv(ch, nearestDetent(tip/c.fe.ProbeFactor(ch)))
				c.fe.SetOffset(ch, 0)
			}
		}
		for _, td := range sweep {
			if cancelled() {
				return res, false
			}
			ss := c.setTdivNearest(td)
			if !c.waitBandFrame(stop, ss) {
				return res, false
			}
			m, _ := c.measureChans()
			// A signal switching a few codes at 2 V/div, with none larger
			// beside it, is timed by the 200 mV/div pass: the steps left here
			// read the same few codes (a 3.3 V bus through a 10x probe set as
			// 1x spent 1.5 s, and 4.5 s with the slowest step's extra looks,
			// before that pass began).
			if pass == 0 && c.fe != nil && !fq2(m) && c.smallActive() {
				break
			}
			// The slowest step reads up to two captures more while it sees
			// nothing: a bus that sends a 23 ms frame every 120 ms (an IR
			// remote) is missed by a 100 ms screen now and then.
			if td == sweep[len(sweep)-1] && !has(m[0]) && !has(m[1]) {
				// whole records, not the roll view's chunks with their gaps
				e, ok := c.eng.(interface{ LeaseSearchView(time.Duration) })
				for k := 0; k < sparseLooks && !has(m[0]) && !has(m[1]); k++ {
					if ok {
						e.LeaseSearchView(2 * time.Second)
					}
					if !c.waitFrame(stop) || !c.waitFrame(stop) {
						return res, false
					}
					m, _ = c.measureChans()
				}
				if ok {
					e.LeaseSearchView(0)
				}
			}
			// every step's capture counts towards the swing: a pulse slower than
			// the screen shows one level in one capture and the other in the next
			for ch := range m {
				timed[ch] = timed[ch] || has(m[ch]) && m[ch].Freq > 0
				m[ch] = widestSwing(m[ch], swing[ch])
				swing[ch] = m[ch]
			}
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
				found, foundTd = m[foundCh], td
				break
			}
		}
	}
	if found == nil || found.Freq <= 0 {
		noSignal() // restore the user's scale + tell them nothing was found
		return res, false
	}

	// 1b. One capture of a long window widens each channel's swing: levels that
	//     change slower than the frequency found never showed in the sweep's
	//     fast captures (an ARINC 429 line without the bandwidth limit, found
	//     on its 50 MHz NULL toggle at 1us/div, read +-0.38 V there, fitted
	//     200mV/div, and its 3.3 V data level ran off the screen).
	if foundTd < swingTdiv {
		ss := c.setTdivNearest(swingTdiv)
		if !c.waitBandFrame(stop, ss) {
			return res, false
		}
		ms, _ := c.measureChans()
		for ch := range ms {
			if has(ms[ch]) {
				timed[ch] = timed[ch] || ms[ch].Freq > 0
				swing[ch] = widestSwing(swing[ch], ms[ch])
			}
		}
	}

	// 2. Fit the VERTICAL from a window holding MANY cycles: a short capture of an
	//    aperiodic/serial signal (UART, a bursty stream) can miss the full swing,
	//    under-reading Vpp — which then picks a too-sensitive V/div that CLIPS.
	//    Measuring over ~30 cycles captures the true min/max regardless of pattern.
	fit := (measCycles / found.Freq) / divX
	if g.fitTdiv > 0 {
		fit = min(fit, g.fitTdiv)
	}
	ss := c.setTdivNearest(fit)
	if !c.waitBandFrame(stop, ss) {
		return res, false
	}
	m, _ := c.measureChans()
	for ch := range m {
		timed[ch] = timed[ch] || has(m[ch]) && m[ch].Freq > 0
		// A bursty bus (FlexRay: two frames each 240 us, idle high between)
		// can leave this capture without its burst: a flat line read as a few
		// tens of mV fitted 100 mV/div and pushed the trace off the screen.
		m[ch] = widestSwing(m[ch], swing[ch])
		swing[ch] = m[ch]
	}
	// A channel that showed no frequency and no more swing than noise is a
	// quiet line, or a pulse slower than every capture so far (a 10 Hz pulse
	// beside a 1 kHz square, seen at one level): fitted, its level went off
	// the screen when the other came. It keeps the sweep's 2 V/div at 0 V,
	// where either level of a logic line shows, and is fitted no further.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if has(m[ch]) && !timed[ch] && swing[ch].Vpp < refineMinVpp {
				c.fe.SetOffset(ch, 0)
				m[ch], swing[ch] = nil, nil
			}
		}
	}
	if !has(m[0]) && !has(m[1]) {
		noSignal()
		return res, false
	}

	// 3. Vertical: fit Vpp into g's divisions, centred on the MIDPOINT
	//    (Vmax+Vmin)/2 — NOT the mean, which for a duty-cycled signal (UART idles
	//    high) skews toward a rail and would push the other rail off-screen.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if !has(m[ch]) {
				continue
			}
			p := c.fe.ProbeFactor(ch)
			c.setVdiv(ch, detentFor(m[ch].Vpp/p, g.fill))
			c.fe.SetOffset(ch, -(m[ch].Vmax+m[ch].Vmin)/2/p)
		}
		if !c.waitFrame(stop) {
			return res, false
		}
	}

	// 3a. Refine: the sweep read the swing at 2 V/div, 80 mV a code, and a
	//     3.3 V logic swing read a code or two wide fitted 1 V/div one time
	//     and 500 mV/div the next. Read again at the fitted V/div, when the
	//     capture holds the swing (a bursty or fast bus's may not), and take
	//     a finer detent that fits.
	if c.fe != nil {
		if _, ok := c.refineVertical(stop, m, swing, g); !ok {
			return res, false
		}
	}

	// 3b. Close the loop on centring: the offset-DAC volts→code model drifts across
	//     detents (up to a couple divisions), so read where the trace ACTUALLY
	//     landed and nudge the offset to bring the mean to screen centre. This is
	//     model-independent — it works off the raw codes. A larger fill leaves
	//     less room above and below, so it centres closer.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if !has(m[ch]) {
				continue
			}
			c.nudgeCentre(ch, swing[ch], g)
		}
		if !c.waitFrame(stop) {
			return res, false
		}
	}

	// 3c. Coarsen off the screen edge: on the sensitive ×1 ranges (V/div ≤ 200 mV,
	//     attenuator bit off) the vertical offset DAC is DEAD — its code moves but
	//     the trace does not — so a small AC riding a large DC lands there (the AC
	//     fits) yet the DC can't be pulled to centre. detentForVpp only sees the AC,
	//     so it has no way to know. That off-screen trace shows up two ways: a hard
	//     ADC RAIL (codes pinned ≤1/≥254), OR — when the large DC SATURATES THE
	//     FRONT-END AMP before the ADC — the trace pins at the screen EDGE (mean
	//     ~251, ~+4.9 div) while the codes never quite reach the rail. So coarsen
	//     while the trace is either railed OR still > 2.5 div off centre after the
	//     offset nudge, OR clipped (a stretch of it past the screen's top or
	//     bottom, which a capture missing part of the swing does not judge by its
	//     centre): step up one detent (coarser) and re-centre. Each step is
	//     model-independent (re-measure Vpp/midpoint at the new V/div, re-run the
	//     midpoint offset + the rawCenterErr nudge from 3b). This walks a DC-heavy
	//     signal onto an ATTENUATED range (V/div ≥ 500 mV) where the offset actually
	//     works. Bounded by the detent count so it can never spin.
	if c.fe != nil {
		for ch := 0; ch < 2; ch++ {
			if !has(m[ch]) {
				continue
			}
			for i := 0; i < len(analog.Detents); i++ {
				clip := c.clipped(ch)
				if !clip && !c.offScreenFor(ch, swing[ch]) {
					break // trace is on-screen AND centred — nothing to coarsen
				}
				stepped, ok := c.coarsen(stop, ch, &swing, g)
				if !ok {
					return res, false
				}
				if !stepped {
					break
				}
				if clip {
					res.clip++
				}
			}
		}
	}

	// 3d. SPECTRUM is timed on the FFT source: a slower signal there than the
	//     one the sweep found shows no frequency at the fit's timebase, so it
	//     is looked for on slower ones.
	if g.spectrum {
		c.mu.Lock()
		fs := c.fftSrc & 1
		c.mu.Unlock()
		if !c.freqOn(stop, fs) {
			return res, false
		}
	}

	// 4. Trigger: re-measure the now-centred trace and put an EDGE trigger at its
	//    midpoint (this is why it must be measured AFTER the vertical settles —
	//    else the level lands off-screen).
	src, ok := c.autoTrigger(stop, &m, swing, g)
	if !ok {
		return res, false
	}
	res.src, res.m = src, m
	if !has(m[src]) {
		return res, true
	}

	// 5. Finally, set the DISPLAY timebase to g's cycles of the (accurately
	//    measured) signal. The trigger level is in volts, so this timebase
	//    change does not disturb it.
	if f := m[src].Freq; f > 0 {
		ss := c.setTdivNearest(c.displayTdiv(g, f))
		res.tdiv = c.eng.Snapshot().TdivSet()
		if !c.waitBandFrame(stop, ss) {
			return res, false
		}
		// 5a. Refine on the screen AUTO leaves: the fit's capture, filtered at
		//     a slower timebase, read a 1 MHz square's ringing (3.8 V of a
		//     3.4 V swing) and fitted 1 V/div when the sweep timed the screen
		//     by the slower signal on the other channel.
		if c.fe != nil {
			moved, ok := c.refineVertical(stop, m, swing, g)
			if !ok {
				return res, false
			}
			for ch := 0; ch < 2 && moved; ch++ {
				if has(m[ch]) && c.nudgeCentre(ch, swing[ch], g) && !c.waitFrame(stop) {
					return res, false
				}
			}
		}
	}

	// 6. The clipping check on the screen AUTO leaves, over a few captures: a
	//    capture at the fit's timebase can miss part of the swing (a bus's rare
	//    level, a burst), so a trace that still runs past the screen's top or
	//    bottom steps one detent coarser and re-centres, at most twice, and the
	//    trigger follows.
	if c.fe != nil {
		again := false
		var steps [2]int // each channel's steps, at most autoClipSteps off the rail
		for look := 0; look < autoClipLooks; look++ {
			if look > 0 && !c.waitFrame(stop) {
				return res, false
			}
			for ch := 0; ch < 2; ch++ {
				if !has(swing[ch]) {
					continue // (a railed trace measures no swing now: the sweep's says it had one)
				}
				// a trace pinned at the ADC's rail says nothing of how far past it
				// the signal goes, and one whose own span is more than the screen
				// cannot fit (an ARINC 429 line left at 200mV/div spanning 9.8
				// div): those step on past the two
				for ; steps[ch] < len(analog.Detents) && c.clipped(ch) && (steps[ch] < autoClipSteps || c.railing(ch) || c.overScreen(ch)); steps[ch]++ {
					fixed, ok := c.recentreClipped(stop, ch, g)
					if !ok {
						return res, false
					}
					if fixed {
						again = true
						break
					}
					stepped, ok := c.coarsen(stop, ch, &swing, g)
					if !ok {
						return res, false
					}
					if !stepped {
						break
					}
					res.clip++
					again = true
				}
			}
		}
		if again {
			src, ok := c.autoTrigger(stop, &m, swing, g)
			if !ok {
				return res, false
			}
			res.src, res.m = src, m
		}
	}
	return res, true
}

// detectionWait bounds how long AUTO waits for a bus detection under way.
const detectionWait = 30 * time.Second

// awaitDetection waits (bounded) for a bus detection under way to end before
// the sweep: pressed during BUS entry's detection, both set the timebase and
// the vertical each for itself (the detection's search at the full rate), and
// the one ending last left its own. It returns false when cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-223
func (c *Controller) awaitDetection(stop chan struct{}) bool {
	for end := time.Now().Add(detectionWait); time.Now().Before(end); {
		c.mu.Lock()
		busy := c.ui.busDetecting
		c.mu.Unlock()
		if !busy {
			return true
		}
		select {
		case <-stop:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}

// freqOn steps the timebase a decade slower at a time, within the sampled
// bands, until channel ch, showing a signal without a frequency, shows its
// frequency. It returns false when cancelled.
// TRLC-LINKS: REQ-SDS-224
func (c *Controller) freqOn(stop chan struct{}, ch int) bool {
	m, _ := c.measureChans()
	td := c.eng.Snapshot().TdivSet()
	for i := 0; i < 3 && has(m[ch]) && !(m[ch].Freq > 0); i++ {
		if td *= 10; td >= 5e-3 {
			break
		}
		if !c.waitBandFrame(stop, c.setTdivNearest(td)) {
			return false
		}
		m, _ = c.measureChans()
	}
	return true
}

// swingTdiv is the timebase of the long window that widens the sweep's swing
// (10 ms of the signal, decimated).
const swingTdiv = 1e-3

// autoClipSteps bounds the clipping check on the final screen, but for a
// trace at the ADC's rail; it looks at autoClipLooks captures, as a bus's
// level can show in one capture and not the next.
const (
	autoClipSteps = 2
	autoClipLooks = 3
)

// refineMinVpp is the least swing autoset refines: four codes of the
// sweep's 2 V/div at the probe tip; less is a quiet line's noise. The capture at the fitted
// V/div must hold refineShare of the swing: a filtered capture of a fast bus
// read its bits smaller (2.9 V of FlexRay's 3.3 V), refined to 500 mV/div,
// and the bus clipped at the full rate.
const (
	refineMinVpp = 4 * 2.0 / 25
	refineShare  = 0.9
)

// refineVertical reads the swing again at the V/div in use, on two captures
// (the smaller reading: one read a 3.4 V square 3.8 V), and takes a finer
// detent that fits g, re-centred, for each channel whose capture holds the
// swing seen: refineShare of its extremes or of its step (an edge's ringing
// on the sweep's capture widened the extremes, not the step). It reports
// whether a channel moved; ok false when cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func (c *Controller) refineVertical(stop chan struct{}, m, swing [2]*measure.Result, g autoGoal) (moved, ok bool) {
	mf, _ := c.measureChans()
	if !c.waitFrame(stop) {
		return false, false
	}
	m2, _ := c.measureChans()
	for ch := range mf {
		if has(m2[ch]) && (!has(mf[ch]) || m2[ch].Vpp < mf[ch].Vpp) {
			mf[ch] = m2[ch]
		}
	}
	step := func(r *measure.Result) float64 { return r.Vtop - r.Vbase }
	for ch := 0; ch < 2; ch++ {
		if !has(m[ch]) || !has(mf[ch]) || !has(swing[ch]) || swing[ch].Vpp < refineMinVpp || c.clipped(ch) ||
			mf[ch].Vpp < refineShare*swing[ch].Vpp && !(step(mf[ch]) >= refineShare*step(swing[ch]) && step(swing[ch]) > 0) {
			continue
		}
		p := c.fe.ProbeFactor(ch)
		snap, _ := c.fe.Snapshot()
		if d := detentFor(refineMargin*mf[ch].Vpp/p, g.fill); d < snap[ch] {
			c.setVdiv(ch, d)
			c.fe.SetOffset(ch, -(mf[ch].Vmax+mf[ch].Vmin)/2/p)
			moved = true
		}
	}
	if moved && !c.waitFrame(stop) {
		return moved, false
	}
	return moved, true
}

// refineMargin is how much room a finer detent must leave: a swing at the
// edge of g's fill (the bench's 100 kHz square, 3.55 V with its overshoot,
// against 3.6 V at 500mV/div) read one capture finer and the next not.
const refineMargin = 1.03

// nudgeCentre moves a channel's offset so the swing it shows sits at the
// screen's centre, read off the raw codes (centreErrOver), when it is off by
// more than g's fill leaves room for.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func (c *Controller) nudgeCentre(ch int, swing *measure.Result, g autoGoal) bool {
	errDiv, full := c.centreErrOver(ch, swing) // +ve => trace sits above centre
	tol := min(0.4, (8-g.fill)/2*0.6)
	if !full || math.Abs(errDiv) <= tol {
		return false
	}
	snap, _ := c.fe.Snapshot()
	vdiv := analog.AnalogVdiv(snap[ch]) // raw-code divisions are on the analog range
	c.fe.SetOffset(ch, c.fe.OffsetReqV(ch)-errDiv*vdiv)
	return true
}

// overScreen reports a channel whose trace spans more than the screen's
// eight divisions (1 % trimmed each end): no offset fits it.
// TRLC-LINKS: REQ-SDS-225
func (c *Controller) overScreen(ch int) bool {
	_, span := c.rawCentre(ch)
	return span > 8*25
}

// recentreClipped centres a clipped trace whose own span fits g's fill: it
// runs past the screen's edge off centre, and centred it fits (an ARINC 429
// line without the bandwidth limit, its NULL a 50 MHz toggle, was left 2 div
// high at 500mV/div after two coarser steps). A trace at the ADC's rail shows
// part of its span: centring that brings the rest in sight, or the step
// coarser that follows does. fixed reports it no longer clips; ok false when
// cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-225
func (c *Controller) recentreClipped(stop chan struct{}, ch int, g autoGoal) (fixed, ok bool) {
	errDiv, span := c.rawCentre(ch)
	if span <= 0 || float64(span) > g.fill*25 || math.Abs(errDiv) < 0.2 {
		return false, true
	}
	snap, _ := c.fe.Snapshot()
	c.fe.SetOffset(ch, c.fe.OffsetReqV(ch)-errDiv*analog.AnalogVdiv(snap[ch]))
	if !c.waitFrame(stop) {
		return false, false
	}
	return !c.clipped(ch), true
}

// coarsen steps a channel one detent coarser and re-centres it there on the
// widest swing seen: the capture that clipped (its rail a bound of the swing)
// and the one at the new V/div widen sw[ch] first, as a capture of a bus
// shows a level the last did not. Then the raw-code nudge. stepped is false
// at the coarsest detent or with the signal gone; ok false when cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-225
func (c *Controller) coarsen(stop chan struct{}, ch int, sw *[2]*measure.Result, g autoGoal) (stepped, ok bool) {
	snap, _ := c.fe.Snapshot()
	if snap[ch] >= len(analog.Detents)-1 {
		return false, true // already the coarsest detent — can't fit it any better
	}
	m0, _ := c.measureChans()
	sw[ch] = widestSwing(m0[ch], sw[ch])
	c.setVdiv(ch, snap[ch]+1) // one detent coarser
	if !c.waitFrame(stop) {
		return false, false
	}
	mc, _ := c.measureChans() // re-measure Vpp/midpoint at the new V/div
	mc[ch] = widestSwing(mc[ch], sw[ch])
	if !has(mc[ch]) {
		return false, true
	}
	sw[ch] = mc[ch]
	p := c.fe.ProbeFactor(ch)
	c.fe.SetOffset(ch, -(mc[ch].Vmax+mc[ch].Vmin)/2/p) // re-centre on midpoint
	if !c.waitFrame(stop) {
		return false, false
	}
	if c.nudgeCentre(ch, sw[ch], g) { // close the loop on centring (as in 3b)
		if !c.waitFrame(stop) {
			return false, false
		}
	}
	return true, true
}

// autoTrigger re-measures the fitted trace and puts an EDGE trigger at its
// midpoint on the source AUTO times the screen by (SPECTRUM: the FFT source
// when it shows a frequency), rising at mid-screen for g.centre, then checks
// the comparator fires there. It returns the source; ok false when cancelled.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func (c *Controller) autoTrigger(stop chan struct{}, m *[2]*measure.Result, swing [2]*measure.Result, g autoGoal) (src int, ok bool) {
	*m, _ = c.measureChans()
	for ch := range m {
		m[ch] = widestSwing(m[ch], swing[ch])
	}
	src = autosetTrigSource(*m)
	if g.spectrum {
		c.mu.Lock()
		fs := c.fftSrc & 1
		c.mu.Unlock()
		if has(m[fs]) && m[fs].Freq > 0 {
			src = fs
		}
	}
	if !has(m[src]) {
		return src, true
	}
	mid := (m[src].Vmax + m[src].Vmin) / 2
	// Per-detent cal maps tip-volts→code correctly at this V/div; fall back to
	// the global fit if no front end. (mid is tip-referred; TrigCode divides by
	// the source probe internally.)
	codeF := 31437 - 911*mid
	if c.fe != nil {
		codeF = c.fe.TrigCode(mid, src)
	}
	code := int(codeF + 0.5)
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
	if g.centre {
		c.eng.SetTrigSlope(true)
		_ = c.setTrigPos(0.5)
	}

	// VERIFY the level actually fires the comparator, closed-loop. The
	// code←volts fit above is exact only at 1–2 V/div; at other detents the
	// comparator's real mapping shifts and the computed code can land outside
	// the signal's band — the display then free-runs untriggered (honest, but
	// not what AUTO promises). Rather than model every detent, probe the
	// hardware: if the comparator isn't firing, scan the code range and centre
	// on the band that fires (WYSIWYG — same discipline as the level recommit).
	if !c.verifyTrigLevel(stop, uint16(code)) {
		return src, false
	}
	select {
	case <-stop:
		return src, false
	default:
	}
	return src, true
}

// trigFiring reports whether the comparator fired on most recent captures.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) trigFiring() bool {
	log, _ := c.eng.AcqLog(4)
	if c.eng.Snapshot().BandKind == "sram" && c.frameFn != nil {
		fired := false
		c.frameFn(func(f *engine.Frame) { fired = f != nil && f.Coherent && f.Trigd })
		return fired
	}
	n := 0
	for _, e := range log {
		if e.SawTrig {
			n++
		}
	}
	return n >= 2 && len(log) >= 2
}

// verifyTrigLevel checks the committed level fires; if not, it scans the DAC
// range and centres the level on the empirically-firing band. Returns false
// only when cancelled; a no-fire-anywhere signal keeps the computed code (a
// quiet or non-repetitive input legitimately never fires — AUTO free-runs).
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) verifyTrigLevel(stop chan struct{}, computed uint16) bool {
	if !c.waitFrame(stop) || !c.waitFrame(stop) {
		return false
	}
	if c.trigFiring() {
		return true // the fit was right at this detent — nothing to do
	}
	var fires []uint16
	for _, code := range c.trigScanCodes() {
		c.eng.SetTrigLevelCode(code)
		if !c.waitFrame(stop) {
			return false
		}
		if c.trigFiring() {
			fires = append(fires, uint16(code))
		}
	}
	pick := computed
	if len(fires) > 0 {
		pick = fires[len(fires)/2] // centre of the firing band
	}
	c.eng.SetTrigLevelCode(pick)
	c.mu.Lock()
	c.trigCode = pick
	c.mu.Unlock()
	return true
}

// waitFrame sleeps ~one publish interval so a frame at the new scale exists,
// returning false if autoset was cancelled during the wait.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) waitFrame(stop chan struct{}) bool {
	// Autoset measures chronological samples. Renew its own lease (evenly
	// sampled frames; a decimated live capture keeps the sweep fast).
	if e, ok := c.eng.(interface{ LeaseSampleView(time.Duration) }); ok {
		e.LeaseSampleView(3 * time.Second)
	}
	seq0 := c.frameSeq()
	select {
	case <-stop:
		return false
	case <-time.After(settleMs * time.Millisecond):
	}
	// A frame published after the change can have been captured before it:
	// measured at the new V/div, it read a wrong midpoint and the offset
	// pushed the trace two divisions off (SPECTRUM draws ~5 frames a
	// second). The second frame published since is captured after it.
	for end := time.Now().Add(freshWait); seq0 != 0 && c.frameSeq() < seq0+2 && time.Now().Before(end); {
		select {
		case <-stop:
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
	return true
}

// freshWait bounds waitFrame's wait for frames published after a change.
const freshWait = time.Second

// frameSeq is the latest frame's publish number (0: none or unnumbered).
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) frameSeq() uint64 {
	var seq uint64
	if c.frameFn != nil {
		c.frameFn(func(f *engine.Frame) {
			if f != nil {
				seq = f.Seq
			}
		})
	}
	return seq
}

// measureChans reads the latest frame and computes each channel's measurement in
// tip-referred volts (using the CURRENT V/div + offset), plus whether it holds
// real signal.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) measureChans() ([2]*measure.Result, bool) {
	st := c.eng.Snapshot()
	var m [2]*measure.Result
	ok := false
	if c.frameFn == nil {
		return m, false
	}
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
			vdiv := analog.AnalogVdiv(idx) // volts per code on the analog range
			q := f.Q1
			if ch == 1 {
				q = f.Q2
			}
			if len(q) >= valid {
				m[ch] = measure.ComputeQ8(q[:valid], vdiv/25*probe, offV*probe, f.SampleS)
			} else {
				m[ch] = measure.Compute(sig, vdiv/25*probe, offV*probe, f.SampleS)
			}
			if f.PeakDetect {
				m[ch] = m[ch].WithEnvelopeTiming(sig, vdiv/25*probe, offV*probe, f.SampleS)
			}
			if f.BandwidthHz > 0 {
				m[ch] = ringFree(m[ch], sig, vdiv/25*probe, offV*probe)
			}
		}
	})
	return m, ok
}

// ringFree is r of a filtered decimated capture without the filter's ringing
// on a two-level signal's edges: the extremes held within ringAllow of the
// step past its top and base. The sweep's captures are filtered, and a 3.3 V
// square read 3.8 V there (the screen's trace, 3.4 V) and fitted 1 V/div. A
// signal with most of its samples elsewhere than its two levels keeps its
// extremes.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func ringFree(r *measure.Result, sig []uint8, vpc, offV float64) *measure.Result {
	if r == nil || !(vpc > 0) {
		return r
	}
	a := r.Vtop - r.Vbase
	if !twoLevel(r, sig, vpc, offV) {
		return r
	}
	w := *r
	w.Vmax, w.Vmin = min(r.Vmax, r.Vtop+ringAllow*a), max(r.Vmin, r.Vbase-ringAllow*a)
	w.Vpp = w.Vmax - w.Vmin
	return &w
}

// twoLevel reports whether r, measured on sig, is a two-level signal: most
// of its samples at its top or its base.
// TRLC-LINKS: REQ-SDS-224, REQ-SDS-225
func twoLevel(r *measure.Result, sig []uint8, vpc, offV float64) bool {
	if r == nil || !(r.Vtop > r.Vbase) || !(vpc > 0) || len(sig) == 0 {
		return false
	}
	a := r.Vtop - r.Vbase
	code := func(v float64) float64 { return (v+offV)/vpc + 128 }
	top, base, band := code(r.Vtop), code(r.Vbase), max(levelBand*a/vpc, 1)
	n := 0
	for _, v := range sig {
		if fv := float64(v); math.Abs(fv-top) <= band || math.Abs(fv-base) <= band {
			n++
		}
	}
	return float64(n) >= twoLevelShare*float64(len(sig))
}

// A two-level signal (twoLevel) holds twoLevelShare of its samples within
// levelBand of its step at its top or base (a sine about 30 %, a square
// ringing on a hundred edges a screen more than half); its fit allows
// ringAllow of the step past each (a fast edge's own overshoot on the
// screen's trace).
const (
	levelBand     = 0.05
	twoLevelShare = 0.5
	ringAllow     = 0.02
)

// rawCenterErr reports how far a channel's signal MIDPOINT ((min+max)/2) sits from
// screen centre, in divisions (+ve = above centre), read straight off the ADC codes
// (128 = centre, 25 codes/div). Uses 1%-trimmed rails so a stray spike can't skew it,
// and the midpoint (not the mean) so duty cycle doesn't matter. Model-independent, so
// it corrects any offset-DAC calibration drift.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) rawCenterErr(ch int) float64 {
	errDiv, _ := c.rawCentre(ch)
	return errDiv
}

// rawCentre is rawCenterErr and the trimmed span (codes) it was read over.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) rawCentre(ch int) (errDiv float64, span int) {
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
		errDiv, span = (float64(lo+hi)/2-128)/25, hi-lo
	})
	return errDiv, span
}

// centreErrOver is rawCenterErr when the latest capture holds most of swing,
// the channel's widest swing seen (volts): a capture of a bursty bus's idle
// line alone reads its rail as the midpoint, and centring on it put the
// burst's other rail near the screen's edge. full is false for such a
// capture, whose midpoint says nothing.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) centreErrOver(ch int, swing *measure.Result) (errDiv float64, full bool) {
	errDiv, span := c.rawCentre(ch)
	if !has(swing) || c.fe == nil {
		return errDiv, true
	}
	snap, _ := c.fe.Snapshot()
	volts := float64(span) * analog.AnalogVdiv(snap[ch]) / 25 * c.fe.ProbeFactor(ch)
	return errDiv, volts >= 0.7*swing.Vpp
}

// railing reports whether a channel's trace is clipped against a rail: more than
// ~5% of its valid samples pinned at an extreme code (≤1 or ≥254). A DC-heavy
// signal parked on a sensitive ×1 range (V/div ≤ 200 mV, where the offset DAC is
// dead) stays railed even after centring; the 3c guard uses this to coarsen onto
// an attenuated range where the offset works. Model-independent — reads raw ADC
// codes straight off the latest frame, so it needs no calibration model.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) railing(ch int) bool {
	railed := false
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv {
			return // an envelope frame's min/max bands aren't per-sample rails
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
		n := 0
		for _, v := range sig[:valid] {
			if v <= 1 || v >= 254 {
				n++
			}
		}
		railed = n*20 > valid // >5% pinned at a rail
	})
	return railed
}

// Clipping (autoset steps 3c and 6): a stretch of at least clipRun samples
// past the screen's top or bottom (more than clipMargin codes beyond the
// eight divisions, 100 codes either side of 128): the trace pinned at the
// ADC or the screen's edge. A ringing edge's sample or two past it is not.
const (
	clipRun    = 4
	clipMargin = 2
)

// clipped reports whether a channel's latest sampled capture runs past the
// screen's top or bottom for a stretch (clipRun). Unlike offScreen it needs
// no judgement of the centre, so a capture holding one level of the swing
// (a bus's rare level at the ADC's floor) still reads as clipped. Envelope
// frames are not judged.
// TRLC-LINKS: REQ-SDS-225
func (c *Controller) clipped(ch int) bool {
	clip := false
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv {
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
		if f.BandwidthHz > 0 {
			clip = clipFiltered(sig[:valid])
			return
		}
		clip = clipStretch(sig[:valid])
	})
	return clip
}

// The screen's codes, clipMargin past its eight divisions.
const (
	clipLo = 128 - 100 - clipMargin
	clipHi = 128 + 100 + clipMargin
)

// clipStretch reports whether codes hold a run of clipRun samples past the
// screen's top or bottom.
// TRLC-LINKS: REQ-SDS-225
func clipStretch(codes []uint8) bool {
	return runOf(codes, func(v uint8) bool { return v < clipLo || v > clipHi })
}

// clipFiltered is clipStretch for a filtered decimated capture, whose filter
// rings past a square's levels on every edge (a square on 7.5 divisions rang
// past the screen at 10us/div while the screen's trace held inside it): a
// two-level signal is clipped when a level itself lies past the screen, any
// signal when a stretch of it is at the ADC's rail, and any other signal by
// clipStretch.
// TRLC-LINKS: REQ-SDS-225
func clipFiltered(codes []uint8) bool {
	if runOf(codes, func(v uint8) bool { return v <= 1 || v >= 254 }) {
		return true
	}
	r := measure.Compute(codes, 1, -128, 1) // in codes
	if twoLevel(r, codes, 1, -128) {
		return r.Vtop > clipHi || r.Vbase < clipLo
	}
	return clipStretch(codes)
}

// runOf reports whether codes hold a run of clipRun samples that out holds.
// TRLC-LINKS: REQ-SDS-225
func runOf(codes []uint8, out func(uint8) bool) bool {
	run := 0
	for _, v := range codes {
		if out(v) {
			if run++; run >= clipRun {
				return true
			}
			continue
		}
		run = 0
	}
	return false
}

// offScreen reports whether a channel's trace still sits off-screen after
// centring, so the 3c guard must coarsen it onto a range where the offset works.
// It catches BOTH ways a DC-heavy signal escapes a sensitive range: a hard ADC
// rail (railing), and front-end-amp SATURATION — where the trace pins at the
// screen edge (raw-code midpoint ~251, i.e. > 2.5 div off centre) yet the codes
// never reach the ADC rail. rawCenterErr is the same model-independent raw-code
// midpoint read used by step 3b (+ve = above centre), so a legitimately centred
// full-screen signal reads ≈0 and never false-coarsens.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) offScreen(ch int) bool { return c.offScreenFor(ch, nil) }

// offScreenFor is offScreen judging the centre only on a capture that holds
// most of swing (centreErrOver); a rail is off-screen in any capture.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) offScreenFor(ch int, swing *measure.Result) bool {
	if c.railing(ch) {
		return true
	}
	errDiv, full := c.centreErrOver(ch, swing)
	return full && math.Abs(errDiv) > 2.5
}

// TRLC-LINKS: REQ-SDS-137
func has(r *measure.Result) bool { return r != nil && r.Vpp > 0.02 }

// fq2 reports a capture that timed either channel's frequency.
// TRLC-LINKS: REQ-SDS-137
func fq2(m [2]*measure.Result) bool {
	return has(m[0]) && m[0].Freq > 0 || has(m[1]) && m[1].Freq > 0
}

// Codes a line's swing spans for smallActive: from smallActiveMin (a 3.3 V
// logic line through a 10x probe set as 1x spans four at 2 V/div, a quiet
// line's noise two) to smallActiveMax, over which a signal is timed at the
// coarse scale (and could clip at the fine one).
const (
	smallActiveMin = 4
	smallActiveMax = 8
)

// smallActive reports a capture with a line switching across a swing of
// smallActiveMin to smallActiveMax codes - crossing its midpoint with a code
// of hysteresis at least four times - and no line swinging wider.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) smallActive() bool {
	if c.frameFn == nil {
		return false
	}
	small, wide := false, false
	c.frameFn(func(f *engine.Frame) {
		if f == nil || f.IsEnv || f.Valid < 64 {
			return
		}
		for _, codes := range [2][]uint8{f.C1, f.C2} {
			if len(codes) < f.Valid {
				continue
			}
			sw, x := codeSwing(codes[:f.Valid])
			switch {
			case sw > smallActiveMax:
				wide = true
			case sw >= smallActiveMin && x >= 4:
				small = true
			}
		}
	})
	return small && !wide
}

// codeSwing is a line's swing in codes (its 1st to 99th percentile) and how
// often it crosses the middle of that swing by more than a code either way.
// TRLC-LINKS: REQ-SDS-137
func codeSwing(codes []uint8) (swing, crossings int) {
	var h [256]int
	for _, v := range codes {
		h[v]++
	}
	lo, hi := codePercentile(&h, len(codes), 0.01), codePercentile(&h, len(codes), 0.99)
	mid2 := lo + hi // twice the midpoint
	state := -1
	for _, v := range codes {
		switch {
		case 2*int(v) > mid2+2:
			if state == 0 {
				crossings++
			}
			state = 1
		case 2*int(v) < mid2-2:
			if state == 1 {
				crossings++
			}
			state = 0
		}
	}
	return hi - lo, crossings
}

// widestSwing is r with its extremes widened to those of seen, an earlier
// capture of the same channel in volts (tip-referred, so a V/div change since
// does not matter), and its step (top to base) the wider of the two: the
// vertical fit and the trigger midpoint then hold the signal's whole swing
// although the latest capture missed a burst. Timing stays r's.
// TRLC-LINKS: REQ-SDS-137
func widestSwing(r, seen *measure.Result) *measure.Result {
	if !has(seen) {
		return r
	}
	if !has(r) {
		w := *seen
		return &w
	}
	w := *r
	w.Vmax, w.Vmin = math.Max(r.Vmax, seen.Vmax), math.Min(r.Vmin, seen.Vmin)
	w.Vpp = w.Vmax - w.Vmin
	if seen.Vtop-seen.Vbase > r.Vtop-r.Vbase {
		w.Vtop, w.Vbase = seen.Vtop, seen.Vbase
	}
	return &w
}

// TRLC-LINKS: REQ-SDS-137
func strongerCh(m [2]*measure.Result) int {
	if has(m[1]) && (!has(m[0]) || m[1].Vpp > m[0].Vpp) {
		return 1
	}
	return 0
}

// autosetTrigSource is the channel AUTO triggers on: the stronger one, unless
// only the other's repetition is resolved at the fitted timebase - a stronger
// channel without a frequency there (a 10 Hz pulse beside a 1 kHz square)
// fires on few screens, and the display timebase follows the trigger source.
// TRLC-LINKS: REQ-SDS-137
func autosetTrigSource(m [2]*measure.Result) int {
	src := strongerCh(m)
	hasFreq := func(r *measure.Result) bool { return has(r) && r.Freq > 0 }
	if !hasFreq(m[src]) && hasFreq(m[1-src]) {
		src = 1 - src
	}
	return src
}

// AutosetBusy reports whether an autoset sweep is in progress (for the LCD hint).
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) AutosetBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.autosetBusy
}

// setVdiv applies a vertical detent and keeps the shadow in sync (guarded).
// TRLC-LINKS: REQ-SDS-137
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
// TRLC-LINKS: REQ-SDS-137
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
// TRLC-LINKS: REQ-SDS-137
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
			if c.eng.Snapshot().BandKind == "sram" {
				c.mu.Lock()
				target := c.tdivs[c.tdivIdx]
				c.mu.Unlock()
				matched = f.TdivS > 0 && math.Abs(f.TdivS-target) <= target*0.01
				return
			}
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

// detentForVpp is detentFor at six divisions.
// TRLC-LINKS: REQ-SDS-137
func detentForVpp(vpp float64) int { return detentFor(vpp, 6) }

// detentFor picks the most sensitive V/div whose 8-division window still
// fits Vpp within fill divisions — so autoset never lands on a range that
// clips (a clipped trace measures a too-small Vpp, which would pick an even
// more sensitive range: a trap). Detents are ascending, so the first that
// fits wins.
// TRLC-LINKS: REQ-SDS-137, REQ-SDS-224
func detentFor(vpp, fill float64) int {
	for i := range analog.Detents {
		if vpp/analog.Detents[i].VdivV <= fill {
			return i
		}
	}
	return len(analog.Detents) - 1
}

// nearestDetent returns the vertical detent index whose V/div is nearest the
// (electrical, pre-probe) target.
// TRLC-LINKS: REQ-SDS-137
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

// trigScanCodes is verifyTrigLevel's scan: ten levels across the trigger
// source's screen (-4.5 to +4.5 div about the offset). Ten steps across the
// fixed 27000-35000 code window were 0.88 V apart, 88 div at 10 mV/div.
// TRLC-LINKS: REQ-SDS-137
func (c *Controller) trigScanCodes() []uint16 {
	src := c.eng.Snapshot().TrigSource & 1
	vdiv, off, probe := 1.0, 0.0, 1.0
	if c.fe != nil {
		idx, _ := c.fe.Snapshot()
		vdiv, off, probe = analog.Detents[idx[src]].VdivV, c.fe.OffsetReqV(src), c.fe.ProbeFactor(src)
	}
	codes := make([]uint16, 0, 10)
	for i := range 10 {
		v := (float64(i)-4.5)*vdiv - off // BNC volts at this screen height
		codeF := 31437 - 911*v
		if c.fe != nil {
			codeF = c.fe.TrigCode(v*probe, src)
		}
		codes = append(codes, uint16(min(max(math.Round(codeF), engine.TrigCodeMin), engine.TrigCodeMax)))
	}
	return codes
}
