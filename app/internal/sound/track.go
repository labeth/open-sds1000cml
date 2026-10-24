// ENGMODEL-OWNER-UNIT: FU-APP-SOUND
package sound

import (
	"math"
	"sort"
)

// Tracking over time: the tuner's smoothed reading and its pitch strip, the
// V/oct table (a VCO's pitch against its CV) and the drift log.

// Tuner smooths pitch readings: the median of the last few (three fast, nine
// slow), reset by a change of more than 50 cents or by silence.
type Tuner struct {
	Slow   bool
	recent []float64
}

// Add takes a reading and returns the smoothed pitch (0 unvoiced).
// TRLC-LINKS: REQ-SDS-267
func (t *Tuner) Add(p Pitch) float64 {
	if !p.OK {
		t.recent = t.recent[:0]
		return 0
	}
	if len(t.recent) > 0 && math.Abs(Cents(p.Hz, t.recent[len(t.recent)-1])) > 50 {
		t.recent = t.recent[:0]
	}
	k := 3
	if t.Slow {
		k = 9
	}
	t.recent = append(t.recent, p.Hz)
	if len(t.recent) > k {
		t.recent = t.recent[len(t.recent)-k:]
	}
	// The median of at most nine readings, sorted on the stack: no copy on
	// the heap a reading.
	var s [9]float64
	n := copy(s[:], t.recent)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	mid := 0.5 * float64(n-1)
	lo := int(mid)
	hi := min(lo+1, n-1)
	f := mid - float64(lo)
	return s[lo]*(1-f) + s[hi]*f
}

// VOctPoint is one tracking point: the CV and the pitch it gave.
type VOctPoint struct {
	V  float64 `json:"v"`
	Hz float64 `json:"hz"`
}

// VOctRow is a table row.
type VOctRow struct {
	V, Hz      float64
	Note       string
	ErrCents   float64 // against exactly 1 V/oct from the lowest point
	ResidCents float64 // against the best-fit line
}

// VOctFit is the table and what it says to trim.
type VOctFit struct {
	OK          bool
	Rows        []VOctRow
	OctPerV     float64 // fitted
	ScaleCents  float64 // the fit's error a volt: 1200 (OctPerV - 1)
	TrimPct     float64 // scale the CV path by this to reach 1 V/oct
	HFDevCents  float64 // the top point against the fit of the lower half
	SpanOctaves float64
}

// FitVOct reads the points (any order; A4 = a4 names them).
// TRLC-LINKS: REQ-SDS-267
func FitVOct(pts []VOctPoint, a4 float64) VOctFit {
	f := VOctFit{}
	var ps []VOctPoint
	for _, p := range pts {
		if p.Hz > 0 && finite(p.V) && finite(p.Hz) {
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].V < ps[j].V })
	if len(ps) == 0 {
		return f
	}
	v0, h0 := ps[0].V, ps[0].Hz
	for _, p := range ps {
		ideal := h0 * math.Pow(2, p.V-v0)
		f.Rows = append(f.Rows, VOctRow{V: p.V, Hz: p.Hz, Note: NoteOf(p.Hz, a4).String(), ErrCents: Cents(p.Hz, ideal)})
	}
	if len(ps) < 2 || !(ps[len(ps)-1].V-ps[0].V > 0.05) {
		return f
	}
	slope, icpt := lineFit(ps)
	f.OctPerV = slope
	f.ScaleCents = 1200 * (slope - 1)
	if slope > 0 {
		f.TrimPct = 100 * (1/slope - 1)
	}
	for i, p := range ps {
		f.Rows[i].ResidCents = 1200 * (math.Log2(p.Hz) - (icpt + slope*p.V))
	}
	f.SpanOctaves = math.Log2(ps[len(ps)-1].Hz / ps[0].Hz)
	if len(ps) >= 4 {
		lower := ps[:(len(ps)+1)/2]
		s2, i2 := lineFit(lower)
		top := ps[len(ps)-1]
		f.HFDevCents = 1200 * (math.Log2(top.Hz) - (i2 + s2*top.V))
	}
	f.OK = true
	return f
}

