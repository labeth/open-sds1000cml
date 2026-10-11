// Package analysis is a TEMPORARY stand-in for open-sds/app/internal/analysis
// (Agent LIB's shared analysis package, being built in parallel). It follows
// the brief's API so the panel can be written and tested now; when LIB's
// branch is merged the panel imports the real package and this directory is
// deleted. Keep it small: it only has to behave sensibly, not be the reference.
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package analysis

import (
	"bytes"
	"math"
	"sort"

	"open-sds/app/internal/decode"
)

// Packet is one decoded message: a run of bytes between idle gaps (UART,
// SPI, the bit-rate protocols) or a START..STOP transaction (I2C).
type Packet struct {
	I0, I1   int
	Bytes    []byte
	ByteErr  []bool
	Addr     int
	HasAddr  bool
	Read     bool
	Complete bool
	Err      string
}

// Packets groups a decode's spans into packets. Complete means the packet is
// not cut by the record edges.
// TRLC-LINKS: REQ-SDS-214
func Packets(r decode.Result, recordLen int) []Packet {
	if !r.OK {
		return nil
	}
	if r.Proto == "i2c" {
		return i2cPackets(r, recordLen)
	}
	var out []Packet
	var cur *Packet
	last, byteLen := -1, 0.0
	for _, s := range r.Spans {
		if s.Kind != "data" && s.Kind != "frame-error" && s.Kind != "parity-error" {
			continue
		}
		d := float64(max(s.I1-s.I0, 1))
		if byteLen == 0 || d < byteLen {
			byteLen = d
		}
		if cur == nil || float64(s.I0-last) > 1.5*d {
			if cur != nil {
				cur.Complete = cur.Complete && true
			}
			out = append(out, Packet{I0: s.I0, Complete: float64(s.I0) > 1.5*d})
			cur = &out[len(out)-1]
		}
		cur.Bytes = append(cur.Bytes, byte(s.Val))
		cur.ByteErr = append(cur.ByteErr, s.Kind != "data")
		cur.I1 = s.I1
		last = s.I1
	}
	for i := range out {
		if float64(recordLen-out[i].I1) <= 1.5*byteLen {
			out[i].Complete = false
		}
	}
	return out
}

func i2cPackets(r decode.Result, recordLen int) []Packet {
	var out []Packet
	var cur *Packet
	for _, s := range r.Spans {
		switch s.Kind {
		case "start":
			out = append(out, Packet{I0: s.I0, I1: s.I1})
			cur = &out[len(out)-1]
			cur.Complete = true
		case "stop":
			if cur != nil {
				cur.I1 = s.I1
			}
			cur = nil
		case "addr":
			if cur == nil {
				out = append(out, Packet{I0: s.I0})
				cur = &out[len(out)-1]
			}
			cur.Addr, cur.HasAddr, cur.I1 = s.Val, true, s.I1
		case "rw":
			if cur != nil {
				cur.Read = s.Text == "R"
			}
		case "data", "nak", "ack":
			if cur == nil {
				out = append(out, Packet{I0: s.I0})
				cur = &out[len(out)-1]
			}
			if s.Kind == "data" {
				cur.Bytes = append(cur.Bytes, byte(s.Val))
				cur.ByteErr = append(cur.ByteErr, false)
			}
			cur.I1 = s.I1
		case "frame-error":
			if cur != nil {
				cur.Err, cur.Complete = s.Text, false
			}
		}
	}
	for i := range out {
		if out[i].I1 >= recordLen-1 || len(out[i].Bytes) == 0 && !out[i].HasAddr {
			out[i].Complete = false
		}
	}
	return out
}

// PacketList holds the listed packets still while the same messages repeat.
type PacketList struct {
	rows []Packet
	key  string
	sel  int
	selB []byte
}

