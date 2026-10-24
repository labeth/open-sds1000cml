// ENGMODEL-OWNER-UNIT: FU-APP-DECODE
package decode

import (
	"fmt"
	"testing"
)

// hex6 reads as fmt's %06X for every 24-bit sample.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-235
func TestHex6MatchesFmt(t *testing.T) {
	for v := 0; v < 1<<24; v += 4093 {
		if got, want := hex6(v), fmt.Sprintf("%06X", v); got != want {
			t.Fatalf("hex6(%#x) = %q, want %q", v, got, want)
		}
	}
	if got := hex6(0xFFFFFF); got != "FFFFFF" {
		t.Fatalf("hex6(0xFFFFFF) = %q", got)
	}
}
