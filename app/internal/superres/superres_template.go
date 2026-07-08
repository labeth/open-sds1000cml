package superres

import "math"

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
