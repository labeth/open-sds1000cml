// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

// The accumulating eye diagram of the LCD's BUS workspace (BUS again). It is
// built from the eyejitter.js port: the same edges, UI estimate and least-
// squares bit grid, the same TIE statistics, and eye height and width measured
// with ejEyeMetrics' rules. Three things differ, because a bus is not a PRBS
// stream:
//
//   - Edges more than 12 UI apart start a new burst, and each burst gets its
//     own bit grid (a UART byte or an I2C transaction starts at any time). A
//     continuous stream is one burst, so it gives the web's figures.
//   - The decoder's bit period seeds the UI estimate (UART 1/baud, the SPI or
//     I2C clock period), so a record with few edges still folds.
//   - The density grid is the LCD's: each pair of successive samples is drawn
//     as a line, as the trace itself is, so an edge that falls between two
//     samples shows as a line across the sample interval instead of being
//     missed. The grid is folded at one UI with the crossing in the middle and
//     shown twice side by side: one full eye in the centre of a 2-UI window.
//
// The figures accumulate until Reset; the caller resets when a setting changes
// the picture (scale, timebase, decode, acquisition).

import "math"

const (
	// EyeCodeLo and EyeCodeHi bound the grid's code window: the LCD
	// graticule's 8 divisions (25 codes per division centred on code 128,
	// spec 10 §7.1), so the eye lies over the plot at the trace's own scale.
	// Samples outside the window are not drawn.
	EyeCodeLo = 28.0
	EyeCodeHi = 228.0

	// MinSamplesPerUI is the sampling below which the eye is sample-limited:
	// its width is then off by more than 5 % (the prototype review).
	MinSamplesPerUI = 20.0

	eyeMinSPU     = 1.2     // the fewest samples per UI an eye folds
	eyeBurstGapUI = 12      // edges further apart start a new burst
	eyeFramingUI  = 0.15    // a burst's first or last edge at least this far off the grid may be framing (I2C START/STOP)
	eyeMaxCount   = 1 << 30 // halve the grid before a count (or a run in diff) can overflow
	eyeFix        = 16      // fractional bits of the fixed-point grid positions
	eyeMaxCols    = 2048
	eyeMaxRows    = 1024
	eyeTIECap     = 60000 // the TIE and period samples kept for RJ/DJ and the period jitter (the web's cap)
)

// Why a capture was not folded (EyeResult.LastErr).
const (
	EyeNoRecord       = "no-record"              // too short, no sample interval, or non-finite codes
	EyeNoSwing        = "no-swing"               // no two distinct levels, or fewer than 3 edges
	EyeNoBitGrid      = "no-bit-grid"            // the edge intervals do not cluster on a bit period
	EyeTooFewSamples  = "too-few-samples-per-ui" // under 1.2 samples per UI
	EyeShortRecord    = "short-record"           // the record spans less than 2 UI
	EyeUIInconsistent = "ui-inconsistent"        // the bit period moved more than 2 % from the accumulated one
	EyeNoLock         = "no-lock"                // no burst fitted one bit grid
)

// EyeBox is an eye opening in the grid's window: the horizontal opening at a
// threshold (Left and Right, in UI from the window's left edge, the window
// spanning 0..2 UI with the crossings at 0.5 and 1.5) and the vertical opening
// at a sample point (Lo and Hi, in codes). HOK and VOK say whether each was
// measured; a closed eye measures as a zero width.
// TRLC-LINKS: REQ-SDS-215
type EyeBox struct {
	Left, Right float64
	Lo, Hi      float64
	HOK, VOK    bool
}

// Width is the horizontal opening in UI, NaN when not measured.
// TRLC-LINKS: REQ-SDS-215
func (b EyeBox) Width() float64 {
	if !b.HOK {
		return math.NaN()
	}
	return b.Right - b.Left
}

// Height is the vertical opening in codes, NaN when not measured.
// TRLC-LINKS: REQ-SDS-215
func (b EyeBox) Height() float64 {
	if !b.VOK {
		return math.NaN()
	}
	return b.Hi - b.Lo
}

