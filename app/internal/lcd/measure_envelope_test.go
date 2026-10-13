// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"

	"open-sds/app/internal/engine"
	"open-sds/app/internal/measure"
)

// An inverted channel on a live envelope frame reads the complement duty: the
// envelope's timing comes before the invert, which would otherwise be undone
// (a 30 % square read 30 %, not 70 %, when inverted).
// TRLC-LINKS: REQ-SDS-021
func TestInvertedEnvelopeDuty(t *testing.T) {
	n := 2048
	f := &engine.Frame{C1: make([]uint8, n), C2: make([]uint8, n), Valid: n, WinCols: n, SampleS: 96e-9, PeakDetect: true, Ordered: true, Seq: 0}
	for b := 0; b < n/2; b++ {
		v := uint8(78)
		if b%104 < 31 { // 30 % of each 104-bucket period high
			v = 178
		}
		f.C1[2*b], f.C1[2*b+1] = v, v
		f.C2[2*b], f.C2[2*b+1] = v, v
	}
	hud := HUD{C1VdivV: 1, C2VdivV: 1, Probe1: 1, Probe2: 1, SampleS: f.SampleS}
	plain := chanMeasure(f, hud, 0)
	hud.Inv1 = true
	inv := chanMeasure(f, hud, 0)
	if plain == nil || inv == nil || !plain.HasTiming || math.Abs(plain.Duty-30) > 2 || math.Abs(inv.Duty-70) > 2 {
		t.Fatalf("duty: plain %+v, inverted %+v", plain, inv)
	}
}

// An envelope too slow for its signal reads no mean or RMS: the readout shows
// none rather than its (min, max) pairs' midpoint.
// TRLC-LINKS: REQ-SDS-021
func TestAliasedEnvelopeHidesMean(t *testing.T) {
	for _, id := range []string{"mean", "rms", "acrms"} {
		if _, ok := measParamRow(&measure.Result{Vmean: 1.6, EnvAliased: true}, id); ok {
			t.Errorf("%s shown on an aliased envelope", id)
		}
		if _, ok := measParamRow(&measure.Result{Vmean: 0.6}, id); !ok {
			t.Errorf("%s hidden on a resolved frame", id)
		}
	}
}
