package panel

import (
	"strings"
	"testing"
)

// Menu labels (holdoff, pulse width, etc.) must not print scientific notation
// at a prefix boundary — the same 999.9->"1e+03" rounding trap fmtEng shares
// with the web/LCD formatters.
func TestFmtEngNoScientific(t *testing.T) {
	for _, v := range []float64{999.9e-9, 1e-6, 999.6e-6, 0.9999, 999.9e-3, 100e-9, 2e-3, 1.0} {
		s := fmtEng(v, "s")
		if strings.Contains(s, "e+") || strings.Contains(s, "e-") {
			t.Errorf("fmtEng(%g) = %q — scientific notation on a menu label", v, s)
		}
	}
	if got := fmtEng(999.9e-9, "s"); got != "1 us" {
		t.Errorf("fmtEng boundary: got %q want %q", got, "1 us")
	}
}