// EyeResult is what the eye has accumulated. Times are in seconds, levels in
// codes; a figure that is not measured yet is NaN.
//
// When an edge spans under two sample intervals it falls between samples: its
// time is then only known to about one sample interval, so TIE, rise and fall
// are upper bounds, the eye width a lower bound, and the crossing is not given
// (the prototype review's rule). The Bound flags mark these figures, and
// SampleLimited says the eye is drawn from fewer than MinSamplesPerUI samples
// per UI.
// TRLC-LINKS: REQ-SDS-215
type EyeResult struct {
	BitRate      float64 // bits per second
	UI           float64 // the unit interval
	SamplesPerUI float64

	EyeHeight float64 // codes: the vertical opening at the eye centre (Open)
	EyeWidth  float64 // UI: the horizontal opening at the threshold (Open)
	Crossing  float64 // the crossing level as a fraction of the swing (0..1)
	Rise      float64 // 10-90 %, mean over the folded edges
	Fall      float64 // 90-10 %
	TIERms    float64 // time interval error against each burst's bit grid
	TIEPkPk   float64
	RJ, DJ    float64 // the TIE's random and deterministic parts (the web's dual-Dirac split, RJDJ)
	PeriodJ   float64 // rms period jitter of consecutive rising edges
	C2CJ      float64 // rms cycle-to-cycle jitter of the rising edges' periods
	Lo, Hi    float64 // the two levels (codes)

	UIs      float64 // unit intervals folded
	Edges    int     // edges in the TIE statistics
	Records  int     // captures folded
	Rejected int     // captures not folded
	LastErr  string  // why the last capture was not folded, "" when it was

	SampleLimited bool // under MinSamplesPerUI samples per UI
	TIEBound      bool // TIERms and TIEPkPk are upper bounds
	RiseBound     bool // Rise is an upper bound
	FallBound     bool // Fall is an upper bound
	WidthBound    bool // EyeWidth and Open's width are lower bounds

	// Open is the opening at the eye centre (1 UI) and the threshold: the
	// crossing level when resolved, else the middle of the two levels.
	Open EyeBox

	// Grid is the density, Cols x Rows row-major, row 0 at EyeCodeHi and
	// column 0 at the window's left edge. It aliases the Eye's buffer and is
	// valid until the next Result or Reset. GridMax is its largest cell.
	Cols, Rows int
	Grid       []uint32
	GridMax    uint32
}

// Eye accumulates an eye diagram and its jitter figures over captures.
// TRLC-LINKS: REQ-SDS-215
type Eye struct {
	cols, rows, tw int
	cpr            float64 // codes per row
	// The density of one UI, the crossing in the middle column, is the sum
	// of single counts (tile) and runs of counts along a row (diff, a
	// difference array per row: a level costs two writes, not a cell per
	// column). dense holds the sum, rebuilt when dirty.
	tile    []uint32 // rows x tw
	diff    []int32  // rows x (tw+1)
	dense   []uint32 // rows x tw
	dirty   bool
	bound   uint64   // no cell holds more counts than this
	grid    []uint32 // cols x rows, rebuilt by Result
	scratch []float64

	uiS     float64 // running UI estimate (seconds)
	uiN     int
	sampleS float64
	pool    []float64 // cross-record edge intervals (samples)

	tieSum, tieSum2, tieMin, tieMax float64
	tieN                            int
	tie                             []float64 // decimated TIE (s) for RJ/DJ: every tieDecim-th, halved at eyeTIECap
	tieDecim, tieSeen               int
	periodJ, c2cJ                   []float64 // period and cycle-to-cycle errors (s) of rising edges
	loSum, hiSum                    float64
	levN                            int
	xSum                            float64 // crossing samples, as fractions of the swing
	xN                              int
	riseSum, fallSum                float64
	riseN, fallN                    int
	uis                             float64
	records, rejected               int
	lastErr                         string
}

// NewEye makes an empty eye with a cols x rows density grid over a 2-UI x
// code window. cols is rounded down to an even count (the window is one UI
// shown twice); both are clamped to [2, 2048] x [2, 1024].
// TRLC-LINKS: REQ-SDS-215
func NewEye(cols, rows int) *Eye {
	cols = clampInt(cols, 2, eyeMaxCols) &^ 1
	rows = clampInt(rows, 2, eyeMaxRows)
	tw := cols / 2
	return &Eye{cols: cols, rows: rows, tw: tw, cpr: (EyeCodeHi - EyeCodeLo) / float64(rows),
		tile: make([]uint32, rows*tw), diff: make([]int32, rows*(tw+1)), dense: make([]uint32, rows*tw),
		grid: make([]uint32, rows*cols), scratch: make([]float64, max(rows, tw)),
		tieMin: math.Inf(1), tieMax: math.Inf(-1), tieDecim: 1}
}

