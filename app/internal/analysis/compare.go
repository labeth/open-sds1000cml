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
	for i := 0; i < n; i++ {
		d := live[i] - ref[i]
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

// Repeats reports whether two consecutive captures of the trigger source
// repeat, by the mask build's rule (panel/mask.go, engine
// BuildMaskFromEnvelope): every sample of each lies within +-tolDiv of the
// other's min/max envelope over +-slack samples. A sample that is a gap, or
// whose envelope window touches a gap, is not judged (an unobserved mask
// column always passes: a transition may hide in the gap); captures of
// different lengths or without a judged sample do not repeat. A master
// averaged from captures that fail this is a smear.
// TRLC-LINKS: REQ-SDS-218
func Repeats(prev, cur []float64, tolDiv float64, slack int) bool {
	if len(prev) != len(cur) || len(cur) == 0 || !(tolDiv >= 0) {
		return false
	}
	slack = max(0, slack)
	a := within(cur, prev, tolDiv, slack)
	b := within(prev, cur, tolDiv, slack)
	return a > 0 && b > 0
}

// within tests every sample of x against env's sliding min/max over
// +-slack samples, dilated by tol. It returns the number of samples tested,
// or -1 at the first one outside.
// TRLC-LINKS: REQ-SDS-218
func within(x, env []float64, tol float64, slack int) int {
	n := len(env)
	// monotonic deques of indices into env: front = the window's min / max
	minQ := make([]int, 0, 64)
	maxQ := make([]int, 0, 64)
	next := 0 // the next env index to enter the window
	gaps := 0 // gaps of env inside the window
	tested := 0
	for i, v := range x {
		for ; next < n && next <= i+slack; next++ {
			e := env[next]
			if math.IsNaN(e) {
				gaps++
				continue
			}
			for len(minQ) > 0 && env[minQ[len(minQ)-1]] >= e {
				minQ = minQ[:len(minQ)-1]
			}
			minQ = append(minQ, next)
			for len(maxQ) > 0 && env[maxQ[len(maxQ)-1]] <= e {
				maxQ = maxQ[:len(maxQ)-1]
			}
			maxQ = append(maxQ, next)
		}
		if out := i - slack - 1; out >= 0 && math.IsNaN(env[out]) {
			gaps-- // left the window
		}
		for len(minQ) > 0 && minQ[0] < i-slack {
			minQ = minQ[1:]
		}
		for len(maxQ) > 0 && maxQ[0] < i-slack {
			maxQ = maxQ[1:]
		}
		if math.IsNaN(v) || gaps > 0 || len(minQ) == 0 {
			continue
		}
		if v < env[minQ[0]]-tol || v > env[maxQ[0]]+tol {
			return -1
		}
		tested++
	}
	return tested
}
