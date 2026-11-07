// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package acq

import (
	"math"
	"testing"

	"github.com/labeth/open-dso-core/sample"
	"github.com/labeth/open-sds1000cml/app/internal/sramcapture"
)

// The limits in the FPGA's ticks: an interval whose edges are W clocks apart
// counts floor((W-1)/P) ticks of P clocks, so at P 1 a minimum of 10 clocks
// (40 ns) is 9 ticks and a maximum of 10 clocks 9 ticks (over from 11).
// TRLC-LINKS: REQ-SDS-011
func TestQualTicksMatchTheFPGAsCount(t *testing.T) {
	counts := func(w, p int) int { return (w - 1) / p }
	for _, c := range []struct{ minNs, maxNs float64 }{{40, 40}, {40, 400}, {1000, 3000}, {0, 20}, {2e6, 5e6}} {
		p, lo, hi := QualTicks(c.minNs, c.maxNs)
		minW, maxW := int(c.minNs/4), int(c.maxNs/4)
		if p != 1 {
			continue
		}
		// at least the minimum from minW clocks, not before; over the maximum from maxW+1, not at maxW
		if minW > 0 && (counts(minW, 1) < int(lo) || counts(minW-1, 1) >= int(lo)) {
			t.Fatalf("%g ns minimum: %d ticks", c.minNs, lo)
		}
		if counts(maxW, 1) > int(hi) || counts(maxW+1, 1) <= int(hi) {
			t.Fatalf("%g ns maximum: %d ticks", c.maxNs, hi)
		}
	}
	if p, lo, hi := QualTicks(40, 40); p != 1 || lo != 9 || hi != 9 {
		t.Fatalf("40 ns: prescale %d, %d..%d ticks", p, lo, hi)
	}
}

// The FPGA fires a video line at its sync's end; the trigger point moves
// to the sync's leading edge. A pulse's point is its closing crossing.
// TRLC-LINKS: REQ-SDS-011
func TestQualifiedTriggerIsPlacedOnItsEvent(t *testing.T) {
	const n = 2000
	sig := make([]uint8, n)
	for i := range sig {
		sig[i] = 200
		if i >= 800 && i < 1270 { // a 4.7 us sync at 10 ns
			sig[i] = 40
		}
	}
	f := &sample.Frame{SampleS: 1e-8, TriggerAt: 1272, Status: sample.Status{Triggered: true}, Traces: []sample.Trace{{Codes8: sig, Valid: n}, {Ch: 1, Valid: n}}}
	q := sramcapture.QualifierConfig{Enabled: true, Kind: sramcapture.QualVideo, Negative: true}
	if x := PlaceQualified(f, sig, nil, q, 88); math.Abs(x-800) > 1 {
		t.Fatalf("video placed at %.1f, want the sync's leading edge 800", x)
	}
	q = sramcapture.QualifierConfig{Enabled: true, Kind: sramcapture.QualPulse, Negative: true} // a low pulse closes rising
	if x := PlaceQualified(f, sig, nil, q, 120); math.Abs(x-1270) > 1 {
		t.Fatalf("pulse placed at %.1f, want 1270", x)
	}
}
