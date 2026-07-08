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
