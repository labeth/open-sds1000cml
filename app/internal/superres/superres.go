// Package superres ports the reference-locked stack-and-crunch core from
// internal/web/superres.js to Go for the standalone scope, algorithm-faithful so
// the device and the web CONVERGE to the same stack (see the golden-vector parity
// test). v1 scope: the interp kernel and the reference-locked (SINGLE→UTILITY)
// matching path only — the auto srAlign path, drizzle/cubic kernels, dither and
// model-fit stay web-only (see app/docs/superres-device-plan.md).
//
// Numeric parity (must survive the golden-vector test):
//   - jsRound mirrors JS Math.round (half toward +Inf), not Go's round-half-away.
//   - Inner-product terms are materialized into named float64 locals so Go's FMA
//     on amd64 can't fuse them (V8/ARM don't fuse) — keeps host CI == on-device.
//   - float32 for the reference + drift-normalized frame + mean; float64 for
//     accumulators/scores. Single-threaded, fixed feed order, one accumulator/bin.
package superres

import (
	"math"
	"strconv"
)

// jsRound mirrors JS Math.round (round half UP, toward +Inf): base/shift are
// routinely negative x.5 where Go's math.Round (half away from zero) diverges.
func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }

type chanState struct {
	sum, sum2, cnt, sumA, cntA []float64
	ref                        []float32 // reference for the drift fit
	vpc, offV                  float64
	clipSkips                  int
}

type template struct {
	data      []float64
	lo, hi, L int
	norm      float64
}

// Stack is one accumulation state: n input samples drizzled onto n*K fine bins.
type Stack struct {
	N, K, Nbins int
	C           [2]chanState
	Align       int // channel alignment/matching runs on
	Frames      int
	Rejected    int
	Clipped     int
	Scores      []float64
	Shifts      []float64
	RefEdgeX    float64
	StatLo      int
	StatHi      int
	SampleS     float64
	UserRef     bool
	tpl         *template
	MaxShift    int     // translation budget (samples) for the locate; 0 → default 64
	MinMatch    float64 // match-score cut; 0 → default 0.8

	// Reference-lock v2: gated multi-hit sub-sample stacker (see superres.js).
	// The gate [GateLo,GateHi) is the deterministic feature; the grid is GridL·K
	// (not N·K); Hits counts occurrences (one frame yields many on a repetitive
	// signal). SearchR bounds the per-frame search to trigger-predicted ±R (0 =
	// whole frame).
	Gated      bool
	Hits       int
	GateLo     int
	GateHi     int
	GridL      int
	SearchR    int
	AdaptFloor float64 // self-calibrated hit floor (≥ MinMatch): above the
	// reference's own ambient lookalikes, so junk that merely resembles a
	// low-information gate can't stack. 0 = uncalibrated (use MinMatch).
	gtpl *gateTpl
}

// New allocates a stack: n input samples → n*K fine bins per channel.
func New(n, K int) *Stack {
	nb := n * K
	mk := func() chanState {
		return chanState{
			sum: make([]float64, nb), sum2: make([]float64, nb), cnt: make([]float64, nb),
			sumA: make([]float64, nb), cntA: make([]float64, nb),
			vpc: 1.0 / 32, offV: 0,
		}
	}
	return &Stack{
		N: n, K: K, Nbins: nb,
		C:        [2]chanState{mk(), mk()},
		RefEdgeX: -1, StatLo: 0, StatHi: n,
		MaxShift: 64, MinMatch: 0.8,
	}
}

