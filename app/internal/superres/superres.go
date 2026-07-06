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

import "math"

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

// SeedRef adopts a specific (frozen) frame as the LOCKED alignment reference and
// stacks it at shift 0. Sets UserRef so the matcher never drifts off it. Returns
// false if the frame is unusable (flat/clipped).
func (st *Stack) SeedRef(sig1, sig2 []uint8, edgeX float64) bool {
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
		st.accumCh(ch, ref, 0)
	}
	st.Frames++
	st.Scores = append(st.Scores, 1)
	st.Shifts = append(st.Shifts, 0)
	st.UserRef = true
	st.tpl = buildTemplate(st.C[st.Align].ref, st.N, st.RefEdgeX, st.N)
	return true
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

// Feed runs the reference-locked pipeline for one frame: match against the locked
// template, reject non-matches, drift-normalize both channels and drizzle the
// match. Returns "stacked" | "rejected:<why>". SeedRef must have run first.
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
	if !st.UserRef || st.tpl == nil {
		st.Rejected++
		return "rejected:no-ref"
	}
	base := 0
	if st.RefEdgeX >= 0 && edgeX >= 0 {
		base = jsRound(edgeX - st.RefEdgeX)
	}
	R := st.MaxShift
	if R == 0 {
		R = 64
	}
	minMatch := st.MinMatch
	if minMatch == 0 {
		minMatch = 0.8
	}
	m, ok := st.matchLocate(alignSig, base, R)
	if !ok || m.ambig || m.score < minMatch {
		st.Rejected++
		return "rejected:nomatch"
	}
	shiftInt := jsRound(m.shift)
	wLo := st.tpl.lo - st.tpl.L
	if wLo < 0 {
		wLo = 0
	}
	wHi := st.tpl.hi + st.tpl.L
	if wHi > st.N {
		wHi = st.N
	}
	st.StatLo, st.StatHi = wLo, wHi
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
		g, b := gainOffset(st.C[ch].ref, s, shiftInt, wLo, wHi)
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
		st.accumCh(ch, f, m.shift)
	}
	st.Frames++
	st.Scores = append(st.Scores, m.score)
	st.Shifts = append(st.Shifts, m.shift)
	return "stacked"
}

// Result is the crunch output: the super-resolved mean trace (nil if statsOnly)
// plus the measured resolution figures.
type Result struct {
	Mean                    []float32 // nil if statsOnly
	Frames, Rejected        int
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
	var mean []float32
	if !statsOnly {
		mean = make([]float32, nb)
		for b := 0; b < nb; b++ {
			c := A.cnt[b]
			if c < EPS {
				mean[b] = -1
			} else {
				mean[b] = float32(A.sum[b] / c)
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
		Mean: mean, Frames: st.Frames, Rejected: st.Rejected, Fill: fill,
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
