// ENGMODEL-OWNER-UNIT: FU-APP-WEB
package web

import "testing"

// A frame with no crossing shows the screen centred on the record (lcd
// traceWindow): at 10 % no column past the record's end repeats its last
// sample.
// TRLC-LINKS: REQ-SDS-163
func TestWindowWithoutAnEdge(t *testing.T) {
	sig := make([]uint8, 1000)
	for i := range sig {
		sig[i] = uint8(i / 4)
	}
	out := window(sig, 1000, 1000, -1, false, 100, 0.1)
	if out[0] > 2 || out[99] < 245 {
		t.Fatalf("columns from %d to %d, want the record's 0 to 249", out[0], out[99])
	}
}
