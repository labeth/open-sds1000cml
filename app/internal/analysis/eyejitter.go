// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// A port of the web eye-diagram and jitter engine (internal/web/eyejitter.js
// and eyejitter_analysis.js), kept step for step so the LCD and the browser
// report the same figures for the same record; TestEyeJitterJSParity runs both
// on shared vectors. The JavaScript names are given in each doc comment.
//
// Pipeline per raw record:
//
//	edges: hysteresis mid-level crossings, sub-sample by linear interpolation
//	CDR:   UI estimate by interval clustering, then a 2-pass least-squares fit
//	       of the edge times to an ideal grid t = t0 + n*UI
//	TIE:   each edge's distance from that grid: exact running statistics, a
//	       decimated buffer for RJ/DJ, a per-record spectrum on the UI grid
//	eye:   every sample folded at (t - t0) mod 2UI into a density map
//
// A record that does not lock (too few edges, an inconsistent UI, a residual
// too large for one bit grid) is rejected, never guessed at.

import (
	"math"
	"sort"
	"strconv"
)

// EdgeSet holds the mid-level crossings of one record (ejEdges): sub-sample
// positions T in samples, their polarity Pol (+1 rising, -1 falling), the two
// population levels Lo and Hi in codes, and the threshold Mid between them.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type EdgeSet struct {
	T      []float64
	Pol    []int
	Lo, Hi int
	Mid    float64
}

// FindEdges extracts every crossing of the mid level, with hysteresis of 15 %
// of the swing and sub-sample linear interpolation (ejEdges). The levels are
// the two dominant histogram modes, whatever the duty cycle. It returns nil
// when the record has no serial swing or fewer than three crossings.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func FindEdges(sig []float64) *EdgeSet {
	n := len(sig)
	if n < 2 {
		return nil
	}
	var hist [256]float64
	stride := max(1, n>>13)
	for i := 0; i < n; i += stride {
		if v := sig[i]; v == v { // Math.round(NaN) indexes nothing in a typed array
			hist[histCode(v)]++
		}
	}
	// light smoothing so single-code noise spikes do not win the mode
	var sm [256]float64
	for c := 0; c < 256; c++ {
		a, b := 0.0, 0.0
		if c > 0 {
			a = hist[c-1]
		}
		if c < 255 {
			b = hist[c+1]
		}
		sm[c] = a + 2*hist[c] + b
	}
	m1 := 0
	for c := 1; c < 256; c++ {
		if sm[c] > sm[m1] {
			m1 = c
		}
	}
	m2 := -1
	for c := 0; c < 256; c++ {
		if c-m1 < 24 && m1-c < 24 {
			continue // must be a distinct level
		}
		if m2 < 0 || sm[c] > sm[m2] {
			m2 = c
		}
	}
	if m2 < 0 || sm[m2] < 4 {
		return nil // a single-level record: no serial swing
	}
	lo, hi := min(m1, m2), max(m1, m2)
	if hi-lo < 24 {
		return nil
	}
	mid := float64(lo+hi) / 2
	hys := float64(hi-lo) * 0.15
	up, down := mid+hys, mid-hys
	var t []float64
	var pol []int
	state := -1
	if sig[0] > mid {
		state = 1
	}
	for i := 1; i < n; i++ {
		v := sig[i]
		if state < 0 && v >= up {
			// rising: the mid crossing between the last below-mid sample and here
			j := i
			for j > 0 && sig[j-1] >= mid {
				j--
			}
			if j > 0 {
				a, b := sig[j-1], sig[j]
				if b != a {
					t = append(t, float64(j-1)+(mid-a)/(b-a))
					pol = append(pol, 1)
				}
			}
			state = 1
		} else if state > 0 && v <= down {
			j := i
			for j > 0 && sig[j-1] <= mid {
				j--
			}
			if j > 0 {
				a, b := sig[j-1], sig[j]
				if b != a {
					t = append(t, float64(j-1)+(mid-a)/(b-a))
					pol = append(pol, -1)
				}
			}
			state = -1
		}
	}
	if len(t) < 3 {
		return nil
	}
	return &EdgeSet{T: t, Pol: pol, Lo: lo, Hi: hi, Mid: mid}
}

