// ENGMODEL-OWNER-UNIT: FU-APP-DECODE

package decode

import "testing"

// A PJON frame whose header asks for a 16-bit length but which the line
// cut after four bytes is shown as cut, not read past its end.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-253
func TestPJONFieldsCutBeforeLongHeader(t *testing.T) {
	var o wireOut
	if pjonFields(&o, []int{0x2a, 0x40, 0x00, 0x07}, []float64{0, 9, 18, 27}, 1) {
		t.Fatal("a cut frame checks")
	}
	if len(o.spans) != 4 || o.spans[3].Kind != "frame-error" {
		t.Fatalf("spans %+v", o.spans)
	}
}
