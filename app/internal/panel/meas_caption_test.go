// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
package panel

import (
	"strings"
	"testing"
	"time"
)

// MEASURE's statistics span runs to the last reading counted: with no new
// reading (stopped) the pane does not change as time passes. A caption that
// kept counting changed the LCD's picture ten times a second, and a stopped
// deep record was decoded and measured again for every redraw.
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-168
func TestStatisticsSpanStopsWithTheReadings(t *testing.T) {
	r := newViewRig(t)
	c := r.c
	press(t, c, "f4")
	for i := 0; i < 3; i++ {
		r.show(toneFrame(""))
		r.clk.advance(200 * time.Millisecond)
	}
	before := c.View().Meas.Caption
	if !strings.Contains(before, "N 3") || !strings.Contains(before, "over 0.4 s") {
		t.Fatalf("caption %q", before)
	}
	r.clk.advance(5 * time.Second)
	if got := c.View().Meas.Caption; got != before {
		t.Fatalf("no new reading, but the caption moved: %q -> %q", before, got)
	}
	r.show(toneFrame(""))
	if got := c.View().Meas.Caption; !strings.Contains(got, "N 4") || !strings.Contains(got, "over 5.6 s") {
		t.Fatalf("after a reading: %q", got)
	}
}