// histCode is max(0, min(255, Math.round(v))) for a v that is not NaN,
// computed without a floor call (a software routine on the ARMv7, and this is
// the per-sample loop).
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func histCode(v float64) int {
	switch {
	case !(v > 0):
		return 0
	case v >= 255:
		return 255
	}
	i := int(v) // floor, v being positive
	if v-float64(i) >= 0.5 {
		i++
	}
	return i
}

// EstimateUI is the unit interval in samples from consecutive-edge intervals
// (ejEstimateUI), or 0 when they do not cluster on a common grid.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func EstimateUI(t []float64) float64 {
	if len(t) < 2 {
		return 0
	}
	d := make([]float64, len(t)-1)
	for i := 1; i < len(t); i++ {
		d[i-1] = t[i] - t[i-1]
	}
	return EstimateUIFromIntervals(d)
}

// EstimateUIFromIntervals is the estimator core over a bag of intervals
// (ejEstimateUIFromIntervals): NRZ intervals are k*UI, so it seeds near the
// shortest run (the 10th percentile) and refines by dividing each interval by
// its nearest integer multiple. It returns 0 for fewer than 8 intervals or
// when more than 30 % of them sit off the grid.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func EstimateUIFromIntervals(d []float64) float64 {
	if len(d) < 8 {
		return 0
	}
	s := append([]float64(nil), d...)
	sort.Float64s(s)
	return refineUI(d, s[int(math.Floor(float64(len(s))*0.1))])
}

// refineUI runs the estimator's three refinement passes from seed ui.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func refineUI(d []float64, ui float64) float64 {
	if !(ui > 1) {
		return 0
	}
	for pass := 0; pass < 3; pass++ {
		num, den, bad := 0.0, 0.0, 0
		for _, dd := range d {
			k := jsRound(dd / ui)
			// k caps the accepted run length: framed protocols idle for tens of
			// UI legitimately; beyond 64 UI the k assignment is ambiguous, so
			// such intervals are left out but are no evidence against the grid.
			if k < 1 {
				bad++
				continue
			}
			if k > 64 {
				continue
			}
			if math.Abs(dd/ui-k) > 0.3 {
				bad++ // off-grid interval
				continue
			}
			num += dd
			den += k
		}
		if den == 0 || float64(bad) > float64(len(d))*0.3 {
			return 0 // not a serial bit grid
		}
		ui = num / den
	}
	return ui
}

// GridFit is a least-squares bit grid t = T0 + N*UI through a set of edges:
// N holds each edge's (integral) grid index, TIE its distance from the grid,
// in samples, and RMS the residual.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type GridFit struct {
	T0, UI float64
	TIE    []float64
	N      []float64
	RMS    float64
}

// FitGrid fits the edge times t to an ideal grid in two least-squares passes,
// re-deriving the indices after the first (ejFitGrid). It returns nil when the
// fit does not lock: a residual above a quarter UI is not jitter, it is not
// one grid.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func FitGrid(t []float64, ui0 float64) *GridFit {
	if len(t) == 0 {
		return nil
	}
	t0, ui := t[0], ui0
	m := float64(len(t))
	nIdx := make([]float64, len(t))
	for pass := 0; pass < 2; pass++ {
		for i := range t {
			nIdx[i] = jsRound((t[i] - t0) / ui)
		}
		var sn, st, snn, snt float64
		for i, tt := range t {
			n := nIdx[i]
			sn += n
			st += tt
			snn += n * n
			snt += n * tt
		}
		den := m*snn - sn*sn
		if den <= 0 {
			return nil
		}
		ui = (m*snt - sn*st) / den
		t0 = (st - ui*sn) / m
		if !(ui > 1) {
			return nil
		}
	}
	tie := make([]float64, len(t))
	ss := 0.0
	for i := range t {
		tie[i] = t[i] - (t0 + nIdx[i]*ui)
		ss += tie[i] * tie[i]
	}
	rms := math.Sqrt(ss / m)
	if rms > 0.25*ui {
		return nil
	}
	return &GridFit{T0: t0, UI: ui, TIE: tie, N: nIdx, RMS: rms}
}

