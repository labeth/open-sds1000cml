// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// AUTO forces a capture whose wait is over only while the hardware has not
// triggered: a triggered record fills its post-trigger part, which at
// 10ms/div (135 ms) outlasted the wait, and every frame of a frame-start
// trigger read forced. NORMAL never forces a running scope.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func TestAutoForceWaitsOnATriggeredRecord(t *testing.T) {
	wait := 115 * time.Millisecond
	for _, c := range []struct {
		norm, running, triggered bool
		waited                   time.Duration
		want                     bool
	}{
		{false, true, false, 120 * time.Millisecond, true},
		{false, true, false, 100 * time.Millisecond, false},
		{false, true, true, 140 * time.Millisecond, false},
		{true, true, false, time.Second, false},
		{true, false, false, time.Second, true},
	} {
		if got := autoForceDue(c.norm, c.running, c.triggered, c.waited, wait); got != c.want {
			t.Fatalf("%+v: %v", c, got)
		}
	}
}
