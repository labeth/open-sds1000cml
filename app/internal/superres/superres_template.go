// ENGMODEL-OWNER-UNIT: FU-APP-SUPERRES

package superres

import "math"

// buildTemplate isolates the reference's distinguishing content — the active
// (non-flat) region after the trigger transition — as a zero-mean template. See
// srBuildTemplate in superres.js; general-purpose (burst, UART byte, glitch…).
// TRLC-LINKS: REQ-SDS-141
func buildTemplate(ref []float32, n int, edgeX float64, valid int) *template {
	hi0 := n
	if valid > 0 && valid <= n {
		hi0 = valid
	}
	lo0 := 0
	if edgeX >= 0 {
		lo0 = min(jsRound(edgeX)+16, hi0-1)
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
	// The 20th percentile and the peak, selected, not sorted.
	sorted := append([]float64(nil), mr[lo0:hi0]...)
	peak := sorted[0]
	for _, v := range sorted {
		peak = max(peak, v)
	}
	floor := selectKth(sorted, int(float64(len(sorted))*0.2))
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
		lo, hi = max(lo-h, lo0), min(hi+h, hi0-1)
	}
	L := hi - lo + 1
	data := make([]float64, L)
	mean := 0.0
	for i := range L {
		mean += float64(ref[lo+i])
	}
	mean /= float64(L)
	ss := 0.0
	for i := range L {
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