// fftInPlace is the compact radix-2 FFT of eyejitter.js and peaks.js (ejFFT,
// fftInPlace); len(re) must be a power of two.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-216
func fftInPlace(re, im []float64) {
	n := len(re)
	for i, j := 1, 0; i < n; i++ { // bit-reverse permute
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		ang := -2 * math.Pi / float64(l)
		wr, wi := math.Cos(ang), math.Sin(ang)
		h := l / 2
		for i := 0; i < n; i += l {
			cr, ci := 1.0, 0.0
			for k := 0; k < h; k++ {
				ur, ui := re[i+k], im[i+k]
				vr := re[i+k+h]*cr - im[i+k+h]*ci
				vi := re[i+k+h]*ci + im[i+k+h]*cr
				re[i+k], im[i+k] = ur+vr, ui+vi
				re[i+k+h], im[i+k+h] = ur-vr, ui-vi
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// median is the upper median of a (ejMedian); 0 for an empty slice.
// TRLC-LINKS: REQ-SDS-199
func median(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	s := append([]float64(nil), a...)
	sort.Float64s(s)
	return s[len(s)>>1]
}

// RJDJ splits the aggregated TIE into random and deterministic jitter, dual-
// Dirac-lite (ejRjDj). Splitting a unimodal Gaussian at its median always
// yields side medians 0.674 sigma apart, so bimodality is tested first: the
// band between two separated Diracs is nearly empty, a Gaussian puts about
// 26 % of its samples there. Unimodal: DJ 0, RJ from the global MAD. Bimodal:
// DJ is the mode separation, RJ the spread of each side about its own mode.
// ok is false under 200 samples.
// TRLC-LINKS: REQ-SDS-199
func RJDJ(tie []float64) (rj, dj float64, ok bool) {
	if len(tie) < 200 {
		return 0, 0, false
	}
	s := append([]float64(nil), tie...)
	sort.Float64s(s)
	med := s[len(s)>>1]
	var left, right []float64
	for _, v := range s {
		if v <= med {
			left = append(left, v)
		}
	}
	for _, v := range s {
		if v > med {
			right = append(right, v)
		}
	}
	mL, mR := median(left), median(right)
	sep := mR - mL
	// central-band occupancy, centred on the midpoint of the two modes
	mid := (mL + mR) / 2
	bandLo, bandHi := mid-0.25*sep, mid+0.25*sep
	central := 0
	for _, v := range s {
		if v > bandLo && v < bandHi {
			central++
		}
	}
	if !(sep > 0) || float64(central)/float64(len(s)) > 0.08 {
		return 1.4826 * median(absDev(s, med)), 0, true
	}
	madL, madR := median(absDev(left, mL)), median(absDev(right, mR))
	return 1.4826 * ((madL + madR) / 2), sep, true
}

// absDev is |v - c| for every v of a.
// TRLC-LINKS: REQ-SDS-199
func absDev(a []float64, c float64) []float64 {
	out := make([]float64, len(a))
	for i, v := range a {
		out[i] = math.Abs(v - c)
	}
	return out
}

// rmsOf is the root mean square of a; 0 for an empty slice.
// TRLC-LINKS: REQ-SDS-199
func rmsOf(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	ss := 0.0
	for _, v := range a {
		ss += v * v
	}
	return math.Sqrt(ss / float64(len(a)))
}

// EyeJitterOptions sizes an EyeJitter (ejNew opts); zero fields take the web
// defaults: a 256x128 eye map, a 512-point TIE spectrum and a 60000-sample
// TIE buffer.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type EyeJitterOptions struct {
	EyeW, EyeH int
	FFTN       int
	TIECap     int
}

// EyeJitter is the web engine's accumulating state (ejNew): the eye density
// map, the running UI, the TIE statistics and spectrum, and the cross-record
// interval pool that lets edge-starved records lock.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type EyeJitter struct {
	eyeW, eyeH   int
	eye          []float64 // density counts, row 0 = the lowest code
	lo, hi       int       // levels at the first lock
	eyeY0, eyeY1 float64   // the eye's code window, frozen at the first lock
	ui           float64   // running UI estimate (samples)
	uiN          int
	sampleS      float64

	records, rejected, edges int

	tie              []float64 // decimated TIE buffer (s) for RJ/DJ
	tieCap, tieDecim int
	tieSeen          int
	tieSum, tieSum2  float64 // exact running aggregates, never capped
	tieMin, tieMax   float64
	tieN             int
	periodJ, c2cJ    []float64 // period and cycle-to-cycle jitter (s)
	fftN             int
	spec             []float64 // magnitude-sum TIE spectrum
	specN            int
	specDf           float64
	dPool            []float64 // cross-record edge intervals (the freshest 4000)
	lastSpanUI       float64
	lastErr          string
}

// NewEyeJitter allocates the analysis state (ejNew). Sizes below 2 (eye) or
// not a power of two (spectrum) are raised to the next valid one.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func NewEyeJitter(o EyeJitterOptions) *EyeJitter {
	w, h := o.EyeW, o.EyeH
	if w == 0 {
		w = 256
	}
	if h == 0 {
		h = 128
	}
	w, h = max(2, w), max(2, h)
	n := o.FFTN
	if n == 0 {
		n = 512
	}
	p := 2
	for p < n && p < 1<<20 {
		p <<= 1
	}
	tc := o.TIECap
	if tc <= 0 {
		tc = 60000
	}
	return &EyeJitter{eyeW: w, eyeH: h, eye: make([]float64, w*h), lo: 255,
		tieCap: tc, tieDecim: 1, tieMin: math.Inf(1), tieMax: math.Inf(-1), fftN: p}
}

// Feed analyses one raw record of codes taken sampleS seconds apart (ejFeed)
// and returns "locked:<edges>" or "rejected:<why>".
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func (s *EyeJitter) Feed(sig []float64, sampleS float64) string {
	e := FindEdges(sig)
	if e == nil {
		return s.reject("no-swing")
	}
	// pool intervals across records (the freshest 4000)
	for i := 1; i < len(e.T); i++ {
		s.dPool = append(s.dPool, e.T[i]-e.T[i-1])
	}
	if over := len(s.dPool) - 4000; over > 0 {
		s.dPool = s.dPool[:copy(s.dPool, s.dPool[over:])]
	}
	ui0 := EstimateUI(e.T)
	if !truthy(ui0) && len(s.dPool) >= 8 {
		// a thin record: estimate from the cross-record pool, then still fit
		// this record's edges against it
		ui0 = EstimateUIFromIntervals(s.dPool)
	}
	if !truthy(ui0) {
		return s.reject("no-bit-grid")
	}
	// UI consistency across records: a drifting or false lock is not the same signal
	if s.uiN > 0 && math.Abs(ui0-s.ui) > 0.02*s.ui {
		return s.reject("ui-inconsistent")
	}
	fit := FitGrid(e.T, ui0)
	if fit == nil {
		return s.reject("no-lock")
	}
	if s.uiN == 0 {
		// The vertical mapping is frozen at the first lock, with headroom: the
		// deposits cannot be rebinned, so later level wander lands at its true
		// code in the fixed mapping and the measured eye honestly shrinks.
		s.lo, s.hi = e.Lo, e.Hi
		head := math.Max(8, 0.4*float64(e.Hi-e.Lo))
		s.eyeY0 = math.Max(0, float64(e.Lo)-head)
		s.eyeY1 = math.Min(255, float64(e.Hi)+head)
		s.ui = fit.UI
	} else {
		s.ui = s.ui + (fit.UI-s.ui)/float64(min(s.uiN+1, 32))
	}
	s.uiN++
	s.sampleS = sampleS

	// TIE aggregation (seconds): exact running statistics plus a decimated
	// buffer that stays representative of the whole run
	for _, tv := range fit.TIE {
		v := tv * sampleS
		s.tieSum += v
		s.tieSum2 += v * v
		if v < s.tieMin {
			s.tieMin = v
		}
		if v > s.tieMax {
			s.tieMax = v
		}
		s.tieN++
		if s.tieSeen%s.tieDecim == 0 {
			s.tie = append(s.tie, v)
			if len(s.tie) >= s.tieCap { // halve: keep every other, double the stride
				half := make([]float64, 0, (len(s.tie)+1)/2)
				for j := 0; j < len(s.tie); j += 2 {
					half = append(half, s.tie[j])
				}
				s.tie = half
				s.tieDecim *= 2
			}
		}
		s.tieSeen++
	}
	// period and cycle-to-cycle jitter over consecutive same-polarity (rising)
	// edges: mixing polarities would read duty-cycle distortion as period jitter
	prevIdx, prevPeriod, havePrev := -1, 0.0, false
	for i := range e.T {
		if e.Pol[i] <= 0 {
			continue
		}
		if prevIdx >= 0 {
			if dk := fit.N[i] - fit.N[prevIdx]; dk >= 1 {
				err := (e.T[i] - e.T[prevIdx] - dk*fit.UI) * sampleS
				pushCapped(&s.periodJ, err, s.tieCap)
				if havePrev {
					pushCapped(&s.c2cJ, err-prevPeriod, s.tieCap)
				}
				prevPeriod, havePrev = err, true
			}
		}
		prevIdx = i
	}
	s.edges += len(e.T)

	nUI := fit.N[len(fit.N)-1] - fit.N[0]
	if nUI >= 32 {
		s.feedSpectrum(fit, nUI, sampleS)
	}
	s.lastSpanUI = nUI // record span in UI, for the TIE high-pass corner

	// eye fold: every sample at phase (t - t0) mod 2UI
	w, h := s.eyeW, s.eyeH
	y0, y1 := s.eyeY0, s.eyeY1
	yscale := float64(h-1) / math.Max(1, y1-y0)
	fold := 2 * fit.UI
	n := float64(len(sig))
	iLo := math.Max(0, math.Ceil(fit.T0))
	iHi := math.Min(n, math.Floor(fit.T0+(nUI-1)*fit.UI))
	if iLo < iHi { // both finite here: T0 sits on the record's own edges
		for i := int(iLo); i < int(iHi); i++ {
			if math.IsNaN(sig[i]) {
				continue
			}
			ph := math.Mod(float64(i)-fit.T0, fold)
			if ph < 0 {
				ph += fold
			}
			x := min(w-1, int(math.Floor(ph/fold*float64(w))))
			y := int(math.Min(float64(h-1), math.Max(0, jsRound((sig[i]-y0)*yscale))))
			s.eye[y*w+x]++
		}
	}
	s.records++
	return "locked:" + strconv.Itoa(len(e.T))
}

// feedSpectrum adds one record's TIE spectrum on the UI grid (Hann, magnitude
// averaged across records: the trigger phase is incoherent between records).
// TRLC-LINKS: REQ-SDS-199
func (s *EyeJitter) feedSpectrum(fit *GridFit, nUI, sampleS float64) {
	nf := s.fftN
	grid := make([]float64, nf)
	have := make([]bool, nf)
	base := fit.N[0]
	for i := range fit.N {
		if idx := fit.N[i] - base; idx >= 0 && idx < float64(nf) {
			grid[int(idx)] = fit.TIE[i] * sampleS
			have[int(idx)] = true
		}
	}
	span := int(math.Min(float64(nf), nUI+1))
	// linear-interpolate the data-dependent gaps (PRBS7 runs up to 7 UI)
	last, haveCnt := -1, 0
	for i := 0; i < span; i++ {
		if !have[i] {
			continue
		}
		haveCnt++
		if last >= 0 && i-last > 1 {
			for j := last + 1; j < i; j++ {
				grid[j] = grid[last] + (grid[i]-grid[last])*float64(j-last)/float64(i-last)
			}
		} else if last < 0 {
			for j := 0; j < i; j++ {
				grid[j] = grid[i]
			}
		}
		last = i
	}
	if last >= 0 {
		for j := last + 1; j < span; j++ {
			grid[j] = grid[last]
		}
	}
	// Spectrum honesty gate: a grid that is mostly interpolation (bursty
	// traffic) fabricates spectral structure. NRZ tops out near 50 % slot
	// coverage, bursts sit near 30 %; the gate lives between.
	if float64(haveCnt) < 0.35*float64(span) {
		return
	}
	mean := 0.0
	for i := 0; i < span; i++ {
		mean += grid[i]
	}
	mean /= float64(span)
	re, im := make([]float64, nf), make([]float64, nf)
	for i := 0; i < span; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(span-1))
		re[i] = (grid[i] - mean) * w
	}
	fftInPlace(re, im)
	if s.spec == nil {
		s.spec = make([]float64, nf/2)
	}
	// calibrate each record with its own Hann span (records differ in coverage)
	cal1 := 2 / (0.5 * float64(span))
	for k := 0; k < nf/2; k++ {
		s.spec[k] += math.Sqrt(re[k]*re[k]+im[k]*im[k]) * cal1
	}
	s.specN++
	s.specDf = 1 / (float64(nf) * fit.UI * sampleS) // UI-grid spacing is ui*sampleS seconds
}