// Update offers a capture's packets; only complete ones are listed. It
// returns whether the rows were replaced.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Update(ps []Packet) bool {
	var full []Packet
	for _, p := range ps {
		if p.Complete {
			full = append(full, p)
		}
	}
	uniq := map[string]bool{}
	var keys []string
	for _, p := range full {
		k := string(p.Bytes)
		if !uniq[k] {
			uniq[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0xff)
	}
	key := b.String()
	if l.rows != nil && key == l.key && len(full) > 0 {
		return false
	}
	l.rows, l.key = full, key
	l.sel = -1
	if l.selB != nil {
		for i, p := range full {
			if bytes.Equal(p.Bytes, l.selB) {
				l.sel = i
				break
			}
		}
	}
	if l.sel < 0 && len(full) > 0 {
		l.sel = 0
		l.selB = full[0].Bytes
	}
	return true
}

// Rows returns the listed packets.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Rows() []Packet { return l.rows }

// Select selects row i (clamped).
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Select(i int) {
	if len(l.rows) == 0 {
		l.sel = -1
		return
	}
	l.sel = min(max(i, 0), len(l.rows)-1)
	l.selB = l.rows[l.sel].Bytes
}

// Selected returns the selected row, -1 for none.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) Selected() int {
	if l.sel >= len(l.rows) {
		return -1
	}
	return l.sel
}

// FindMatch returns the first row from `from` in direction dir (+1/-1) that
// holds bytes, or -1.
// TRLC-LINKS: REQ-SDS-214
func (l *PacketList) FindMatch(b []byte, from, dir int) int {
	if len(b) == 0 || dir == 0 {
		return -1
	}
	for i := from; i >= 0 && i < len(l.rows); i += dir {
		if bytes.Contains(l.rows[i].Bytes, b) {
			return i
		}
	}
	return -1
}

// ---- eye ----

// EyeResult is the eye diagram's figures and density grid.
type EyeResult struct {
	BitRate, UI                float64
	EyeHeight, EyeWidth        float64 // codes, UI
	Crossing                   float64 // fraction 0..1, NaN when unresolved
	Rise, Fall                 float64
	TIERms, TIEPkPk            float64
	UIs                        float64
	SamplesPerUI               float64
	Grid                       []uint32
	Cols, Rows                 int
	Max                        uint32
	Bounded                    bool    // timing figures are bounds (edges fall between samples)
	Top, Base                  float64 // logic levels, codes
	BoxC0, BoxR0, BoxC1, BoxR1 int
	HasBox                     bool
	Captures                   int
}

// Eye accumulates captures of a data line folded at the bit period.
type Eye struct {
	cols, rows int
	grid       []uint32
	max        uint32
	tie        []float64
	uiSum      float64
	uiN        int
	uis        float64
	caps       int
	spu        float64
	hiS, hiS2  float64
	hiN        int
	loS, loS2  float64
	loN        int
	xS         float64
	xN         int
	top, base  float64
	bounded    bool
}

// NewEye returns an empty eye of cols x rows cells over 2 UI x codes 28..228.
// TRLC-LINKS: REQ-SDS-215
func NewEye(cols, rows int) *Eye {
	return &Eye{cols: max(cols, 4), rows: max(rows, 4), grid: make([]uint32, max(cols, 4)*max(rows, 4))}
}

// Reset clears the eye.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Reset() { *e = *NewEye(e.cols, e.rows) }

