// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import "testing"

// TRLC-LINKS: REQ-SDS-021
func TestMathUsesCalibratedVoltages(t *testing.T) {
	// Same 0 V / 2 V signal, encoded at different ranges and offsets.
	h := HUD{C1VdivV: 1, C2VdivV: 2, OffC1V: -1, OffC2V: -2}
	for _, tc := range []struct {
		mode int
		want []uint8
	}{
		{1, []uint8{128, 228}}, {2, []uint8{128, 128}},
		{3, []uint8{128, 128}}, {4, []uint8{128, 141}},
	} {
		h.MathMode = tc.mode
		got := mathCodes([]uint8{103, 153}, []uint8{103, 128}, h)
		for i, v := range got {
			if v != tc.want[i] {
				t.Errorf("mode %d point %d got %d want %d", tc.mode, i, v, tc.want[i])
			}
		}
	}
	// Probe and display zoom must not turn equal physical voltages into a difference.
	h = HUD{MathMode: 2, C1VdivV: .1, Zoom1: 2, Probe1: 10, C2VdivV: 2, Probe2: 1}
	if got := mathCodes([]uint8{153}, []uint8{153}, h)[0]; got != 128 {
		t.Fatalf("zoom/probe difference %d", got)
	}
	// AC/GND presentation has no DC offset, just as the measurements do.
	h = HUD{MathMode: 2, C1VdivV: 1, C2VdivV: 1, OffC1V: 5, OffC2V: -5, Cpl1: 2, Cpl2: 2}
	if got := mathCodes([]uint8{128}, []uint8{128}, h)[0]; got != 128 {
		t.Fatalf("GND difference %d", got)
	}
}