// reject counts a record that did not lock.
// TRLC-LINKS: REQ-SDS-199
func (s *EyeJitter) reject(why string) string {
	s.rejected++
	s.lastErr = why
	return "rejected:" + why
}

// pushCapped appends v while a holds fewer than limit values (ejPushCapped).
// TRLC-LINKS: REQ-SDS-199
func pushCapped(a *[]float64, v float64, limit int) {
	if len(*a) < limit {
		*a = append(*a, v)
	}
}

// EyeMetrics are the eye figures measured at the accumulated density
// (ejEyeMetrics): the inner opening between the rails at the eye centre in
// codes, the crossing-free width at the mid level in UI, and the density in
// the centre columns they were measured on.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type EyeMetrics struct {
	EyeHeightCodes float64
	EyeWidthUI     float64
	CrossTotal     float64
}

// JitterResult is EyeJitter's reduction to displayable figures (ejResult).
// The Has flags mark the figures the JavaScript leaves undefined: the TIE
// statistics need 8 edges, RJ/DJ 200, the spectrum one record with enough
// grid coverage, and the eye metrics enough density.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
type JitterResult struct {
	Records, Rejected, Edges int
	UI, SampleS              float64 // UI in samples
	UISeconds, BitRate       float64
	LastErr                  string
	TIEHpHz                  float64 // the per-record fit's high-pass corner

	HasTIE              bool
	TIERms, TIEPp       float64 // seconds
	HasRJDJ             bool
	RJ, DJ              float64
	PeriodJRms, C2CJRms float64
	TIEWindow           int
	Spectrum            []float64 // TIE spectrum magnitudes (s), nil until measured
	SpecDf, SpecPeakHz  float64
	SpecPeakAmp         float64
	Eye                 *EyeMetrics // nil until measurable
}

