package superres

import "math"

// gateTpl is the gate's matched filter: zero-mean reference window + its norm,
// plus SEGMENT sub-templates for the per-hit consistency check (see segMatch).
type gateTpl struct {
	data []float64
	L    int
	norm float64
	rms  float64
	segs []gateSeg
}

// gateSeg is one template segment: re-zero-meaned shape data + its own norm,
// share of the total template energy, and its raw level relative to the window
// mean (relMean) — dead/flat segments discriminate by LEVEL, not shape.
type gateSeg struct {
	a, len  int
	data    []float64
	norm    float64
	share   float64
	relMean float64
}

type hit struct {
	loc   int
	score float64
	delta float64
}

// gateTemplate builds the zero-mean unit-referenced matched filter for [lo,hi),
// with per-segment sub-templates for the consistency check. Mirrors srGateTemplate.
func gateTemplate(ref []float32, lo, hi int) *gateTpl {
	L := hi - lo
	if L < 4 {
		return nil
	}
	data := make([]float64, L)
	mean := 0.0
	for i := 0; i < L; i++ {
		mean += float64(ref[lo+i])
	}
	mean /= float64(L)
	ss := 0.0
	for i := 0; i < L; i++ {
		d := float64(ref[lo+i]) - mean
		data[i] = d
		p := d * d // materialize (no FMA fuse)
		ss += p
	}
	norm := math.Sqrt(ss)
	if !(norm > 0) {
		return nil
	}
	// Segments: ~8 across the gate, each ≥24 samples (shorter is too noisy).
	nseg := L / 24
	if nseg > 8 {
		nseg = 8
	}
	if nseg < 1 {
		nseg = 1
	}
	var segs []gateSeg
	for s := 0; s < nseg; s++ {
		a := s * L / nseg
		b := (s + 1) * L / nseg
		sl := b - a
		if sl < 8 {
			continue
		}
		sm := 0.0
		for i := a; i < b; i++ {
			sm += float64(ref[lo+i])
		}
		sm /= float64(sl)
		sdata := make([]float64, sl)
		sss := 0.0
		for i := 0; i < sl; i++ {
			d := float64(ref[lo+a+i]) - sm
			sdata[i] = d
			p := d * d // materialize
			sss += p
		}
		den := ss
		if den == 0 {
			den = 1
		}
		segs = append(segs, gateSeg{a: a, len: sl, data: sdata, norm: math.Sqrt(sss), share: sss / den, relMean: sm - mean})
	}
	return &gateTpl{data: data, L: L, norm: norm, rms: math.Sqrt(ss / float64(L)), segs: segs}
}

// segMatch verifies one candidate hit against the template's segments: every
// segment must sit at the template's LEVEL (relative to the window mean, gain-
// scaled — flat plateaus discriminate by level), and every segment with real
// shape energy must correlate ≥0.65. A partial overlap or a lookalike matches
// only where the energy is concentrated — the global energy-weighted NCC cannot
// tell those apart, and depositing them contaminates the stack. Mirrors
// srSegMatch (thresholds measured on the adversarial 50-family corpus).
func (t *gateTpl) segMatch(sig []uint8, loc int) bool {
	if len(t.segs) < 2 {
		return true // nothing to cross-check
	}
	L := t.L
	wm := 0.0
	for i := 0; i < L; i++ {
		wm += float64(sig[loc+i])
	}
	wm /= float64(L)
	wss := 0.0
	for i := 0; i < L; i++ {
		d := float64(sig[loc+i]) - wm
		p := d * d // materialize
		wss += p
	}
	gGain := 1.0
	if t.rms > 0 {
		gGain = math.Sqrt(wss/float64(L)) / t.rms
	}
	if gGain < 0.5 {
		gGain = 0.5
	} else if gGain > 2 {
		gGain = 2
	}
	lvlTol := 0.25 * gGain * t.rms
	if lvlTol < 5 {
		lvlTol = 5
	}
	for gi := range t.segs {
		g := &t.segs[gi]
		sm := 0.0
		for i := 0; i < g.len; i++ {
			sm += float64(sig[loc+g.a+i])
		}
		sm /= float64(g.len)
		if math.Abs((sm-wm)-gGain*g.relMean) > lvlTol {
			return false
		}
		if g.share < 0.02 {
			continue // dead segment: level checked above, no shape to correlate
		}
		dot, ss := 0.0, 0.0
		for i := 0; i < g.len; i++ {
			s := float64(sig[loc+g.a+i]) - sm
			p := g.data[i] * s // materialize
			dot += p
			q := s * s
			ss += q
		}
		den := g.norm * math.Sqrt(ss)
		if !(den > 0) || dot/den < 0.65 {
			return false
		}
	}
	return true
}

