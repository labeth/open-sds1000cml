package superres

import (
	"math"
	"strconv"
)

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