// Feed folds one capture of codes (sampleS apart); bitHintS is the decoder's
// bit time (0 = estimate).
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Feed(codes []float64, sampleS, bitHintS float64) {
	n := len(codes)
	if n < 8 || !(sampleS > 0) {
		return
	}
	lo, hi := codes[0], codes[0]
	for _, v := range codes {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if hi-lo < 8 {
		return
	}
	mid := (lo + hi) / 2
	hyH, hyL := mid+0.1*(hi-lo), mid-0.1*(hi-lo)
	var edges []float64
	st := codes[0] > mid
	for i := 1; i < n; i++ {
		if (!st && codes[i] > hyH) || (st && codes[i] < hyL) {
			j := i
			for j > 1 && (codes[j-1] > mid) != st {
				j--
			}
			a, b := codes[j-1], codes[j]
			f := 0.5
			if b != a {
				f = (mid - a) / (b - a)
			}
			edges = append(edges, (float64(j-1)+math.Min(math.Max(f, 0), 1))*sampleS)
			st = !st
		}
	}
	ui := bitHintS
	if !(ui > 0) {
		ui = math.Inf(1)
		for i := 1; i < len(edges); i++ {
			ui = math.Min(ui, edges[i]-edges[i-1])
		}
	}
	if math.IsInf(ui, 0) || ui < 1.2*sampleS || float64(n)*sampleS < 2*ui {
		return
	}
	e.top, e.base = hi, lo
	e.spu = ui / sampleS
	if e.spu < 2 {
		e.bounded = true
	}
	i0 := 0
	for i0 < len(edges) {
		i1 := i0
		for i1+1 < len(edges) && edges[i1+1]-edges[i1] <= 12*ui {
			i1++
		}
		if ne := i1 - i0 + 1; ne >= 2 {
			e0 := edges[i0]
			var km, em float64
			ks := make([]float64, ne)
			for j := 0; j < ne; j++ {
				ks[j] = math.Round((edges[i0+j] - e0) / ui)
				km += ks[j]
				em += edges[i0+j]
			}
			km /= float64(ne)
			em /= float64(ne)
			var sxx, sxy float64
			for j := 0; j < ne; j++ {
				sxx += (ks[j] - km) * (ks[j] - km)
				sxy += (ks[j] - km) * (edges[i0+j] - em)
			}
			b := ui
			if ne >= 3 && sxx > 0 && math.Abs(sxy/sxx/ui-1) < 0.05 {
				b = sxy / sxx
			}
			a := em - b*km
			e.uiSum += b * float64(ne)
			e.uiN += ne
			for j := 0; j < ne; j++ {
				e.tie = append(e.tie, edges[i0+j]-(a+b*ks[j]))
			}
			if len(e.tie) > 4096 {
				e.tie = e.tie[len(e.tie)-4096:]
			}
			ta, tb := e0-b, edges[i1]+2*b
			ja, jb := max(0, int(math.Ceil(ta/sampleS))), min(n-1, int(math.Floor(tb/sampleS)))
			for j := ja; j <= jb; j++ {
				// the window is -1..+1 UI with the bit centre in the middle: an
				// edge falls at +/-0.5 UI (u = 0.5 or 1.5), a bit centre at u = 1
				ph := (float64(j)*sampleS-a)/b + 0.5
				u := ph - 2*math.Floor(ph/2) // 0..2
				c := min(e.cols-1, int(u/2*float64(e.cols)))
				v := codes[j]
				r := int((228 - v) / 200 * float64(e.rows))
				r = min(max(r, 0), e.rows-1)
				e.grid[r*e.cols+c]++
				if e.grid[r*e.cols+c] > e.max {
					e.max = e.grid[r*e.cols+c]
				}
				fu := u - math.Floor(u)
				if fu < 0.15 || fu > 0.85 { // bit centres: the logic levels
					if v > mid {
						e.hiS, e.hiS2, e.hiN = e.hiS+v, e.hiS2+v*v, e.hiN+1
					} else {
						e.loS, e.loS2, e.loN = e.loS+v, e.loS2+v*v, e.loN+1
					}
				} else if math.Abs(fu-0.5) < 0.1 { // edges: the crossing level
					if v > lo+0.2*(hi-lo) && v < hi-0.2*(hi-lo) {
						e.xS += (v - lo) / (hi - lo)
						e.xN++
					}
				}
			}
			e.uis += (tb - ta) / b
		}
		i0 = i1 + 1
	}
	e.caps++
}

// Result returns the figures and the grid.
// TRLC-LINKS: REQ-SDS-215
func (e *Eye) Result() EyeResult {
	r := EyeResult{Grid: e.grid, Cols: e.cols, Rows: e.rows, Max: e.max, UIs: e.uis, Captures: e.caps,
		SamplesPerUI: e.spu, Crossing: math.NaN(), Bounded: e.bounded, Top: e.top, Base: e.base,
		Rise: math.NaN(), Fall: math.NaN()}
	if e.uiN > 0 {
		r.UI = e.uiSum / float64(e.uiN)
		r.BitRate = 1 / r.UI
	}
	var s2, tmin, tmax float64
	tmin, tmax = math.Inf(1), math.Inf(-1)
	for _, t := range e.tie {
		s2 += t * t
		tmin, tmax = math.Min(tmin, t), math.Max(tmax, t)
	}
	if len(e.tie) > 0 {
		r.TIERms = math.Sqrt(s2 / float64(len(e.tie)))
		r.TIEPkPk = tmax - tmin
		if r.UI > 0 {
			r.EyeWidth = math.Max(0, 1-r.TIEPkPk/r.UI)
		}
	}
	if e.hiN > 1 && e.loN > 1 {
		hm, lm := e.hiS/float64(e.hiN), e.loS/float64(e.loN)
		hs := math.Sqrt(math.Max(0, e.hiS2/float64(e.hiN)-hm*hm))
		ls := math.Sqrt(math.Max(0, e.loS2/float64(e.loN)-lm*lm))
		r.EyeHeight = (hm - 3*hs) - (lm + 3*ls)
		if r.EyeHeight > 0 {
			r.HasBox = true
			r.BoxR0 = min(max(int((228-(hm-3*hs))/200*float64(e.rows)), 0), e.rows-1)
			r.BoxR1 = min(max(int((228-(lm+3*ls))/200*float64(e.rows)), 0), e.rows-1)
			half := r.EyeWidth / 2
			r.BoxC0 = int((0.5 - half/2) * float64(e.cols))
			r.BoxC1 = int((0.5 + half/2) * float64(e.cols))
		}
	}
	if e.xN >= 20 && !e.bounded {
		r.Crossing = e.xS / float64(e.xN)
	}
	return r
}

// ---- spectral peaks ----

// Peak is one spectral line.
type Peak struct {
	Freq, DB, RelDB float64
	Bin             int
	Harm            int
}

// Peaks returns up to max local maxima of db (one value per bin, binHz
// apart), largest first.
// TRLC-LINKS: REQ-SDS-216
func Peaks(db []float64, binHz float64, maxN int) []Peak {
	var out []Peak
	for k := 2; k < len(db)-2; k++ {
		if db[k] > db[k-1] && db[k] >= db[k+1] && db[k] > db[k-2] && db[k] >= db[k+2] {
			a, b, c := db[k-1], db[k], db[k+1]
			d := 0.0
			if den := a - 2*b + c; den < 0 {
				d = 0.5 * (a - c) / den
			}
			if math.Abs(d) > 1 {
				d = 0
			}
			out = append(out, Peak{Freq: (float64(k) + d) * binHz, DB: db[k], Bin: k})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DB > out[j].DB })
	if len(out) > 0 {
		top := out[0].DB
		var kept []Peak
		for _, p := range out {
			if p.DB < top-60 {
				break
			}
			p.RelDB = p.DB - top
			kept = append(kept, p)
		}
		out = kept
	}
	if len(out) > maxN {
		out = out[:maxN]
	}
	return out
}

// Harmonics marks the peaks that are harmonics of f0 (Harm = n) and returns
// the THD in percent from the harmonics found (NaN with none).
// TRLC-LINKS: REQ-SDS-216
func Harmonics(ps []Peak, f0 float64) ([]Peak, float64) {
	out := append([]Peak(nil), ps...)
	if !(f0 > 0) || len(out) == 0 {
		return out, math.NaN()
	}
	var fund float64
	var sum float64
	nh := 0
	for i, p := range out {
		n := math.Round(p.Freq / f0)
		if n >= 1 && n <= 15 && math.Abs(p.Freq/f0-n) < 0.04 {
			out[i].Harm = int(n)
			amp := math.Pow(10, p.DB/20)
			if n == 1 {
				fund = math.Max(fund, amp)
			} else {
				sum += amp * amp
				nh++
			}
		}
	}
	if fund == 0 || nh == 0 {
		return out, math.NaN()
	}
	return out, 100 * math.Sqrt(sum) / fund
}

// ---- statistics ----

// Stats is a running mean, extremes and standard deviation (Welford).
type Stats struct {
	n        int
	mean, m2 float64
	min, max float64
}

// Add adds a value; NaN and Inf are ignored.
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Add(v float64) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	s.n++
	if s.n == 1 {
		s.min, s.max = v, v
	}
	s.min, s.max = math.Min(s.min, v), math.Max(s.max, v)
	d := v - s.mean
	s.mean += d / float64(s.n)
	s.m2 += d * (v - s.mean)
}

