// ENGMODEL-OWNER-UNIT: FU-APP-SCPI
package scpi

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"open-sds/app/internal/engine"
)

// square is a 0/200-code square of the given period starting high at phase.
func square(n, period, phase int) []uint8 {
	c := make([]uint8, n)
	for i := range c {
		c[i] = 28
		if ((i + period - phase) % period) < period/2 {
			c[i] = 228
		}
	}
	return c
}

func replyValue(t *testing.T, out string) float64 {
	t.Helper()
	f := strings.Split(strings.TrimSpace(out), ",")
	v, err := strconv.ParseFloat(strings.TrimRight(f[len(f)-1], "SV"), 64)
	if err != nil {
		t.Fatalf("reply %q: %v", out, err)
	}
	return v
}

// C1-C2:MEAD? measures edge delays between the channels (bug hunt
// 2026-10-11: the verb was listed in spec 11 and answered "Undefined header").
// TRLC-LINKS: REQ-SDS-024
func TestMEADDelays(t *testing.T) {
	h, fs := newH(t)
	const n, per, lag = 1000, 100, 10
	fs.frame = &engine.Frame{C1: square(n, per, 5), C2: square(n, per, 5+lag), Valid: n, SampleS: 1e-9, EdgeX: 500, WinCols: 500}
	for _, tc := range []struct {
		arg  string
		want float64
	}{{"FRR", lag * 1e-9}, {"FFF", lag * 1e-9}, {"LRR", lag * 1e-9}, {"FRF", (lag + per/2) * 1e-9}, {"SKEW", lag * 1e-9}, {"PHA", 36}} {
		out := do(t, h, "C1-C2:MEAD? "+tc.arg)
		if !strings.HasPrefix(out, "C1-C2:MEAD "+tc.arg+",") {
			t.Fatalf("%s: %q", tc.arg, out)
		}
		if got := replyValue(t, out); got < tc.want*0.97-1e-12 || got > tc.want*1.03+1e-12 {
			t.Errorf("%s = %g want %g (%q)", tc.arg, got, tc.want, out)
		}
	}
	if got := replyValue(t, do(t, h, "C2-C1:MEAD? FRR")); got > 0 {
		t.Errorf("C2-C1 FRR = %g, want the reverse (negative or the next edge)", got)
	}
	for c, want := range map[string]string{"C1-C2:MEAD? BOGUS": errHeader, "C1-C3:MEAD? FRR": errSuffix, "C1-C1:MEAD? FRR": errSuffix, "C1-C2:PAVA? PKPK": errUndefined} {
		if out := do(t, h, c); out != want+"\n" {
			t.Errorf("%s: %q want %q", c, out, want)
		}
	}
	fs.frame = &engine.Frame{C1: make([]uint8, n), C2: square(n, per, 5), Valid: n, SampleS: 1e-9}
	if out := do(t, h, "C1-C2:MEAD? FRR"); out != "C1-C2:MEAD FRR,****\n" {
		t.Errorf("flat source: %q", out)
	}
}

type fakeCursors struct {
	fakeDisplay
	on  bool
	typ int
	x   [2]float64
	y   [2]float64
}

func (d *fakeCursors) CursorState() (bool, int, [2]float64, [2]float64) { return d.on, d.typ, d.x, d.y }

// Cn:CRVA? reads the panel cursors in the LCD readout's mapping.
// TRLC-LINKS: REQ-SDS-024
func TestCRVACursorValues(t *testing.T) {
	h, fs := newH(t)
	d := &fakeCursors{}
	h.disp = d
	fs.stats = engine.Stats{TdivS: 1e-6, TrigPosFrac: .5}
	c := make([]uint8, 1000)
	for i := range c {
		c[i] = uint8(128 + i/10) // a ramp: code 128 at sample 0
	}
	fs.frame = &engine.Frame{C1: c, C2: c, Valid: 1000, SampleS: 1e-9, EdgeX: 500, WinCols: 1000}
	if out := do(t, h, "C1:CRVA? HREL"); out != "C1:CRVA OFF\n" {
		t.Fatalf("cursors off: %q", out)
	}
	d.on, d.x, d.y = true, [2]float64{0.25, 0.75}, [2]float64{0.25, 0.75}
	out := do(t, h, "C1:CRVA? HREL")
	// dt = 0.5 of 10 us; cursors at samples 250 and 750 -> codes 153, 203 at 1 V/div (25 codes/div)
	want := fmt.Sprintf("C1:CRVA HREL,%s,%s,%s,%s\n", sciS(5e-6), "2.00E+05Hz", sciV(1.0), sciV(3.0))
	if out != want {
		t.Fatalf("HREL %q want %q", out, want)
	}
	// VREL: y 0.25 -> +2 div, 0.75 -> -2 div at 1 V/div: dV = -4 V
	if out := do(t, h, "C1:CRVA? VREL"); out != fmt.Sprintf("C1:CRVA VREL,%s,%s,%s\n", sciV(-4), sciV(2), sciV(-2)) {
		t.Fatalf("VREL %q", out)
	}
	if out := do(t, h, "C1:CRVA? XREL"); out != errHeader+"\n" {
		t.Fatalf("bad mode: %q", out)
	}
}
