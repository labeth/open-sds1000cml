// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// A capture waiting for its event polls each millisecond while a fast
// record is still due, then every other millisecond.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-011
func TestStatusPollWaitBacksOff(t *testing.T) {
	prev := time.Duration(0)
	for _, w := range []time.Duration{0, 5 * time.Millisecond, 10 * time.Millisecond, 39 * time.Millisecond, 40 * time.Millisecond, time.Minute} {
		d := statusPollWait(w)
		if d < prev || d < time.Millisecond || d > 2*time.Millisecond || (w < 10*time.Millisecond && d != time.Millisecond) {
			t.Fatalf("waited %s: poll every %s (before %s)", w, d, prev)
		}
		prev = d
	}
}
