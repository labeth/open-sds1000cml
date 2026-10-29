// ENGMODEL-OWNER-UNIT: FU-APP-LCD
package lcd

import (
	"testing"

	"open-sds/app/internal/measure"
)

// A measurement's envelope timing is taken once for it and its scale: the
// readouts of one picture, and of the pictures the measurement serves, share
// it; another measurement or scale takes it again, and a hand-built frame
// (no sequence) is never cached.
// TRLC-LINKS: REQ-SDS-021
func TestTimedForTakesTheTimingOnce(t *testing.T) {
	a, b := &measure.Result{}, &measure.Result{}
	n := 0
	take := func(m *measure.Result) *measure.Result { n++; c := *m; return &c }
	r1 := timedFor(0, a, true, 0.1, 0, 2e-9, take)
	r2 := timedFor(0, a, true, 0.1, 0, 2e-9, take)
	if n != 1 || r1 != r2 {
		t.Fatalf("same measurement and scale: taken %d times", n)
	}
	timedFor(0, a, true, 0.2, 0, 2e-9, take)
	timedFor(0, b, true, 0.2, 0, 2e-9, take)
	timedFor(1, b, true, 0.2, 0, 2e-9, take)
	if n != 4 {
		t.Fatalf("a new scale, measurement or channel: taken %d times, want 4", n)
	}
	timedFor(0, b, false, 0.2, 0, 2e-9, take)
	timedFor(0, b, false, 0.2, 0, 2e-9, take)
	if n != 6 {
		t.Fatalf("uncached: taken %d times, want 6", n)
	}
}

// X-Y's tables put every code where the arithmetic did.
// TRLC-LINKS: REQ-SDS-021
func TestXYTablesMatchTheArithmetic(t *testing.T) {
	p := plotArea{top: 20, bot: 400, y0: 14, h: 400, w: 700}
	rows := p.codeRows(1)
	for c := 0; c < 256; c++ {
		if int(rows[c]) != p.sy(float64(c)) {
			t.Fatalf("code %d: row %d, sy %d", c, rows[c], p.sy(float64(c)))
		}
	}
}
