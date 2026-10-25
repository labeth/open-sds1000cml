// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"fmt"
	"math"
	"sort"
	"time"

	"open-sds/app/internal/analog"
	"open-sds/app/internal/decode"
	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
	"open-sds/app/internal/panel/framecalc"
	"open-sds/app/internal/panel/wsview"
)

// Per-frame analysis for the workspace panes. The render paths call OnFrame
// with each published frame; it runs at most once per frame (by Seq and
// Content) and only for the active workspace, so a redraw never repeats the
// heavy part (decode, FFT, eye fold, measurements, the repeat test).

// geom is where a frame's samples sit on the screen.
type geom struct {
	valid    int
	winCols  int
	edgeX    float64
	sampleS  float64
	tdivS    float64
	peak     bool    // a peak-detect readout: sampleS is a min/max point's span
	captureS float64 // the converters' sample interval behind it
}

// anaState is the analysis the panes show (guarded by Controller.amu).
type anaState struct {
	key  frameKey // the frame analysed last, and for what
	need needKey
	g    geom

	// BUS
	res     decode.Result // the frame's decode
	resOK   bool
	live    []Packet // this frame's packets (complete and cut)
	list    PacketList
	listKey string
	rawKey  string   // the bus settings whose live view was too coarse: sampled frames leased (busRawFrames)
	rawNo   string   // the bus settings whose sampled records were too coarse as well
	hits    busHits  // the rows the match tokens hit, for the caption
	marks   busMarks // the strip's marks of live
	// BUS eye
	eye    *Eye
	eyeKey string
	eyeRes EyeResult
	eyeWhy string
	eyeCh  int
	eyeOK  bool
	eyeFed frameKey  // the frame folded last (each is folded once)
	eyeOff int       // captures in a row off the folded bit period
	eyeAt  time.Time // when the eye last folded a full-rate capture
	// specLease: SPECTRUM's FFT or spectrogram holds the spectrum lease; the
	// first frame after it is left gives it back (its decimated frames are
	// filtered: MEASURE's statistics took them for 3 s after)
	specLease bool

	// SPECTRUM
	specOK   bool
	spec     framecalc.Spec
	specDBV  []float64 // per bin, dBV (rms)
	specCh   int
	specVPC  float64 // the volts per code specDBV was read at
	peaks    []Peak
	thd      THD
	floorDBV float64

	// MEASURE
	stats    map[measItem]*Stats
	statLast map[measItem]float64
	statKey  string
	statN    int
	statAt   time.Time    // the last reading counted
	statFed  frameKey     // the frame counted last
	statEdge uint64       // the edge-window read counted last (Frame.EdgeRead)
	allVals  [2][]float64 // MEASURE all values: per channel, per parameter
	allOK    [2]bool
	allWin   [2]map[int]bool // the parameters taken from edge windows (carried between reads)
	curV     [2][2]float64   // each channel's volts at the time cursors A and B
	curOK    [2]bool

	// COMPARE
	stabPrev []float64
	stabRes  []bool
	stabKey  stabK
	stabFed  frameKey  // the frame tested last
	stabAt   time.Time // when it was tested
	refDiff  [2]refDiffResult
}

// refDiffResult is a reference against the live trace.
type refDiffResult struct {
	ok            bool
	why           string
	max, rms, pct float64 // volts, volts, percent within 0.5 div
}

// stabN is how many captures the repeat verdict looks back over.
const (
	stabN   = 12
	stabMin = 6
)

// stabEvery is the least time between two repeat tests: a capture against
// one about this much older. Testing every capture cost a millisecond of
// each frame at 60 fps for a verdict read when a master starts.
const stabEvery = 60 * time.Millisecond

// frameKey names a published frame; the zero key (a hand-built frame) is
// never taken as seen.
type frameKey struct{ seq, content uint64 }

// keyOf is f's key.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func keyOf(f *engine.Frame) frameKey { return frameKey{f.Seq, f.Content} }

// seen reports whether k is the key last recorded in *last, recording it.
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func seen(last *frameKey, k frameKey) bool {
	if k != (frameKey{}) && k == *last {
		return true
	}
	*last = k
	return false
}