// ambientMax measures the reference record's own ambient similarity to the gate
// template: the max off-gate local-maximum NCC below 0.93 (≥0.93 = genuine
// periodic repeats, which must keep stacking). Mirrors srAmbientMax.
func (st *Stack) ambientMax(ref []float32) float64 {
	t := st.gtpl
	L, n := t.L, st.N
	best, prev, prev2 := 0.0, -2.0, -2.0
	for loc := 0; loc <= n-L; loc++ {
		mean := 0.0
		for i := 0; i < L; i++ {
			mean += float64(ref[loc+i])
		}
		mean /= float64(L)
		dot, ss := 0.0, 0.0
		for i := 0; i < L; i++ {
			s := float64(ref[loc+i]) - mean
			p := t.data[i] * s // materialize
			dot += p
			q := s * s
			ss += q
		}
		den := t.norm * math.Sqrt(ss)
		sc := 0.0
		if den > 0 {
			sc = dot / den
		}
		if prev > prev2 && prev >= sc {
			ploc := loc - 1
			d := ploc - st.GateLo
			if d < 0 {
				d = -d
			}
			if d >= L>>1 && prev < 0.93 && prev > best {
				best = prev
			}
		}
		prev2 = prev
		prev = sc
	}
	return best
}

// detectPeriod returns the fundamental period (samples) of ref[lo:hi) via
// normalized autocorrelation — the first local peak above 0.5 — or 0 if not
// clearly periodic. Mirrors srDetectPeriod.
func detectPeriod(ref []float32, lo, hi int) int {
	W := hi - lo
	if W < 32 {
		return 0
	}
	mean := 0.0
	for i := lo; i < hi; i++ {
		mean += float64(ref[i])
	}
	mean /= float64(W)
	x := make([]float64, W)
	for i := 0; i < W; i++ {
		x[i] = float64(ref[lo+i]) - mean
	}
	minLag, maxLag := 8, W>>1
	prev, rising, dipped := -2.0, false, false
	for lag := minLag; lag <= maxLag; lag++ {
		dot, ea, eb := 0.0, 0.0, 0.0
		m := W - lag
		for i := 0; i < m; i++ {
			a, b := x[i], x[i+lag]
			p := a * b
			dot += p
			qa := a * a
			ea += qa
			qb := b * b
			eb += qb
		}
		den := math.Sqrt(ea * eb)
		r := 0.0
		if den > 0 {
			r = dot / den
		}
		// A SQUARE stays highly self-correlated across its flat tops, so the raw
		// "first peak > 0.5" fires at lag ~8 (the main lobe) → a bogus tiny period.
		// Require the autocorrelation to DIP below 0.3 first (proof we crossed a
		// half-period); the first strong peak AFTER that is the true fundamental.
		if r < 0.3 {
			dipped = true
		}
		if dipped && rising && r < prev && prev > 0.5 {
			return lag - 1
		}
		rising = r > prev
		prev = r
	}
	return 0
}

// DetectPeriodU8 is the exported period probe for callers holding raw codes
// (the device panel): the fundamental period of sig[lo:hi) in samples, or 0.
func DetectPeriodU8(sig []uint8, lo, hi int) int {
	if lo < 0 {
		lo = 0
	}
	if hi > len(sig) {
		hi = len(sig)
	}
	if hi-lo < 32 {
		return 0
	}
	ref := make([]float32, hi)
	for i := lo; i < hi; i++ {
		ref[i] = float32(sig[i])
	}
	return detectPeriod(ref, lo, hi)
}

