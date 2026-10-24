// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"math"
	"testing"
)

// mathCodesRef is the per-sample math the tabled mathCodes must match.
// TRLC-LINKS: REQ-SDS-021
func mathCodesRef(c1, c2 []uint8, hud HUD) []uint8 {
	scale := mathScale(hud) / 25
	v1 := hud.C1VdivV * float64(max(hud.Zoom1, 1)) / 25 * math.Max(hud.Probe1, 1)
	v2 := hud.C2VdivV * float64(max(hud.Zoom2, 1)) / 25 * math.Max(hud.Probe2, 1)
	if v1 <= 0 {
		v1 = 1.0 / 25
	}
	if v2 <= 0 {
		v2 = 1.0 / 25
	}
	off1, off2 := hud.OffC1V*math.Max(hud.Probe1, 1), hud.OffC2V*math.Max(hud.Probe2, 1)
	if hud.Cpl1 != 0 {
		off1 = 0
	}
	if hud.Cpl2 != 0 {
		off2 = 0
	}
	m := make([]uint8, min(len(c1), len(c2)))
	for i := range m {
		a, b := (float64(c1[i])-128)*v1-off1, (float64(c2[i])-128)*v2-off2
		var v float64
		switch hud.MathMode {
		case 1:
			v = a + b
		case 2:
			v = a - b
		case 3:
			v = b - a
		case 4:
			v = a * b
		}
		m[i] = uint8(math.Round(math.Max(0, math.Min(255, 128+v/scale))))
	}
	return m
}

// Every code pair, every mode, odd scales, offsets, probes and coupling.
// TRLC-LINKS: REQ-SDS-021
func TestMathCodesMatchesRef(t *testing.T) {
	c1, c2 := make([]uint8, 65536+7), make([]uint8, 65536)
	for i := range c2 {
		c1[i], c2[i] = uint8(i), uint8(i>>8)
	}
	for _, h := range []HUD{
		{C1VdivV: 0.2, C2VdivV: 0.5},
		{C1VdivV: 0.002, C2VdivV: 5, Probe1: 10, Zoom2: 4, OffC1V: 0.013, OffC2V: -1.7},
		{C1VdivV: 0.1, C2VdivV: 0.1, OffC1V: 0.3, OffC2V: 0.3, Cpl1: 1},
		{C1VdivV: 0, C2VdivV: -1, Probe2: 100, OffC2V: 1e-3},
		{C1VdivV: 1, C2VdivV: 1, OffC1V: math.Inf(1), OffC2V: math.Inf(1)}, // NaN sums
	} {
		for mode := 0; mode <= 5; mode++ {
			h.MathMode = mode
			got, want := mathCodes(c1, c2, h), mathCodesRef(c1, c2, h)
			if string(got) != string(want) {
				t.Fatalf("mode %d hud %+v: mathCodes differs from the reference", mode, h)
			}
		}
	}
}