// Clipped reports whether a channel is railed (≥0.5% of samples pinned at a rail).
func Clipped(sig []uint8) bool {
	lo, hi := 255, 0
	for _, v := range sig {
		x := int(v)
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	if lo > 6 && hi < 253 {
		return false
	}
	nlo, nhi, n := 0, 0, len(sig)
	for _, v := range sig {
		x := int(v)
		if x <= lo+2 {
			nlo++
		}
		if x >= hi-2 {
			nhi++
		}
	}
	return (lo <= 6 && nlo*200 > n) || (hi >= 253 && nhi*200 > n)
}

// buildTemplate isolates the reference's distinguishing content — the active
// (non-flat) region after the trigger transition — as a zero-mean template. See
// srBuildTemplate in superres.js; general-purpose (burst, UART byte, glitch…).
func buildTemplate(ref []float32, n int, edgeX float64, valid int) *template {
	hi0 := n
	if valid > 0 && valid <= n {
		hi0 = valid
	}
	lo0 := 0
	if edgeX >= 0 {
		lo0 = jsRound(edgeX) + 16
		if lo0 > hi0-1 {
			lo0 = hi0 - 1
		}
	}
	if hi0-lo0 < 16 {
		return nil
	}
	const W = 12
	h := W >> 1
	mr := make([]float64, hi0)
	for i := lo0; i < hi0; i++ {
		a, b := i-h, i+h+1
		if a < lo0 {
			a = lo0
		}
		if b > hi0 {
			b = hi0
		}
		mn, mx := float32(255), float32(0)
		for j := a; j < b; j++ {
			v := ref[j]
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
		}
		mr[i] = float64(mx - mn)
	}
	sorted := append([]float64(nil), mr[lo0:hi0]...)
	sortFloat(sorted)
	floor := sorted[int(float64(len(sorted))*0.2)]
	peak := sorted[len(sorted)-1]
	if peak-floor < 6 {
		return nil
	}
	thr := floor + math.Max(4, 0.2*(peak-floor))
	lo, hi := -1, -1
	for i := lo0; i < hi0; i++ {
		if mr[i] < thr {
			continue
		}
		if lo < 0 {
			lo = i
		}
		hi = i
	}
	if lo < 0 || hi-lo < 8 {
		lo, hi = lo0, hi0-1
	} else {
		if lo-h < lo0 {
			lo = lo0
		} else {
			lo -= h
		}
		if hi+h >= hi0 {
			hi = hi0 - 1
		} else {
			hi += h
		}
	}
	L := hi - lo + 1
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
	return &template{data: data, lo: lo, hi: hi, L: L, norm: norm}
}

type matchResult struct {
	shift float64
	score float64
	ambig bool
}

// matchLocate slides the template over the frame within base±R (the trigger-
// predicted position ± translation budget) and returns the best sub-window match.
func (st *Stack) matchLocate(sig []uint8, base, R int) (matchResult, bool) {
	t := st.tpl
	if t == nil {
		return matchResult{}, false
	}
	L, data, tnorm := t.L, t.data, t.norm
	center := t.lo + base
	lo, hi := center-R, center+R
	if lo < 0 {
		lo = 0
	}
	if hi > st.N-L {
		hi = st.N - L
	}
	if hi < lo {
		return matchResult{}, false
	}
	best, bestLoc, second := -2.0, center, -2.0
	half := L >> 1
	for loc := lo; loc <= hi; loc++ {
		mean := 0.0
		for i := 0; i < L; i++ {
			mean += float64(sig[loc+i])
		}
		mean /= float64(L)
		dot, ss := 0.0, 0.0
		for i := 0; i < L; i++ {
			s := float64(sig[loc+i]) - mean
			p := data[i] * s // materialize (no FMA fuse)
			dot += p
			q := s * s
			ss += q
		}
		den := tnorm * math.Sqrt(ss)
		sc := 0.0
		if den > 0 {
			sc = dot / den
		}
		if sc > best {
			if abs(loc-bestLoc) > half {
				second = best
			}
			best = sc
			bestLoc = loc
		} else if sc > second && abs(loc-bestLoc) > half {
			second = sc
		}
	}
	return matchResult{shift: float64(bestLoc - t.lo), score: best, ambig: second > 0.9*best}, true
}

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

// drizzleHit stacks one aligned occurrence onto the L·K grid by INTERP-resampling
// (the same kernel as accumCh): grid bin b reads the frame at sub-sample position
// p + b/K, linearly interpolated. Every fine bin gets a contribution from every
// hit — gap-free and staircase-free. odd routes it into the A half-stack. Mirrors
// srDrizzleHit (interp/linear branch — the device kernel).
func (st *Stack) drizzleHit(ch int, sig []float32, p float64, odd bool) {
	K, G, n := st.K, st.Nbins, st.N
	C := &st.C[ch]
	invK := 1.0 / float64(K)
	for b := 0; b < G; b++ {
		t := p + float64(b)*invK
		i0 := int(math.Floor(t))
		if i0 < 0 || i0+1 >= n {
			continue
		}
		w := t - float64(i0)
		a := float64(sig[i0]) * (1 - w)
		bb := float64(sig[i0+1]) * w
		v := a + bb
		p2 := v * v // materialize (no FMA fuse)
		C.sum[b] += v
		C.sum2[b] += p2
		C.cnt[b]++
		if odd {
			C.sumA[b] += v
			C.cntA[b]++
		}
	}
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

// SeedRef locks the frozen frame as the reference with an AUTO gate (active
// region, narrowed to one period if periodic). See SeedRefGate for manual gates.
func (st *Stack) SeedRef(sig1, sig2 []uint8, edgeX float64) bool {
	return st.SeedRefGate(sig1, sig2, edgeX, -1, -1)
}

// SeedRefGate locks the frozen frame as the reference and installs the gate. A
// gateLo<gateHi with gateLo>=0 is a manual gate; otherwise the gate is
// auto-derived. Returns false if the frame is unusable (flat/clipped/no feature).
func (st *Stack) SeedRefGate(sig1, sig2 []uint8, edgeX float64, gateLo, gateHi int) bool {
	sigs := [2][]uint8{sig1, sig2}
	alignSig := sigs[st.Align]
	if len(alignSig) < st.N || Clipped(alignSig) {
		return false
	}
	lo, hi := 255, 0
	for i := 0; i < st.N; i++ {
		v := int(alignSig[i])
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi-lo < 12 {
		return false
	}
	st.RefEdgeX = edgeX
	for ch := 0; ch < 2; ch++ {
		s := sigs[ch]
		if len(s) < st.N {
			continue
		}
		ref := make([]float32, st.N)
		for i := 0; i < st.N; i++ {
			ref[i] = float32(s[i])
		}
		st.C[ch].ref = ref
	}
	var gLo, gHi int
	if gateHi > gateLo && gateLo >= 0 {
		// MANUAL gate (the on-screen view / dragged markers): use it EXACTLY. The
		// user picked this region — do not narrow it. A wide gate stays cheap
		// because Feed bounds the per-frame search; a repetitive wide gate still
		// multi-hits (it matches at every period offset).
		gLo, gHi = gateLo, gateHi
		if gLo < 0 {
			gLo = 0
		}
		if gHi > st.N {
			gHi = st.N
		}
	} else {
		// AUTO default: the active region, narrowed to ONE period when clearly
		// periodic — a fast, sensible default (multi-hit, cheap search).
		active := buildTemplate(st.C[st.Align].ref, st.N, st.RefEdgeX, st.N)
		if active == nil {
			return false
		}
		gLo, gHi = active.lo, active.hi+1
		if period := detectPeriod(st.C[st.Align].ref, gLo, gHi); period >= 16 && period < gHi-gLo {
			gHi = gLo + period
		}
	}
	if gHi-gLo < 4 {
		return false
	}
	return st.gateInstall(gLo, gHi)
}

// ResetKeepRef clears the accumulation and re-stacks the locked reference at
// shift 0 — a "start the stack over" that keeps the same match reference/template
// (the Reset softkey). Must be called on the stacker goroutine (mutates the
// accumulators), never concurrently with Feed.
func (st *Stack) ResetKeepRef() {
	for ch := range st.C {
		C := &st.C[ch]
		for i := range C.sum {
			C.sum[i], C.sum2[i], C.cnt[i], C.sumA[i], C.cntA[i] = 0, 0, 0, 0, 0
		}
	}
	st.Frames, st.Rejected, st.Clipped, st.Hits = 0, 0, 0, 0
	st.Scores, st.Shifts = st.Scores[:0], st.Shifts[:0]
	st.Hits = 1
	for ch := 0; ch < 2; ch++ {
		if st.C[ch].ref != nil {
			st.drizzleHit(ch, st.C[ch].ref, float64(st.GateLo), true) // re-seed the gate
		}
	}
	st.Frames = 1
	st.Scores = append(st.Scores, 1)
	st.Shifts = append(st.Shifts, 0)
}

// gainOffset fits g,b minimizing |ref − (g·sig[·+lag] + b)|² over [wLo,wHi).
func gainOffset(ref []float32, sig []uint8, lag, wLo, wHi int) (g, b float64) {
	lo := wLo
	if -lag > lo {
		lo = -lag
	}
	hi := wHi
	if len(ref) < hi {
		hi = len(ref)
	}
	if len(sig)-lag < hi {
		hi = len(sig) - lag
	}
	n := hi - lo
	if n < 16 {
		return 1, 0
	}
	var sr, ss, srr, srs float64
	for i := lo; i < hi; i++ {
		r := float64(ref[i])
		s := float64(sig[i+lag])
		sr += r
		ss += s
		p1 := r * r
		srr += p1
		p2 := r * s
		srs += p2
	}
	den := float64(n)*srr - sr*sr
	if den <= 0 {
		return 1, 0
	}
	g = (float64(n)*srs - sr*ss) / den
	if !(g > 0.1 && g < 10) {
		return 1, 0
	}
	return g, (ss - g*sr) / float64(n)
}

// accumCh resamples one aligned frame at every fine bin (interp kernel): bin b
// reads the frame at raw index b/K + shift, linearly interpolated. Odd frames
// also land in the A half-stack (odd/even split for the measured σ).
func (st *Stack) accumCh(ch int, sig []float32, shift float64) {
	K, nb, n := st.K, st.Nbins, st.N
	C := &st.C[ch]
	odd := (st.Frames & 1) == 1
	invK := 1.0 / float64(K)
	for bi := 0; bi < nb; bi++ {
		t := float64(bi)*invK + shift
		i0 := int(math.Floor(t))
		if i0 < 0 || i0+1 >= n {
			continue
		}
		w := t - float64(i0)
		a := float64(sig[i0]) * (1 - w)
		bb := float64(sig[i0+1]) * w
		v := a + bb
		p := v * v
		C.sum[bi] += v
		C.sum2[bi] += p
		C.cnt[bi]++
		if odd {
			C.sumA[bi] += v
			C.cntA[bi]++
		}
	}
}

// Feed runs the gated multi-hit pipeline for one frame: find EVERY occurrence of
// the gate feature, then sub-sample align + drift-normalize + drizzle each onto
// the L·K grid (both channels at the align channel's positions). Zero occurrences
// → the frame is rejected. Returns "stacked:<n>" | "rejected:<why>". SeedRef must
// have run first. Mirrors srGateFeed.
func (st *Stack) Feed(sig1, sig2 []uint8, edgeX float64) string {
	sigs := [2][]uint8{sig1, sig2}
	alignSig := sigs[st.Align]
	if len(alignSig) < st.N {
		return "rejected:short"
	}
	if Clipped(alignSig) {
		st.Clipped++
		st.Rejected++
		return "rejected:clip"
	}
	if !st.Gated || st.gtpl == nil {
		st.Rejected++
		return "rejected:no-ref"
	}
	base := 0
	if st.RefEdgeX >= 0 && edgeX >= 0 {
		base = jsRound(edgeX - st.RefEdgeX)
	}
	// Whole-frame multi-hit is O((N−L)·L). A gate narrowed to one period is small
	// and searches the whole frame cheaply; a wide aperiodic gate would be
	// seconds/frame on the device, so bound its search to trigger-predicted ±R
	// (it occurs once per frame at the aligned position anyway) — no hang.
	R := st.SearchR
	if R == 0 {
		L := st.gtpl.L
		const maxWork = 12000000
		if int64(st.N-L)*int64(L) > maxWork {
			R = maxWork / (2 * L)
			if R < 64 {
				R = 64
			}
		}
	}
	hits := st.gateFind(alignSig, base, R)
	if len(hits) == 0 {
		st.Rejected++
		return "rejected:nomatch"
	}
	for _, h := range hits {
		p := float64(h.loc) + h.delta
		lag := h.loc - st.GateLo
		st.Hits++
		odd := (st.Hits & 1) == 1
		for ch := 0; ch < 2; ch++ {
			s := sigs[ch]
			if len(s) < st.N || st.C[ch].ref == nil {
				continue
			}
			if ch != st.Align && Clipped(s) {
				st.C[ch].clipSkips++
				continue
			}
			f := make([]float32, st.N)
			g, b := gainOffset(st.C[ch].ref, s, lag, st.GateLo, st.GateHi)
			if g != 1 || b != 0 {
				for i := 0; i < st.N; i++ {
					v := (float64(s[i]) - b) / g
					if v < 0 {
						v = 0
					}
					f[i] = float32(v)
				}
			} else {
				for i := 0; i < st.N; i++ {
					f[i] = float32(s[i])
				}
			}
			st.drizzleHit(ch, f, p, odd)
		}
		st.Scores = append(st.Scores, h.score)
		st.Shifts = append(st.Shifts, float64(lag)+h.delta)
	}
	st.Frames++
	return "stacked:" + strconv.Itoa(len(hits))
}

// Result is the crunch output: the super-resolved mean trace (nil if statsOnly)
// plus the measured resolution figures.
type Result struct {
	Mean                    []float32 // align-channel mean; nil if statsOnly
	Mean2                   []float32 // the OTHER channel, same fine grid; nil if statsOnly
	Frames, Rejected, Hits  int
	Fill                    float64
	SigmaSingle, SigmaStack float64
	SigmaMeasured           bool
	BitsGained, EffBits     float64
	FineDtS, EffRateSa      float64
}

// Result crunches the stack: builds the mean trace and measures σ_single→σ_stack
// (odd/even half-stack) → bitsGained. stride subsamples the O(nbins) stats.
func (st *Stack) Result(statsOnly bool, stride int) Result {
	nb := st.Nbins
	if stride < 1 {
		stride = 1
	}
	A := &st.C[st.Align]
	const EPS = 0.05
	var mean, mean2 []float32
	if !statsOnly {
		// Align channel (Mean) plus the OTHER channel (Mean2), both on the same
		// L·K fine grid so a stacked X-Y / dual-spectrum can pair them index-for-
		// index. The other channel is drizzled at the align channel's positions,
		// so an unlocked C1↔C2 phase shows up as honest smear (matches the web).
		B := &st.C[1-st.Align]
		mean = make([]float32, nb)
		mean2 = make([]float32, nb)
		for b := 0; b < nb; b++ {
			if c := A.cnt[b]; c < EPS {
				mean[b] = -1
			} else {
				mean[b] = float32(A.sum[b] / c)
			}
			if c := B.cnt[b]; c < EPS {
				mean2[b] = -1
			} else {
				mean2[b] = float32(B.sum[b] / c)
			}
		}
	}
	statLo, statHi := st.StatLo*st.K, st.StatHi*st.K
	span := statHi - statLo
	if span < 1 {
		span = 1
	}
	statStride := stride
	if s := int(math.Ceil(float64(span) / 4096)); s > statStride {
		statStride = s
	}
	var sigSingles, halves, cnts []float64
	filled, scanned := 0, 0
	for b := statLo; b < statHi; b += statStride {
		c := A.cnt[b]
		scanned++
		if c < EPS {
			continue
		}
		filled++
		cnts = append(cnts, c)
		mv := A.sum[b] / c
		if c >= 4 {
			v := A.sum2[b]/c - mv*mv
			if v < 0 {
				v = 0
			}
			sigSingles = append(sigSingles, math.Sqrt(v))
			ca := A.cntA[b]
			cb := c - ca
			if ca >= 2 && cb >= 2 {
				ma := A.sumA[b] / ca
				mb := (A.sum[b] - A.sumA[b]) / cb
				halves = append(halves, (ma-mb)/2)
			}
		}
	}
	sigmaSingle := median(sigSingles)
	sigmaStack, sigmaMeasured := 0.0, true
	if len(halves) >= 16 {
		absh := make([]float64, len(halves))
		for i, v := range halves {
			absh[i] = math.Abs(v)
		}
		sigmaStack = 1.4826 * median(absh)
	}
	sigmaStackTheory := 0.0
	if sigmaSingle > 0 && len(cnts) > 0 {
		sigmaStackTheory = sigmaSingle / math.Sqrt(median(cnts))
	}
	if sigmaStack == 0 && sigmaStackTheory > 0 {
		sigmaStack, sigmaMeasured = sigmaStackTheory, false
	}
	bitsGained := 0.0
	if sigmaStack > 0 && sigmaSingle > 0 {
		bitsGained = math.Log2(sigmaSingle / sigmaStack)
	}
	fineDt, effRate := 0.0, 0.0
	if st.SampleS > 0 {
		fineDt = st.SampleS / float64(st.K)
		effRate = float64(st.K) / st.SampleS
	}
	fill := 0.0
	if scanned > 0 {
		fill = float64(filled) / float64(scanned)
	}
	return Result{
		Mean: mean, Mean2: mean2, Frames: st.Frames, Rejected: st.Rejected, Hits: st.Hits, Fill: fill,
		SigmaSingle: sigmaSingle, SigmaStack: sigmaStack, SigmaMeasured: sigmaMeasured,
		BitsGained: bitsGained, EffBits: 8 + bitsGained,
		FineDtS: fineDt, EffRateSa: effRate,
	}
}

// median = upper-middle element after sort (matches JS med: s[len>>1]).
func median(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	s := append([]float64(nil), a...)
	sortFloat(s)
	return s[len(s)>>1]
}

func sortFloat(a []float64) {
	// insertion sort is fine (stats slices are ≤4096); avoids importing sort for
	// a hot path and matches JS's stable numeric sort on equal keys.
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