// Reset empties the eye, keeping its size.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Reset() {
	clear(e.tile)
	clear(e.diff)
	clear(e.dense)
	clear(e.grid)
	*e = Eye{cols: e.cols, rows: e.rows, tw: e.tw, cpr: e.cpr, tile: e.tile, diff: e.diff,
		dense: e.dense, grid: e.grid, scratch: e.scratch, pool: e.pool[:0],
		tie: e.tie[:0], periodJ: e.periodJ[:0], c2cJ: e.c2cJ[:0],
		tieMin: math.Inf(1), tieMax: math.Inf(-1), tieDecim: 1}
}

// Feed folds one capture of codes taken sampleS seconds apart into the eye.
// bitHintS is the decoder's bit period in seconds (UART 1/baud, the SPI or I2C
// clock period), or 0 to estimate it from the edges alone. A capture that
// cannot be folded is counted and its reason kept in LastErr.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Feed(codes []float64, sampleS float64, bitHintS float64) {
	if !(sampleS > 0) || math.IsInf(sampleS, 0) || len(codes) < 16 {
		e.reject(EyeNoRecord)
		return
	}
	for _, v := range codes {
		if v-v != 0 { // NaN or infinite
			e.reject(EyeNoRecord)
			return
		}
	}
	es := FindEdges(codes)
	if es == nil {
		e.reject(EyeNoSwing)
		return
	}
	d := make([]float64, len(es.T)-1)
	for i := 1; i < len(es.T); i++ {
		d[i-1] = es.T[i] - es.T[i-1]
	}
	e.pool = append(e.pool, d...)
	if over := len(e.pool) - 4000; over > 0 {
		e.pool = e.pool[:copy(e.pool, e.pool[over:])]
	}
	hint := 0.0
	if bitHintS > 0 && finite(bitHintS) {
		hint = bitHintS / sampleS
	}
	ui0 := e.estimate(d, hint)
	switch {
	case !(ui0 > 0) || !finite(ui0):
		e.reject(EyeNoBitGrid)
		return
	case ui0 < eyeMinSPU:
		e.reject(EyeTooFewSamples)
		return
	case float64(len(codes)) < 2*ui0:
		e.reject(EyeShortRecord)
		return
	case e.uiN > 0 && math.Abs(ui0*sampleS-e.uiS) > 0.02*e.uiS:
		e.reject(EyeUIInconsistent)
		return
	}
	folded, fittable := false, false
	for a, k := 0, 1; k <= len(es.T); k++ {
		if k < len(es.T) && es.T[k]-es.T[k-1] <= eyeBurstGapUI*ui0 {
			continue
		}
		fittable = fittable || k-a >= 3
		if e.foldBurst(codes, es, a, k, ui0, sampleS) {
			folded = true
		}
		a = k
	}
	if !fittable {
		// every burst is a lone pulse (a sparse pulse train on a steady
		// clock): fit the record as one grid, as the web does
		folded = e.foldBurst(codes, es, 0, len(es.T), ui0, sampleS)
	}
	if !folded {
		e.reject(EyeNoLock)
		return
	}
	e.records++
	e.sampleS = sampleS
	e.loSum += float64(es.Lo)
	e.hiSum += float64(es.Hi)
	e.levN++
	e.lastErr = ""
}

// halve halves every count once they could approach overflow (a cell, or a
// run total in diff, past 2^31): after hours of captures the picture keeps
// its shape and the figures their ratios.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) halve() {
	if e.bound <= eyeMaxCount {
		return
	}
	e.dirty = true
	e.materialize()
	top := uint32(0)
	for i, v := range e.dense {
		e.tile[i] = v >> 1
		top = max(top, v>>1)
	}
	clear(e.diff)
	e.bound, e.dirty = uint64(top), true
}

// estimate is the UI in samples for a record: refined from the decoder's
// period when there is one, else the web estimator on the record's own
// intervals, then on the cross-record pool, and as a last resort the
// decoder's period itself (an edge-starved record, a single UART byte).
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) estimate(d []float64, hint float64) float64 {
	ok := func(u float64) bool { return u > 0 && finite(u) }
	if hint > 1 && len(d) >= 3 {
		if u := refineUI(d, hint); ok(u) {
			return u
		}
	}
	if u := EstimateUIFromIntervals(d); ok(u) {
		return u
	}
	if len(e.pool) >= 8 {
		if hint > 1 {
			if u := refineUI(e.pool, hint); ok(u) {
				return u
			}
		}
		if u := EstimateUIFromIntervals(e.pool); ok(u) {
			return u
		}
	}
	return hint
}

