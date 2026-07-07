package lcd

import (
	"strings"
	"testing"
)

// Boundary formatting: a value that rounds up to 1000 at a prefix boundary
// (e.g. a 999.9 ns period) must PROMOTE to the next prefix, never print
// scientific notation ("1e+03 ns") on the scope readout.
func TestSiScaleNoScientific(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{fmtTdiv(999.9e-9), "1us"}, // just under 1 µs -> 1 us, not 1e+03 ns
		{fmtTdiv(1e-6), "1us"},
		{fmtTdiv(999.6e-6), "1ms"},
		{fmtTdiv(0.9999), "1s"},
		{fmtTdiv(500e-9), "500ns"},
		{fmtFreq(999900), "1MHz"}, // just under 1 MHz -> 1 MHz
		{fmtFreq(999.6e3), "1MHz"},
		{fmtFreq(2.5e6), "2.5MHz"},
		{fmtVolt(0.9999), "1V"},
		{fmtVolt(-999.9e-3), "-1V"},
	}
	for _, c := range cases {
		if strings.Contains(c.got, "e+") || strings.Contains(c.got, "e-") {
			t.Errorf("scientific notation leaked: %q", c.got)
		}
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}
