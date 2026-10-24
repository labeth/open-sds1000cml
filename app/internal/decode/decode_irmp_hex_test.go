// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"testing"
)

// labelHex reads as fmt's "%s%X", negative values included.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-255
func TestLabelHexMatchesFmt(t *testing.T) {
	for _, v := range []int{0, 1, 0xa, 0xff, 0x1fff, 0xabcdef, -1, -0x2b, 1 << 40} {
		for _, l := range []string{"", "A", "@", "CMD "} {
			if got, want := labelHex(l, v), fmt.Sprintf("%s%X", l, v); got != want {
				t.Errorf("labelHex(%q, %d) = %q, want %q", l, v, got, want)
			}
		}
	}
}