// foldBurst fits edges [a, b) to their own bit grid and, when it locks, adds
// their TIE, rise and fall times and samples to the eye.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) foldBurst(sig []float64, es *EdgeSet, a, b int, ui0, sampleS float64) bool {
	if b-a < 3 {
		return false
	}
	fit := fitBurst(es.T[a:b], ui0)
	if fit == nil {
		return false
	}
	// A first or last edge far off the grid is protocol framing, not jitter
	// (an I2C START or STOP moves SDA while SCL is high): fold without it.
	// One such edge tilts the fit, so the worse end goes first and the other
	// is judged on the refit.
	for drop := 0; drop < 2; drop++ {
		lim := framingLimit(fit)
		first, last := math.Abs(fit.TIE[0]), math.Abs(fit.TIE[len(fit.TIE)-1])
		if first <= lim && last <= lim {
			break
		}
		if first >= last {
			a++
		} else {
			b--
		}
		if b-a < 3 {
			return false
		}
		if fit = fitBurst(es.T[a:b], ui0); fit == nil {
			return false
		}
	}
	if fit.UI < eyeMinSPU { // the burst's own period is under the floor
		return false
	}
	if uiS := fit.UI * sampleS; e.uiN == 0 {
		e.uiS = uiS
	} else {
		e.uiS += (uiS - e.uiS) / float64(min(e.uiN+1, 32))
	}
	e.uiN++
	for _, tv := range fit.TIE {
		v := tv * sampleS
		e.tieSum += v
		e.tieSum2 += v * v
		e.tieMin = math.Min(e.tieMin, v)
		e.tieMax = math.Max(e.tieMax, v)
		e.tieN++
		if e.tieSeen%e.tieDecim == 0 { // a buffer that stays representative of the whole run, as the web's
			if e.tie = append(e.tie, v); len(e.tie) >= eyeTIECap {
				half := make([]float64, 0, (len(e.tie)+1)/2)
				for j := 0; j < len(e.tie); j += 2 {
					half = append(half, e.tie[j])
				}
				e.tie, e.tieDecim = half, e.tieDecim*2
			}
		}
		e.tieSeen++
	}
	e.periods(es.T[a:b], es.Pol[a:b], fit, sampleS)
	lo, hi := float64(es.Lo), float64(es.Hi)
	e.edgeTimes(sig, es, a, b, lo, hi, fit.UI, sampleS)
	e.fold(sig, fit, es.T[a], es.T[b-1], lo, hi)
	return true
}

// periods adds a burst's period and cycle-to-cycle errors: consecutive rising
// edges against the bit grid (mixing polarities would read duty-cycle
// distortion as period jitter), as the web's engine does.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) periods(t []float64, pol []int, fit *GridFit, sampleS float64) {
	prev, prevErr, havePrev := -1, 0.0, false
	for i := range t {
		if pol[i] <= 0 {
			continue
		}
		if prev >= 0 {
			if dk := fit.N[i] - fit.N[prev]; dk >= 1 {
				err := (t[i] - t[prev] - dk*fit.UI) * sampleS
				pushCapped(&e.periodJ, err, eyeTIECap)
				if havePrev {
					pushCapped(&e.c2cJ, err-prevErr, eyeTIECap)
				}
				prevErr, havePrev = err, true
			}
		}
		prev = i
	}
}

// framingLimit is how far off the grid a burst's first or last edge must lie
// to count as framing: 0.15 UI, and five times the rms residual of the edges
// between them, so genuine jitter (a 5 sigma end edge is one in a million)
// stays in the figures.
// TRLC-LINKS: REQ-SDS-215
func framingLimit(fit *GridFit) float64 {
	ss, n := 0.0, 0
	for _, v := range fit.TIE[1 : len(fit.TIE)-1] {
		ss += v * v
		n++
	}
	lim := eyeFramingUI * fit.UI
	if n > 0 {
		lim = math.Max(lim, 5*math.Sqrt(ss/float64(n)))
	}
	return lim
}

// fitBurst is FitGrid when the burst has edges enough to fit its own period
// within 5 % of the record's, else a grid with the record's period (a few
// edges fix an offset well but a slope badly).
// TRLC-LINKS: REQ-SDS-215
func fitBurst(t []float64, ui float64) *GridFit {
	if len(t) >= 8 {
		if f := FitGrid(t, ui); f != nil && math.Abs(f.UI/ui-1) <= 0.05 {
			return f
		}
	}
	return fitOffset(t, ui)
}

