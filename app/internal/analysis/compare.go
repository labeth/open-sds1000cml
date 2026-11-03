// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS

package analysis

// The COMPARE workspace's figures (REQ-SDS-218): the live trace against a
// stored reference, and the repeat test that refuses a master from a trigger
// source that does not repeat. Records are in divisions and aligned sample by
// sample (the same time from the trigger at the same index); NaN marks a gap,
// a sample that was not captured.

import "math"

// RefDiff compares live against ref sample by sample over their common length,
// skipping gaps: the largest |live - ref|, the RMS difference, and the share
// of samples within +-tolDiv, in percent. All three are NaN when no sample
// pair is comparable.
// TRLC-LINKS: REQ-SDS-218
func RefDiff(live, ref []float64, tolDiv float64) (maxAbs, rms, pctWithin float64) {
	if !(tolDiv >= 0) {
		tolDiv = 0
	}
	n := min(len(live), len(ref))
	var ss float64
	cnt, within := 0, 0
	live, ref = live[:n], ref[:n]
	for i, l := range live {
		d := l - ref[i]
		if math.IsNaN(d) {
			continue
		}
		a := math.Abs(d)
		maxAbs = math.Max(maxAbs, a)
		ss += d * d
		cnt++
		if a <= tolDiv {
			within++
		}
	}
	if cnt == 0 {
		nan := math.NaN()
		return nan, nan, nan
	}
	return maxAbs, math.Sqrt(ss / float64(cnt)), 100 * float64(within) / float64(cnt)
}

// RepeatsBand reports whether two consecutive captures of the trigger
// source repeat, each read as column extremes (lo, hi per column), by the mask
// build's rule: each capture's column midpoints lie within +-tolDiv of the
// band the other covers over +-slack columns, from its lowest low to its
// highest high. A probe's ringing after an edge, a sample or two deep and
// caught at another depth or in the next column by another capture, widens
// the band where it rings and moves no midpoint out of it; a capture on
// another phase still puts a flat stretch where the other has the other
// level. A column that is a gap, one of the first or last slack columns, or
// one whose band window touches a gap, is not judged (a transition may hide
// there); captures of different lengths or without a judged column do not
// repeat. A master averaged from captures that fail this is a smear.
// TRLC-LINKS: REQ-SDS-218
func RepeatsBand(prevLo, prevHi, curLo, curHi []float64, tolDiv float64, slack int) bool {
	n := len(curLo)
	if n == 0 || len(curHi) != n || len(prevLo) != n || len(prevHi) != n || !(tolDiv >= 0) {
		return false
	}
	slack = max(0, slack)
	mid := func(lo, hi []float64) []float64 {
		m := make([]float64, n)
		for i := range m {
			m[i] = (lo[i] + hi[i]) / 2
		}
		return m
	}
	a := within(mid(curLo, curHi), prevLo, prevHi, tolDiv, slack)
	b := within(mid(prevLo, prevHi), curLo, curHi, tolDiv, slack)
	return a > 0 && b > 0
}

// within tests every sample of x against the sliding min of envLo and max of
// envHi over +-slack samples, dilated by tol. It returns the number of samples
// tested, or -1 at the first one outside.
// TRLC-LINKS: REQ-SDS-218
func within(x, envLo, envHi []float64, tol float64, slack int) int {
	n := len(envLo)
	// monotonic deques of indices into the envelopes: front = the window's min / max
	minQ := make([]int, 0, 64)
	maxQ := make([]int, 0, 64)
	next := 0 // the next index to enter the window
	gaps := 0 // gaps of the envelopes inside the window
	tested := 0
	for i, v := range x {
		for ; next < n && next <= i+slack; next++ {
			lo, hi := envLo[next], envHi[next]
			if math.IsNaN(lo) || math.IsNaN(hi) {
				gaps++
				continue
			}
			for len(minQ) > 0 && envLo[minQ[len(minQ)-1]] >= lo {
				minQ = minQ[:len(minQ)-1]
			}
			minQ = append(minQ, next)
			for len(maxQ) > 0 && envHi[maxQ[len(maxQ)-1]] <= hi {
				maxQ = maxQ[:len(maxQ)-1]
			}
			maxQ = append(maxQ, next)
		}
		if out := i - slack - 1; out >= 0 && (math.IsNaN(envLo[out]) || math.IsNaN(envHi[out])) {
			gaps-- // left the window
		}
		for len(minQ) > 0 && minQ[0] < i-slack {
			minQ = minQ[1:]
		}
		for len(maxQ) > 0 && maxQ[0] < i-slack {
			maxQ = maxQ[1:]
		}
		// the first and last slack samples have a truncated envelope: a
		// shifted edge there has no partner inside the record (wsmath.js
		// repeats skips them too), unless they are all there is
		if end := min(slack, (n-1)/2); i < end || i >= n-end || math.IsNaN(v) || gaps > 0 || len(minQ) == 0 {
			continue
		}
		if v < envLo[minQ[0]]-tol || v > envHi[maxQ[0]]+tol {
			return -1
		}
		tested++
	}
	return tested
}