// needKey is what the panes analyse: when it changes, the frame on screen
// is analysed again (a stopped scope has no next frame).
type needKey struct {
	ws           wsview.WS
	measTab, eye bool
	refs         [2]RefTag // a reference stored, shown or cleared while stopped is compared at once
}

// need reads the needKey (c.mu).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (c *Controller) need() needKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := needKey{ws: c.ui.ws, measTab: c.ui.open && sheetTabs[c.ui.tab].id == "meas", eye: c.ui.busEye}
	for i, r := range c.refs {
		k.refs[i] = RefTag{Has: r.has, Show: r.has && r.show, Gen: r.gen}
	}
	return k
}

// OnFrame analyses a newly published frame for the active workspace. It is
// idempotent per frame (the accumulating figures take a frame once) and
// cheap when nothing needs it. It must not read frames through the frame
// source (the caller holds the fan-out lock).
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-215, REQ-SDS-216, REQ-SDS-217, REQ-SDS-218
func (c *Controller) OnFrame(f *engine.Frame) {
	if f == nil || len(f.C1) == 0 {
		return
	}
	k, need := keyOf(f), c.need()
	c.amu.Lock()
	if k != (frameKey{}) && k == c.ana.key && need == c.ana.need {
		c.amu.Unlock()
		return
	}
	c.amu.Unlock()
	s := c.snap()
	var finish bool
	c.amu.Lock()
	a := &c.ana
	if k != (frameKey{}) && k == a.key && need == a.need {
		c.amu.Unlock() // another render path (a screenshot) analysed it meanwhile
		return
	}
	a.key, a.need = k, need
	a.g = geom{valid: framecalc.Valid(f), winCols: f.WinCols, edgeX: f.EdgeX, sampleS: f.SampleS, tdivS: f.TdivS,
		peak: f.PeakDetect, captureS: f.CaptureSampleS}
	if g := a.g.valid; g > 0 { // the cursors' zoomed screen: its pan is a fraction of the record
		c.curRecords.Store(math.Float64bits(float64(g) / float64(max(min(f.WinCols, g), 1))))
	}
	switch s.ui.ws {
	case wsview.Bus:
		c.busFrame(a, s, f)
		if s.ui.busEye {
			c.eyeFrame(a, s, f)
		}
	case wsview.Spectrum:
		c.specFrame(a, s, f)
	case wsview.Measure:
		c.measFrame(a, s, f)
	case wsview.Compare:
		c.refDiffFrame(a, s, f)
	}
	// An armed SPI trigger reads its data at the delay measured on the eye:
	// on Auto with none measured yet (after a restart, or a new bus) it read
	// a fast bus's data on its edges, matched nothing and never triggered
	// while BUS was not shown.
	if s.ui.ws != wsview.Bus && s.st.SerialMode != 0 && s.decDelayAuto {
		c.noteSPIEye(s, f)
	}
	// whether the trigger source repeats, for COMPARE and the web's COMPARE
	// alike (400 columns a frame: cheap enough to keep up everywhere)
	c.stabFrame(a, s, f)
	if measTabOpen(s) && s.ui.ws != wsview.Measure && !f.IsEnv {
		measVals(a, s, f, s.chDisp) // the MEAS tab's live values
	}
	if a.specLease && !(s.ui.ws == wsview.Spectrum && (s.viewMode == viewFFT || s.viewMode == viewSpectrogram)) {
		a.specLease = false
		if e, ok := c.eng.(interface{ LeaseSpectrumView(time.Duration) }); ok {
			e.LeaseSpectrumView(0)
		}
	}
	c.amu.Unlock()
	c.watchBus(s, f)
	c.noteTdivCap(s)
	if s.stacking {
		finish = c.stackProgress(f)
	}
	if finish {
		c.stackFinish(f)
	} else if !s.stacking {
		c.masterMaskFrame(f)
	}
}