// fitOffset fits the grid offset alone, the period fixed at ui, in two passes
// as FitGrid does; nil when the residual exceeds a quarter UI.
// TRLC-LINKS: REQ-SDS-215
func fitOffset(t []float64, ui float64) *GridFit {
	m := float64(len(t))
	t0 := t[0]
	n := make([]float64, len(t))
	for pass := 0; pass < 2; pass++ {
		s := 0.0
		for i := range t {
			n[i] = jsRound((t[i] - t0) / ui)
			s += t[i] - n[i]*ui
		}
		t0 = s / m
	}
	tie := make([]float64, len(t))
	ss := 0.0
	for i := range t {
		tie[i] = t[i] - (t0 + n[i]*ui)
		ss += tie[i] * tie[i]
	}
	rms := math.Sqrt(ss / m)
	if !(rms <= 0.25*ui) {
		return nil
	}
	return &GridFit{T0: t0, UI: ui, TIE: tie, N: n, RMS: rms}
}

// edgeTimes adds the 10-90 % rise and 90-10 % fall time of each edge in
// [a, b), searching up to half a UI either side of its mid crossing.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) edgeTimes(sig []float64, es *EdgeSet, a, b int, lo, hi, ui, sampleS float64) {
	n := len(sig)
	l10, l90 := lo+0.1*(hi-lo), lo+0.9*(hi-lo)
	lim := int(math.Max(1, 0.5*ui))
	for k := a; k < b; k++ {
		j := int(es.T[k]) // sig[j] and sig[j+1] straddle the mid level
		if j < 0 || j+1 >= n {
			continue
		}
		if es.Pol[k] > 0 {
			i10, i90 := -1, -1
			for i := j; i >= 0 && j-i <= lim; i-- {
				if sig[i] <= l10 {
					i10 = i
					break
				}
			}
			for i := j + 1; i < n && i-j <= lim+1; i++ {
				if sig[i] >= l90 {
					i90 = i
					break
				}
			}
			if i10 >= 0 && i90 > 0 {
				t10 := float64(i10) + (l10-sig[i10])/(sig[i10+1]-sig[i10])
				t90 := float64(i90-1) + (l90-sig[i90-1])/(sig[i90]-sig[i90-1])
				if t90 > t10 {
					e.riseSum += (t90 - t10) * sampleS
					e.riseN++
				}
			}
			continue
		}
		i90, i10 := -1, -1
		for i := j; i >= 0 && j-i <= lim; i-- {
			if sig[i] >= l90 {
				i90 = i
				break
			}
		}
		for i := j + 1; i < n && i-j <= lim+1; i++ {
			if sig[i] <= l10 {
				i10 = i
				break
			}
		}
		if i90 >= 0 && i10 > 0 {
			t90 := float64(i90) + (sig[i90]-l90)/(sig[i90]-sig[i90+1])
			t10 := float64(i10-1) + (sig[i10-1]-l10)/(sig[i10-1]-sig[i10])
			if t10 > t90 {
				e.fallSum += (t10 - t90) * sampleS
				e.fallN++
			}
		}
	}
}