// N is the count. TRLC-LINKS: REQ-SDS-217
func (s *Stats) N() int { return s.n }

// Mean is the mean. TRLC-LINKS: REQ-SDS-217
func (s *Stats) Mean() float64 { return s.mean }

// Min is the minimum. TRLC-LINKS: REQ-SDS-217
func (s *Stats) Min() float64 { return s.min }

// Max is the maximum. TRLC-LINKS: REQ-SDS-217
func (s *Stats) Max() float64 { return s.max }

// Sdev is the sample standard deviation (0 below two values).
// TRLC-LINKS: REQ-SDS-217
func (s *Stats) Sdev() float64 {
	if s.n < 2 {
		return 0
	}
	return math.Sqrt(s.m2 / float64(s.n-1))
}

// Reset clears the statistics. TRLC-LINKS: REQ-SDS-217
func (s *Stats) Reset() { *s = Stats{} }

// ---- references ----

// RefDiff compares a live trace with a reference (division units, NaN =
// gap): the largest |difference|, its RMS and the percentage within tolDiv.
// TRLC-LINKS: REQ-SDS-218
func RefDiff(live, ref []float64, tolDiv float64) (maxAbs, rms, pctWithin float64) {
	n := min(len(live), len(ref))
	cnt, in := 0, 0
	var s2 float64
	for i := 0; i < n; i++ {
		a, b := live[i], ref[i]
		if math.IsNaN(a) || math.IsNaN(b) {
			continue
		}
		d := math.Abs(a - b)
		maxAbs = math.Max(maxAbs, d)
		s2 += d * d
		cnt++
		if d <= tolDiv {
			in++
		}
	}
	if cnt == 0 {
		return math.NaN(), math.NaN(), math.NaN()
	}
	return maxAbs, math.Sqrt(s2 / float64(cnt)), 100 * float64(in) / float64(cnt)
}

// Repeats reports whether cur lies within tolDiv of prev's +/-slack-sample
// min/max envelope everywhere (the mask build's rule).
// TRLC-LINKS: REQ-SDS-218
func Repeats(prev, cur []float64, tolDiv float64, slack int) bool {
	n := min(len(prev), len(cur))
	if n == 0 {
		return false
	}
	for i := 0; i < n; i++ {
		if math.IsNaN(cur[i]) {
			continue
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for j := max(0, i-slack); j <= min(n-1, i+slack); j++ {
			if !math.IsNaN(prev[j]) {
				lo, hi = math.Min(lo, prev[j]), math.Max(hi, prev[j])
			}
		}
		if math.IsInf(lo, 0) {
			continue
		}
		if cur[i] < lo-tolDiv || cur[i] > hi+tolDiv {
			return false
		}
	}
	return true
}