// Result reduces the state to displayable figures (ejResult). The TIE
// statistics come from the exact running aggregates, RJ/DJ from the decimated
// buffer.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func (s *EyeJitter) Result() JitterResult {
	out := JitterResult{Records: s.records, Rejected: s.rejected, Edges: s.edges,
		UI: s.ui, SampleS: s.sampleS, UISeconds: s.ui * s.sampleS, LastErr: s.lastErr}
	if s.ui > 0 && s.sampleS > 0 {
		out.BitRate = 1 / (s.ui * s.sampleS)
	}
	// The TIE reference clock is a per-record linear fit: jitter slower than
	// about 1/T_record is absorbed by it. Report the corner.
	if s.lastSpanUI > 0 && s.ui > 0 && s.sampleS > 0 {
		out.TIEHpHz = 1 / (s.lastSpanUI * s.ui * s.sampleS)
	}
	if s.tieN >= 8 {
		mean := s.tieSum / float64(s.tieN)
		varc := math.Max(0, s.tieSum2/float64(s.tieN)-mean*mean)
		out.HasTIE = true
		out.TIERms = math.Sqrt(varc)
		out.TIEPp = s.tieMax - s.tieMin
		if rj, dj, ok := RJDJ(s.tie); ok {
			out.HasRJDJ, out.RJ, out.DJ = true, rj, dj
		}
		out.PeriodJRms = rmsOf(s.periodJ)
		out.C2CJRms = rmsOf(s.c2cJ)
		out.TIEWindow = s.tieN
	}
	if s.spec != nil && s.specN > 0 {
		cal := 1 / float64(s.specN)
		mags := make([]float64, len(s.spec))
		for k := range s.spec {
			mags[k] = s.spec[k] * cal
		}
		out.Spectrum, out.SpecDf = mags, s.specDf
		pk, pkV := 0, 0.0 // the dominant off-DC peak
		for k := 3; k < len(mags); k++ {
			if mags[k] > pkV {
				pkV, pk = mags[k], k
			}
		}
		out.SpecPeakHz = float64(pk) * s.specDf
		out.SpecPeakAmp = pkV
	}
	if s.records > 0 {
		out.Eye = s.Metrics()
	}
	return out
}