// fold draws the samples from one UI before the burst's first edge to one UI
// after its last into the tile, as lines between successive samples, and
// collects the crossing samples. Positions are fixed point (eyeFix fraction
// bits, under 2^27 so an int on the ARMv7 holds them): x in tile columns
// wrapped to one UI, r in rows. The drawing does no floating-point rounding
// per sample (software routines on the ARMv7), and no int64 conversion (a
// runtime call on 32-bit ARM).
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) fold(sig []float64, fit *GridFit, tFirst, tLast, lo, hi float64) {
	ja := max(0, int(math.Ceil(tFirst-fit.UI)))
	jb := min(len(sig)-1, int(math.Floor(tLast+fit.UI)))
	if jb <= ja {
		return
	}
	const one = 1 << eyeFix
	span := e.tw << eyeFix   // one UI
	pos := func(j int) int { // the burst's edges sit at x.5 UI: the crossing mid-tile
		p := (float64(j)-fit.T0)/fit.UI + 0.5
		return min(span-1, int((p-math.Floor(p))*float64(span)))
	}
	step := int(float64(span)/fit.UI + 0.5) // one sample, under one UI
	if step >= span {
		return // under one sample per UI: no line fits in the tile (foldBurst's floor prevents it)
	}
	rowK, rowMax := one/e.cpr, float64(e.rows*one)
	rowPos := func(code float64) int {
		v := (EyeCodeHi - code) * rowK
		if v < -one {
			v = -one
		} else if v > rowMax {
			v = rowMax
		}
		return int(v+one) - one // floor: rows above the window stay negative
	}
	sw := hi - lo
	xLo, xHi := lo+0.2*sw, hi-0.2*sw
	cLo, cHi := int(0.4*float64(span)), int(0.6*float64(span)) // the crossing +-0.1 UI
	x, r := pos(ja), rowPos(sig[ja])
	e.cell(r>>eyeFix, x>>eyeFix)
	// The common lines are drawn here, without a call: a level (one run) and
	// a step of one row (noise on a level: two runs, split mid-way).
	// segment draws the rest, the transitions.
	tw, rows, tile, diff := e.tw, e.rows, e.tile, e.diff
	runs := 0
	for j := ja; j <= jb; j++ {
		v := sig[j]
		if j > ja {
			d := step
			if (j-ja)&1023 == 0 { // re-anchor: the rounded step drifts 0.5/65536 column a sample
				if d = pos(j) - x; d < 0 {
					d += span
				}
				// at most 1024 samples of at most rows+2 counts since the
				// last check: the 2^30 limit keeps 2^31 headroom
				e.bound += uint64(runs)
				runs = 0
				e.halve()
			}
			nr := rowPos(v)
			row0, row1 := r>>eyeFix, nr>>eyeFix
			c0, c1 := x>>eyeFix, (x+d)>>eyeFix
			switch dr := row1 - row0; {
			case dr == 0 && uint(row1) < uint(rows):
				if c1 == c0 {
					if c1 >= tw {
						c1 -= tw
					}
					tile[row1*tw+c1]++
				} else {
					addRun(diff[row1*(tw+1):(row1+1)*(tw+1)], c0+1, c1, tw)
				}
				runs++
			case (dr == 1 || dr == -1) && c1-c0 > 1 && uint(row0) < uint(rows) && uint(row1) < uint(rows):
				mid := (c0 + c1 + 1) >> 1
				addRun(diff[row0*(tw+1):(row0+1)*(tw+1)], c0+1, mid, tw)
				addRun(diff[row1*(tw+1):(row1+1)*(tw+1)], mid, c1, tw)
				runs += 2
			default:
				e.segment(x, d, r, nr)
			}
			if x += d; x >= span {
				x -= span
			}
			r = nr
		}
		if x > cLo && x < cHi && v > xLo && v < xHi { // at the crossing, mid-swing
			e.xSum += (v - lo) / sw
			e.xN++
		}
	}
	e.bound += uint64(runs)
	e.uis += float64(jb-ja) / fit.UI
	e.dirty = true
	e.halve()
}

// addRun counts one in each column a..b of a row's difference array d
// (tw+1 long; b - a under one UI, b up to one UI past the tile).
// TRLC-LINKS: REQ-SDS-215
func addRun(d []int32, a, b, tw int) {
	if a >= tw {
		a, b = a-tw, b-tw
	}
	d[a]++
	if b < tw {
		d[b+1]--
		return
	}
	d[tw]-- // past the last column: never summed
	d[0]++
	d[b-tw+1]--
}

// rowOf is the grid row of a code as a float: row centres sit on integers,
// row 0 at the top of the window.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) rowOf(code float64) float64 { return (EyeCodeHi-code)/e.cpr - 0.5 }

// codeOfRow is the code at the centre of row r.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) codeOfRow(r int) float64 { return EyeCodeHi - (float64(r)+0.5)*e.cpr }

// segment draws the line from fixed-point column x0 (within one UI) a
// distance d (under one UI) to the right, and from fixed-point row r0 to r1,
// leaving out its start point, which the previous segment drew. A level is one
// run; a line that changes rows is a run in each row it crosses, or, when it
// is steep, one cell in each row.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) segment(x0, d, r0, r1 int) {
	c0, c1 := x0>>eyeFix, (x0+d)>>eyeFix // c1 may pass the tile by up to one UI
	row0, row1 := r0>>eyeFix, r1>>eyeFix
	if row0 == row1 {
		if c1 == c0 {
			e.cell(row1, c1)
		} else {
			e.run(row0, c0+1, c1)
		}
		return
	}
	dr, s, dc := row1-row0, 1, c1-c0
	if dr < 0 {
		dr, s = -dr, -1
	}
	if dr >= dc { // steep: a cell in each row, the column stepping evenly
		col, acc := c0, 0
		for k := 1; k <= dr; k++ {
			if acc += dc; acc >= dr {
				acc -= dr
				col++
			}
			e.cell(row0+s*k, col)
		}
		return
	}
	if dr == 1 { // a one-row step (noise on a level): split mid-way
		mid := (c0 + c1 + 1) >> 1
		e.run(row0, c0+1, mid)
		e.run(row1, mid, c1)
		return
	}
	// shallow: split at each row boundary; the boundary column goes to both rows
	slope := float64(d) / float64(r1-r0)
	from := c0 + 1
	for row := row0; row != row1; row += s {
		b := row << eyeFix
		if s > 0 {
			b += 1 << eyeFix
		}
		cb := (x0 + int(float64(b-r0)*slope)) >> eyeFix
		e.run(row, from, cb)
		from = max(from, cb)
	}
	e.run(row1, from, c1)
}

