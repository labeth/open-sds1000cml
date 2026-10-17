// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"math"
	"testing"
)

// A serial trigger's match edge goes either way: a UART start bit falls. The
// average under a serial trigger aligns on the crossing nearest the match,
// rising or falling; aligned on the nearest rising edge (the previous stop
// bit's) it sat a bit away from the live captures, which are centred on the
// match, and the master stacked from it was drawn shifted.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-218
func TestNearerCrossTakesTheMatchEdge(t *testing.T) {
	sig := make([]uint8, 600)
	for i := range sig {
		switch {
		case i >= 40 && i < 60, i >= 100 && i < 300: // a low bit, then the start bit and a 0
			sig[i] = 50
		default:
			sig[i] = 200
		}
	}
	const anchor = 100.0 // the decoder's start of the matched byte
	lvl := midLevel(sig)
	if got := nearerCrossHint(sig, lvl, anchor); math.Abs(got-anchor) > 1.5 {
		t.Fatalf("nearer crossing %.1f, want the start bit's falling edge at %.0f", got, anchor)
	}
	if r := centerCrossHint(sig, lvl, true, anchor); math.Abs(r-anchor) < 30 {
		t.Fatalf("the nearest rising crossing is %.1f: the test no longer shows the shift", r)
	}
}

// A high pulse trigger closes on the falling edge: the average aligns there,
// though no rising edge lies in the window around the trigger point.
// TRLC-LINKS: REQ-SDS-011, REQ-SDS-218
func TestAverageAlignsOnAPulseClosingEdge(t *testing.T) {
	sig := make([]uint8, 8192)
	for i := range sig {
		sig[i] = 200 // idle high, then the closing edge and a long low
		if i >= 4096 {
			sig[i] = 50
		}
	}
	const hint = 4096.0
	if got := averageAlignEdge(sig, hint, true, true); math.Abs(got-hint) > 1.5 {
		t.Fatalf("pulse closing edge: aligned at %.1f, want %.0f", got, hint)
	}
	if got := averageAlignEdge(sig, hint, false, true); got >= 0 {
		t.Fatalf("an edge trigger's rising crossing found at %.1f in a falling-only window", got)
	}
}
