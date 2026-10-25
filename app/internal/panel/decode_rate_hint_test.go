// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
)

// A decode refused for too few samples a bit names the slowest timebase
// that gives enough, and says the trigger fires when it does; other errors
// get no hint.
// TRLC-LINKS: REQ-SDS-214
func TestDecodeRateHint(t *testing.T) {
	h := decodeRateHint("2.0 samples/bit; need >= 3", 10e-3, true, 330)
	if !strings.Contains(h, "5ms/div or faster") || !strings.Contains(h, "trigger fires") {
		t.Fatalf("modbus at 10 ms/div: %q", h)
	}
	if h := decodeRateHint("modbus: 2.0 samples/bit; need >= 3", 10e-3, false, 0); !strings.Contains(h, "5ms/div or faster") || strings.Contains(h, "trigger") {
		t.Fatalf("prefixed, no serial trigger: %q", h)
	}
	if h := decodeRateHint("1.0 samples/bit; need >= 4", 2e-6, true, 0); !strings.Contains(h, "500ns/div or faster") || strings.Contains(h, "fires") {
		t.Fatalf("no matches yet: %q", h)
	}
	for _, e := range []string{"too few edges", "no Manchester frame (preamble) found", "3.5 samples/bit; need >= 3", ""} {
		if h := decodeRateHint(e, 10e-3, true, 5); h != "" {
			t.Fatalf("%q: hint %q, want none", e, h)
		}
	}
}