// cell counts one at row and column c (c up to one UI past the tile).
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) cell(row, c int) {
	if uint(row) >= uint(e.rows) || c < 0 {
		return
	}
	if c >= e.tw {
		c -= e.tw
	}
	e.tile[row*e.tw+c]++
	e.bound++
}

// run counts one in each column a..b of row (b - a under one UI, b up to one
// UI past the tile): two writes to the row's difference array, four when it
// wraps.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) run(row, a, b int) {
	if uint(row) >= uint(e.rows) || b < a || a < 0 {
		return
	}
	addRun(e.diff[row*(e.tw+1):(row+1)*(e.tw+1)], a, b, e.tw)
	e.bound++
}

// materialize rebuilds dense, the counts and runs summed, when it is stale.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) materialize() {
	if !e.dirty {
		return
	}
	tw := e.tw
	for r := 0; r < e.rows; r++ {
		d := e.diff[r*(tw+1) : r*(tw+1)+tw]
		t := e.tile[r*tw : (r+1)*tw]
		out := e.dense[r*tw : (r+1)*tw]
		acc := int32(0)
		for c := range out {
			acc += d[c]
			out[c] = t[c] + uint32(acc)
		}
	}
	e.dirty = false
}

// reject counts a capture that was not folded.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) reject(why string) {
	e.rejected++
	e.lastErr = why
}

// Result reduces the accumulated eye to its figures and density grid.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Result() EyeResult {
	nan := math.NaN()
	r := EyeResult{BitRate: nan, UI: nan, SamplesPerUI: nan, EyeHeight: nan, EyeWidth: nan,
		Crossing: nan, Rise: nan, Fall: nan, TIERms: nan, TIEPkPk: nan, RJ: nan, DJ: nan, PeriodJ: nan, C2CJ: nan,
		Lo: nan, Hi: nan,
		UIs: e.uis, Edges: e.tieN, Records: e.records, Rejected: e.rejected, LastErr: e.lastErr,
		Cols: e.cols, Rows: e.rows, Grid: e.grid}
	if e.uiN > 0 && e.uiS > 0 {
		r.UI, r.BitRate = e.uiS, 1/e.uiS
		if e.sampleS > 0 {
			r.SamplesPerUI = e.uiS / e.sampleS
		}
	}
	if e.tieN >= 8 {
		mean := e.tieSum / float64(e.tieN)
		r.TIERms = math.Sqrt(math.Max(0, e.tieSum2/float64(e.tieN)-mean*mean))
		r.TIEPkPk = e.tieMax - e.tieMin
	}
	if rj, dj, ok := RJDJ(e.tie); ok {
		r.RJ, r.DJ = rj, dj
	}
	if len(e.periodJ) > 0 {
		r.PeriodJ = rmsOf(e.periodJ)
	}
	if len(e.c2cJ) > 0 {
		r.C2CJ = rmsOf(e.c2cJ)
	}
	if e.riseN > 0 {
		r.Rise = e.riseSum / float64(e.riseN)
	}
	if e.fallN > 0 {
		r.Fall = e.fallSum / float64(e.fallN)
	}
	if e.levN > 0 {
		r.Lo, r.Hi = e.loSum/float64(e.levN), e.hiSum/float64(e.levN)
	}
	// edges spanning under two sample intervals fall between samples
	riseOK := r.Rise >= 2*e.sampleS
	fallOK := r.Fall >= 2*e.sampleS
	r.RiseBound, r.FallBound = !riseOK, !fallOK
	r.TIEBound = !(riseOK && fallOK)
	r.WidthBound = r.TIEBound
	r.SampleLimited = r.SamplesPerUI < MinSamplesPerUI
	if riseOK && fallOK && e.xN >= 20 {
		r.Crossing = e.xSum / float64(e.xN)
	}
	th := (r.Lo + r.Hi) / 2 // NaN until a capture is folded: Opening then measures nothing
	if !math.IsNaN(r.Crossing) {
		th = r.Lo + r.Crossing*(r.Hi-r.Lo)
	}
	r.Open = e.Opening(1, th)
	r.EyeHeight, r.EyeWidth = r.Open.Height(), r.Open.Width()
	e.materialize()
	for row := 0; row < e.rows; row++ {
		src := e.dense[row*e.tw : (row+1)*e.tw]
		copy(e.grid[row*e.cols:], src)
		copy(e.grid[row*e.cols+e.tw:], src)
		for _, v := range src {
			r.GridMax = max(r.GridMax, v)
		}
	}
	return r
}

