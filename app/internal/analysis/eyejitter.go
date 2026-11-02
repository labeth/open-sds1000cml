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

// medianInPlace is median(a) for a slice the caller gives up: the element
// a sort would put at len>>1, found by quickselect (a is reordered).
// TRLC-LINKS: REQ-SDS-199
func medianInPlace(a []float64) float64 {
	if len(a) == 0 {
		return 0
	}
	k := len(a) >> 1
	lo, hi := 0, len(a)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		if a[mid] < a[lo] {
			a[mid], a[lo] = a[lo], a[mid]
		}
		if a[hi] < a[lo] {
			a[hi], a[lo] = a[lo], a[hi]
		}
		if a[hi] < a[mid] {
			a[hi], a[mid] = a[mid], a[hi]
		}
		p := a[mid]
		i, j := lo, hi
		for i <= j {
			for a[i] < p {
				i++
			}
			for a[j] > p {
				j--
			}
			if i <= j {
				a[i], a[j] = a[j], a[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j
		case k >= i:
			lo = i
		default:
			return a[k]
		}
	}
	return a[k]
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
	// s is sorted: the two sides are its prefix up to the median's last copy
	// and the rest, already in order, so their medians are read, not sorted
	// again.
	k := sort.Search(len(s), func(i int) bool { return s[i] > med })
	left, right := s[:k], s[k:]
	mL, mR := 0.0, 0.0
	if len(left) > 0 {
		mL = left[len(left)>>1]
	}
	if len(right) > 0 {
		mR = right[len(right)>>1]
	}
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
	// The deviations are this call's own: their medians selected in place.
	if !(sep > 0) || float64(central)/float64(len(s)) > 0.08 {
		return 1.4826 * medianInPlace(absDev(s, med)), 0, true
	}
	madL, madR := medianInPlace(absDev(left, mL)), medianInPlace(absDev(right, mR))
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

// pushCapped appends v while a holds fewer than limit values (ejPushCapped).
// TRLC-LINKS: REQ-SDS-199
func pushCapped(a *[]float64, v float64, limit int) {
	if len(*a) < limit {
		*a = append(*a, v)
	}
}