// Metrics measures the eye on the density map (ejEyeMetrics). The fold puts
// the edges at phases 0 and 0.5 of the 2-UI window, so the eye centre is
// phase 0.75: the rails come from the density clusters of the centre columns
// (the height is the inner gap, 0.1 % trimmed), the width is the longest
// crossing-free run of the mid row that holds the centre. It returns nil
// below 500 counts in the centre columns.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
func (s *EyeJitter) Metrics() *EyeMetrics {
	w, h := s.eyeW, s.eyeH
	colAt := func(f float64) int { return min(w-1, int(math.Floor(f*float64(w)))) }
	codeOf := func(y int) float64 { return s.eyeY0 + float64(y)*(s.eyeY1-s.eyeY0)/float64(h-1) }
	c, span := colAt(0.75), 3
	total := 0.0
	col := make([]float64, h)
	for x := c - span; x <= c+span; x++ {
		if x < 0 || x >= w {
			continue
		}
		for y := 0; y < h; y++ {
			col[y] += s.eye[y*w+x]
			total += s.eye[y*w+x]
		}
	}
	if total < 500 {
		return nil
	}
	midY := float64(h-1) / 2
	// trim 0.1 % from the inner side of each rail (row 0 = the lowest code)
	trim := math.Max(1, total*0.001)
	acc, topInner, botInner := 0.0, -1, -1
	for y := int(math.Floor(midY)); y >= 0; y-- {
		if acc += col[y]; acc >= trim {
			botInner = y
			break
		}
	}
	acc = 0
	for y := int(math.Ceil(midY)); y < h; y++ {
		if acc += col[y]; acc >= trim {
			topInner = y
			break
		}
	}
	height := 0.0
	if topInner >= 0 && botInner >= 0 && topInner > botInner {
		height = codeOf(topInner) - codeOf(botInner)
	}
	// the width: the longest contiguous mid-row gap holding the eye centre
	// (counting every empty column of the fold would add the second eye)
	yMid, band := int(jsRound(midY)), 2
	row := make([]float64, w)
	rowTot := 0.0
	for y := yMid - band; y <= yMid+band; y++ {
		if y < 0 || y >= h {
			continue
		}
		for x := 0; x < w; x++ {
			row[x] += s.eye[y*w+x]
			rowTot += s.eye[y*w+x]
		}
	}
	width := 0.0
	if rowTot > 200 {
		thr := rowTot / float64(w) * 0.5 // half of uniform density = a crossing cluster
		xc := colAt(0.75)
		if row[xc] < thr {
			l, r := xc, xc
			for l > 0 && row[l-1] < thr {
				l--
			}
			for r < w-1 && row[r+1] < thr {
				r++
			}
			width = float64(r-l+1) / float64(w) * 2 // the fold spans 2 UI
		}
	}
	return &EyeMetrics{EyeHeightCodes: height, EyeWidthUI: math.Min(1, width), CrossTotal: total}
}

// EyeDigest summarises the density map for parity checks: the total count,
// the index-weighted total and the number of occupied cells.
// TRLC-LINKS: REQ-SDS-215
func (s *EyeJitter) EyeDigest() (sum, weighted float64, cells int) {
	for i, v := range s.eye {
		if v != 0 {
			sum += v
			weighted += v * float64(i+1)
			cells++
		}
	}
	return sum, weighted, cells
}