// noteTdivCap says when the loaded image stepped the timebase down to the
// slowest whose screen its record fills, with no key turned (a serial
// trigger on the packet or line image armed at 500us/div: the scope went to
// 200us/div by itself), and keeps the S/DIV knob's place there.
// TRLC-LINKS: REQ-SDS-212
func (c *Controller) noteTdivCap(s *sv) {
	most := s.st.TdivMaxS
	if !(most > 0) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tdivIdx >= len(c.tdivs) || c.tdivs[c.tdivIdx] <= most*(1+1e-6) {
		return
	}
	for c.tdivIdx > 0 && c.tdivs[c.tdivIdx] > most*(1+1e-6) {
		c.tdivIdx--
	}
	c.toastLocked(fmt.Sprintf("%s/div at most on this trigger: its image records %s", si(c.tdivs[c.tdivIdx], "s"), si(max(s.st.RecordS, 10*most), "s")), 3*time.Second)
}

// decodeParams are the panel's decode settings, as the LCD's strip takes
// them from the Display (framecalc.Params).
// TRLC-LINKS: REQ-SDS-214
func decodeParamsOf(s *sv) framecalc.DecodeParams {
	p := framecalc.Params(s.decProto, s.decBaud, s.decChA, s.decChB, s.decCPOL, s.decCPHA, s.decLSB, s.decFormat, s.spiDelayNs(), s.decParity)
	if s.decProto == framecalc.ProtoUART { // the framing detection or the S.BUS layer set (8N1 by default)
		p.Parity, p.Inverted = s.decParity, s.decInv
	}
	return p
}

// codesOf returns a channel's valid samples as codes (Q8.8 when the frame
// carries them), with the display coupling applied.
// TRLC-LINKS: REQ-SDS-215
func codesOf(f *engine.Frame, ch, lo, hi int, cpl int) []float64 {
	valid := framecalc.Valid(f)
	lo, hi = max(lo, 0), min(hi, valid)
	if hi <= lo {
		return nil
	}
	sig := framecalc.Channel(f, ch, valid)
	if cpl != analog.CplDC {
		sig = analog.CoupleDisplay(sig, cpl)
	}
	q := f.Q1
	if ch == 1 {
		q = f.Q2
	}
	out := make([]float64, hi-lo)
	if cpl == analog.CplDC && len(q) >= valid {
		for i, v := range q[lo:hi] {
			out[i] = float64(v) / 256
		}
		return out
	}
	for i, v := range sig[lo:hi] {
		out[i] = float64(v)
	}
	return out
}

// window is the sample span the screen shows: [left, left+win) of the frame
// (the zoom window when zoomed), as the LCD's trace mapping.
// TRLC-LINKS: REQ-SDS-214
func windowOf(g geom, s *sv) (left float64, win int) {
	valid := g.valid
	win = g.winCols
	if win <= 0 || win > valid {
		win = valid
	}
	xc := g.edgeX
	if xc < 0 {
		xc = float64(valid) / 2
	}
	pos := s.st.TrigPosFrac
	if !(pos >= 0 && pos <= 1) {
		pos = 0.5
	}
	if s.zoom > 1 {
		win = max(win/s.zoom, 8)
		xc += s.zoomOff * float64(valid)
		pos = 0.5
	}
	return xc - float64(win)*pos, win
}

// voltsPerCode is a channel's volts per display code at the probe tip.
// TRLC-LINKS: REQ-SDS-216, REQ-SDS-217
func voltsPerCode(s *sv, ch int) float64 {
	d := analog.Detents[s.vidx[ch&1]]
	return d.VdivV * float64(max(d.Zoom, 1)) / 25 * s.probe[ch&1]
}

