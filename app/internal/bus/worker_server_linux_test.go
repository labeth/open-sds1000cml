// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"testing"
	"time"
)

// The pump looks at the 2^9 stream about as rarely as before (a bank fills
// in 2.1 ms) and at the 2^8 stream about every millisecond, never faster
// than streamPumpFastest nor slower than streamPumpEvery.
// TRLC-LINKS: REQ-SDS-035
func TestPumpEveryFollowsTheRate(t *testing.T) {
	for _, c := range []struct {
		rate     float64
		bank     int
		min, max time.Duration
	}{
		{0, 2048, streamPumpEvery, streamPumpEvery},
		{976562.5, 2048, 1800 * time.Microsecond, streamPumpEvery},
		{1953125, 2048, 900 * time.Microsecond, 1000 * time.Microsecond},
		{61035, 2048, streamPumpEvery, streamPumpEvery},
		{20e6, 2048, streamPumpFastest, streamPumpFastest},
		{976562.5, 0, streamPumpEvery, streamPumpEvery},
	} {
		if got := pumpEvery(c.rate, c.bank); got < c.min || got > c.max {
			t.Errorf("rate %g bank %d: %v, want %v..%v", c.rate, c.bank, got, c.min, c.max)
		}
	}
}