// Opening measures the eye at a sample point (sampleUI, UI from the window's
// left edge; the eye centre is 1) and a threshold (a code), with ejEyeMetrics'
// rules: the vertical opening is the gap between the rails' inner edges
// around the middle of the two levels, each rail trimmed by 0.1 % of the
// counts near the sample point; the horizontal opening is the run of columns
// around the sample point where the density near the threshold stays under
// half its mean. The cursors of the BUS eye view use it.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Opening(sampleUI, threshold float64) EyeBox {
	nan := math.NaN()
	b := EyeBox{Left: nan, Right: nan, Lo: nan, Hi: nan}
	if e.levN == 0 || !finite(sampleUI) || !finite(threshold) {
		return b
	}
	e.materialize()
	lo, hi := e.loSum/float64(e.levN), e.hiSum/float64(e.levN)
	tw := e.tw
	wcol := math.Floor(sampleUI * float64(tw)) // the sample point's column in the window
	cs := int(wcol - math.Floor(wcol/float64(tw))*float64(tw))
	cs = clampInt(cs, 0, tw-1)

	// vertical: the centre columns (+-0.0234 UI, the web's +-3 of 256)
	span := max(1, int(0.0234*float64(tw)+0.5))
	col := e.scratch[:e.rows]
	clear(col)
	total := 0.0
	for dc := -span; dc <= span; dc++ {
		c := ((cs+dc)%tw + tw) % tw
		for row := 0; row < e.rows; row++ {
			v := float64(e.dense[row*tw+c])
			col[row] += v
			total += v
		}
	}
	if total >= 500 {
		rm := e.rowOf((lo + hi) / 2)
		trim := math.Max(1, total*0.001)
		hiRow, loRow := -1, -1
		acc := 0.0
		for row := int(math.Floor(rm)); row >= 0; row-- { // toward the upper rail
			if row >= e.rows {
				continue
			}
			if acc += col[row]; acc >= trim {
				hiRow = row
				break
			}
		}
		acc = 0
		for row := max(0, int(math.Ceil(rm))); row < e.rows; row++ { // toward the lower rail
			if acc += col[row]; acc >= trim {
				loRow = row
				break
			}
		}
		if hiRow >= 0 && loRow >= 0 {
			b.Hi, b.Lo, b.VOK = e.codeOfRow(hiRow), e.codeOfRow(loRow), true
			if loRow <= hiRow {
				b.Hi = b.Lo // closed
			}
		}
	}

	// horizontal: the rows within +-2.8 % of the swing (the web's +-2 rows)
	band := max(1, int(0.0283*(hi-lo)/e.cpr+0.5))
	rb := int(jsRound(e.rowOf(threshold)))
	row := e.scratch[:tw]
	clear(row)
	rowTot := 0.0
	for rr := rb - band; rr <= rb+band; rr++ {
		if rr < 0 || rr >= e.rows {
			continue
		}
		for c := 0; c < tw; c++ {
			v := float64(e.dense[rr*tw+c])
			row[c] += v
			rowTot += v
		}
	}
	if rowTot > 200 {
		thr := rowTot / float64(tw) * 0.5
		left := wcol / float64(tw)
		b.Left, b.Right, b.HOK = left, left, true
		if row[cs] < thr {
			nl, nr := 0, 0
			for nl+nr+1 < tw && row[((cs-nl-1)%tw+tw)%tw] < thr {
				nl++
			}
			for nl+nr+1 < tw && row[(cs+nr+1)%tw] < thr {
				nr++
			}
			b.Left = (wcol - float64(nl)) / float64(tw)
			b.Right = (wcol + float64(nr+1)) / float64(tw)
		}
	}
	return b
}