// measureCh measures one channel of the frame as the LCD's readouts do:
// calibrated volts at the tip, the display coupling, the invert.
// TRLC-LINKS: REQ-SDS-217
func measureCh(f *engine.Frame, s *sv, ch int) *measure.Result {
	valid := framecalc.Valid(f)
	if ch == 1 && len(f.C2) < valid {
		return nil
	}
	sig := framecalc.Channel(f, ch, valid)
	off := 0.0
	if s.fe {
		off = s.offV[ch]
	}
	cpl := s.cpl[ch]
	if cpl != analog.CplDC {
		sig = analog.CoupleDisplay(sig, cpl)
		off = 0
	}
	q := f.Q1
	if ch == 1 {
		q = f.Q2
	}
	if len(q) < valid {
		q = nil
	} else {
		q = q[:valid]
	}
	vpc := voltsPerCode(s, ch)
	if a, b := f.MeasureRange(); b-a < len(sig) { // a roll view's captured stretch
		sig = sig[a:b]
		if len(q) >= b {
			q = q[a:b]
		} else {
			q = nil
		}
	}
	m := measure.ComputeAcquisition(sig, q, vpc, off*s.probe[ch], f.SampleS, cpl, f.FilterGuard)
	if f.PeakDetect && m != nil {
		m = m.WithEnvelopeTiming(sig, vpc, off*s.probe[ch], f.SampleS).WithEdgeDetail(vpc, off*s.probe[ch], f.EdgeWindows(ch)...)
	}
	// After the timing: the invert swaps rise and fall and complements the
	// duty, which the envelope's timing would otherwise put back.
	if s.hasInv && s.inv[ch] {
		m = m.Inverted()
	}
	return m
}

// measValue is one parameter of a measurement (NaN when it has none).
// TRLC-LINKS: REQ-SDS-217
func measValue(m *measure.Result, p int) float64 {
	if m == nil {
		return math.NaN()
	}
	timing := func(v float64) float64 {
		if !m.HasTiming {
			return math.NaN()
		}
		return v
	}
	pos := func(v float64) float64 {
		if v > 0 {
			return v
		}
		return math.NaN()
	}
	switch measParams[p].id {
	case "freq":
		return timing(m.Freq)
	case "pkpk":
		return m.Vpp
	case "period":
		return timing(m.Period)
	case "duty":
		return timing(m.Duty)
	case "rise":
		return pos(m.RiseS)
	case "fall":
		return pos(m.FallS)
	case "pwid":
		return pos(m.PosWidthS)
	case "nwid":
		return pos(m.NegWidthS)
	case "ampl":
		return m.Vampl
	case "max":
		return m.Vmax
	case "min":
		return m.Vmin
	case "top":
		return m.Vtop
	case "base":
		return m.Vbase
	case "mean", "rms", "acrms":
		if m.EnvAliased { // an envelope of a signal faster than its buckets: unknown
			return math.NaN()
		}
		switch measParams[p].id {
		case "mean":
			return m.Vmean
		case "rms":
			return m.Vrms
		}
		return m.VrmsAC
	case "ovsh", "prsh":
		if m.EnvTiming && !m.EdgeShape { // an envelope alone holds no edge shape: unknown, not 0 % (the web shows it so)
			return math.NaN()
		}
		if measParams[p].id == "prsh" {
			return timing(m.Preshoot)
		}
		return timing(m.Overshoot)
	}
	return math.NaN()
}

// fmtMeas formats a parameter's value in its unit.
// TRLC-LINKS: REQ-SDS-217
func fmtMeas(p int, v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "---"
	}
	switch measParams[p].unit {
	case "%":
		return g3(v) + "%"
	default:
		return si(v, measParams[p].unit)
	}
}

// median is the median of xs (NaN for none).
// TRLC-LINKS: REQ-SDS-216
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	for _, v := range s {
		if v != v { // NaN: sorting's order (NaN first) decides
			sort.Float64s(s)
			return s[len(s)/2]
		}
	}
	return nthFloat(s, len(s)/2)
}

// nthFloat returns the value sorting would put at x[k] (Hoare selection,
// reordering x; no NaN): the spectrum's noise floor is the median of its
// bins each frame, and a sort of them was most of its cost.
// TRLC-LINKS: REQ-SDS-216
func nthFloat(x []float64, k int) float64 {
	lo, hi := 0, len(x)-1
	for lo < hi {
		p := x[lo+(hi-lo)/2]
		i, j := lo, hi
		for i <= j {
			for x[i] < p {
				i++
			}
			for x[j] > p {
				j--
			}
			if i <= j {
				x[i], x[j] = x[j], x[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j
		case k >= i:
			lo = i
		default:
			return x[k]
		}
	}
	return x[k]
}
