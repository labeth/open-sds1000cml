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