// gateInstall resizes the stack to an L·K gate grid, builds the gate template,
// and seeds the reference's own gate at fractional offset 0. gate = [gLo,gHi).
func (st *Stack) gateInstall(gLo, gHi int) bool {
	L := gHi - gLo
	st.Gated = true
	st.UserRef = true
	st.GateLo, st.GateHi, st.GridL = gLo, gHi, L
	st.Nbins = L * st.K
	st.StatLo, st.StatHi = 0, L
	st.Hits = 0
	for ch := 0; ch < 2; ch++ {
		C := &st.C[ch]
		C.sum = make([]float64, st.Nbins)
		C.sum2 = make([]float64, st.Nbins)
		C.cnt = make([]float64, st.Nbins)
		C.sumA = make([]float64, st.Nbins)
		C.cntA = make([]float64, st.Nbins)
	}
	st.gtpl = gateTemplate(st.C[st.Align].ref, gLo, gHi)
	// Self-calibrated floor: sit above the reference's own ambient lookalikes
	// (see ambientMax). Mirrors srGateInstall.
	if st.gtpl != nil {
		amb := st.ambientMax(st.C[st.Align].ref)
		base := st.MinMatch
		if base == 0 {
			base = 0.8
		}
		f := amb + 0.06
		if f > 0.92 {
			f = 0.92
		}
		if f < base {
			f = base
		}
		st.AdaptFloor = f
	}
	st.Hits = 1
	for ch := 0; ch < 2; ch++ {
		if st.C[ch].ref != nil {
			st.drizzleHit(ch, st.C[ch].ref, float64(gLo), true)
		}
	}
	st.Frames = 1
	st.Scores = append(st.Scores[:0], 1)
	st.Shifts = append(st.Shifts[:0], 0)
	return st.gtpl != nil
}

// gateFind matched-filters the gate template across the frame and returns every
// occurrence: NCC local maxima above the floor, L/2-separated, each with a
// parabolic sub-sample offset. R>0 bounds to trigger-predicted ±R; R=0 = whole
// frame. Mirrors srGateFind.
func (st *Stack) gateFind(sig []uint8, base, R int) []hit {
	t := st.gtpl
	if t == nil {
		return nil
	}
	L, data, tnorm, n := t.L, t.data, t.norm, st.N
	lo, hi := 0, n-L
	if R > 0 {
		c := st.GateLo + base
		lo = c - R
		if lo < 0 {
			lo = 0
		}
		hi = c + R
		if hi > n-L {
			hi = n - L
		}
	}
	if hi < lo {
		return nil
	}
	M := hi - lo + 1
	ncc := make([]float64, M)
	for loc := lo; loc <= hi; loc++ {
		mean := 0.0
		for i := 0; i < L; i++ {
			mean += float64(sig[loc+i])
		}
		mean /= float64(L)
		dot, ss := 0.0, 0.0
		for i := 0; i < L; i++ {
			s := float64(sig[loc+i]) - mean
			p := data[i] * s // materialize
			dot += p
			q := s * s
			ss += q
		}
		den := tnorm * math.Sqrt(ss)
		sc := 0.0
		if den > 0 {
			sc = dot / den
		}
		ncc[loc-lo] = sc
	}
	floor := st.AdaptFloor
	if floor == 0 {
		floor = st.MinMatch
	}
	if floor == 0 {
		floor = 0.8
	}
	minSep := L >> 1
	if minSep < 1 {
		minSep = 1
	}
	var hits []hit
	lastLoc := -(1 << 30)
	for k := 0; k < M; k++ {
		sc := ncc[k]
		if sc < floor {
			continue
		}
		if k > 0 && ncc[k-1] >= sc {
			continue
		}
		if k+1 < M && ncc[k+1] > sc {
			continue
		}
		loc := lo + k
		if loc-lastLoc < minSep {
			continue
		}
		if !t.segMatch(sig, loc) { // partial/mixed match → not a hit
			continue
		}
		delta := 0.0
		if k > 0 && k+1 < M {
			yl, y0, yr := ncc[k-1], sc, ncc[k+1]
			t2 := 2 * y0 // materialize (no FMA fuse of yl - 2*y0)
			den2 := yl - t2 + yr
			if den2 < 0 {
				num := 0.5 * (yl - yr)
				delta = num / den2
				if delta > 0.5 {
					delta = 0.5
				} else if delta < -0.5 {
					delta = -0.5
				}
			}
		}
		hits = append(hits, hit{loc: loc, score: sc, delta: delta})
		lastLoc = loc
	}
	return hits
}
