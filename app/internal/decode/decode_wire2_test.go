// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"strings"
	"testing"
)

// A record cut inside an SD token, on a bus sending its tokens back to back,
// reads every whole token after the cut: the cut token fails its check and
// the decode takes up the next token whose CRC checks rather than reading
// on out of step.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-254
func TestDecodeSDCardRecordCutInsideAToken(t *testing.T) {
	var bits []int
	for k := 0; k < 4; k++ {
		bits = append(bits, sdToken(true, 17, 0x1000, false)...)
		bits = append(bits, sdIdle(2)...)
		bits = append(bits, sdToken(false, 17, 0x900, false)...)
		bits = append(bits, sdIdle(1)...)
		bits = append(bits, sdToken(true, 13, 0x12340000, false)...)
		bits = append(bits, sdToken(false, 13, 0x900, false)...)
	}
	sr, per := 4e6, 4e-6
	for cut := 0; cut < 48; cut++ {
		l := newTwoLine(sr)
		clockBits(l, bits[cut:], per)
		r := DecodeSDCard(l.a.codes(), l.b.codes(), 1/sr, ClockedCfg{})
		if !r.OK || strings.Count(r.Text, "CMD13") < 4 || strings.Count(r.Text, "!") > 1 {
			t.Fatalf("cut %d bits in: ok=%v (%s) %q", cut, r.OK, r.Error, r.Text)
		}
	}
}