// lineFit fits log2(Hz) = icpt + slope*V by least squares.
// TRLC-LINKS: REQ-SDS-267
func lineFit(ps []VOctPoint) (slope, icpt float64) {
	var n, sx, sy, sxx, sxy float64
	for _, p := range ps {
		y := math.Log2(p.Hz)
		n++
		sx += p.V
		sy += y
		sxx += p.V * p.V
		sxy += p.V * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, sy / n
	}
	slope = (n*sxy - sx*sy) / den
	return slope, (sy - slope*sx) / n
}

// VOctAuto takes a point by itself once the CV and the pitch have held still
// for HoldS seconds at a CV at least StepV from the last point taken.
type VOctAuto struct {
	HoldS float64
	StepV float64
	hist  []vsample
	lastV float64
	have  bool
}

type vsample struct{ t, v, hz float64 }

// Add feeds a reading at t seconds; it returns a point when one is taken.
// TRLC-LINKS: REQ-SDS-267
func (a *VOctAuto) Add(t, cv, hz float64) (VOctPoint, bool) {
	hold, step := a.HoldS, a.StepV
	if !(hold > 0) {
		hold = 0.5
	}
	if !(step > 0) {
		step = 0.05
	}
	if !(hz > 0) {
		a.hist = a.hist[:0]
		return VOctPoint{}, false
	}
	a.hist = append(a.hist, vsample{t, cv, hz})
	for len(a.hist) > 1 && t-a.hist[0].t > hold {
		a.hist = a.hist[1:]
	}
	if t-a.hist[0].t < hold*0.8 || len(a.hist) < 3 {
		return VOctPoint{}, false
	}
	vlo, vhi, flo, fhi := a.hist[0].v, a.hist[0].v, a.hist[0].hz, a.hist[0].hz
	var sv, sf float64
	for _, s := range a.hist {
		vlo, vhi = math.Min(vlo, s.v), math.Max(vhi, s.v)
		flo, fhi = math.Min(flo, s.hz), math.Max(fhi, s.hz)
		sv += s.v
		sf += s.hz
	}
	if vhi-vlo > 0.006 || Cents(fhi, flo) > 4 {
		return VOctPoint{}, false
	}
	p := VOctPoint{V: sv / float64(len(a.hist)), Hz: sf / float64(len(a.hist))}
	if a.have && math.Abs(p.V-a.lastV) < step {
		return VOctPoint{}, false
	}
	a.lastV, a.have = p.V, true
	a.hist = a.hist[:0]
	return p, true
}

// Drift logs one pitch a second: the median of the second's readings.
type Drift struct {
	T0     float64
	Points []DriftPoint
	cur    []float64
	curSec int
	base   float64
}

// DriftPoint is one second's pitch and its cents from the first.
type DriftPoint struct {
	T     float64 `json:"t"`
	Hz    float64 `json:"hz"`
	Cents float64 `json:"cents"`
}

// driftMax bounds the log: two hours.
const driftMax = 7200

// Add feeds a reading at t seconds (hz 0: none).
// TRLC-LINKS: REQ-SDS-267
func (d *Drift) Add(t, hz float64) {
	if len(d.Points) == 0 && len(d.cur) == 0 {
		d.T0, d.curSec = t, 0
	}
	sec := int(math.Floor(t - d.T0))
	if sec != d.curSec && len(d.cur) > 0 {
		m := median(d.cur)
		if d.base == 0 {
			d.base = m
		}
		d.Points = append(d.Points, DriftPoint{T: float64(d.curSec), Hz: m, Cents: Cents(m, d.base)})
		if len(d.Points) > driftMax {
			d.Points = d.Points[len(d.Points)-driftMax:]
		}
		d.cur = d.cur[:0]
	}
	d.curSec = sec
	if hz > 0 {
		d.cur = append(d.cur, hz)
	}
}
