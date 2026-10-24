// Package analysis holds the measurement analysis the task workspaces share
// between the scope's LCD and the web app (ADR-TASK-WORKSPACES): decoded
// packets and a list of them that holds still (BUS), the eye diagram and its
// jitter figures (BUS again), spectral peaks with harmonics and THD (SPECTRUM),
// running statistics (MEASURE), and the reference difference and repeat test
// behind a stacked master (COMPARE).
//
// It is pure Go: no I/O, no goroutines, no global state, deterministic. Every
// entry point accepts empty or degenerate input without panicking. Where the
// web app already computes the same figure, the Go code is a port of the
// JavaScript and a node parity test holds the two together (eyejitter.js,
// peaks.js).
// ENGMODEL-OWNER-UNIT: FU-APP-ANALYSIS
package analysis

import "math"

// jsRound is JavaScript's Math.round: the nearest integer, halves toward +Inf
// (math.Round sends -2.5 to -3; Math.round sends it to -2). The ported code
// uses it wherever the JavaScript rounds, so the two assign the same bins and
// bit indices.
// TRLC-LINKS: REQ-SDS-215
func jsRound(x float64) float64 {
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// finite reports whether x is neither NaN nor infinite.
// TRLC-LINKS: REQ-SDS-215
func finite(x float64) bool { return x-x == 0 } // NaN and ±Inf give NaN: one subtraction

// truthy is JavaScript's truth test on a number: neither 0 nor NaN.
// TRLC-LINKS: REQ-SDS-215
func truthy(x float64) bool { return x != 0 && !math.IsNaN(x) }

// clampInt limits v to [lo, hi].
// TRLC-LINKS: REQ-SDS-215
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
