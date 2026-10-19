// ENGMODEL-OWNER-UNIT: FU-APP-BUS
package bus

import (
	"testing"
	"time"
)

// The worker's pumps take at most half the core: after a pump it waits for
// a request as long as the pump took (500 us at least). At real-time
// priority, a bus whose events never let up kept it pumping nine tenths of
// the time and starved the app and the OTA agent.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-001
func TestWorkerPumpsTakeAtMostHalfTheCore(t *testing.T) {
	for _, busy := range []time.Duration{0, 100 * time.Microsecond, 2 * time.Millisecond, 15 * time.Millisecond} {
		wait := workerPollWait(busy)
		if wait < 500*time.Microsecond || busy > 0 && float64(busy)/float64(busy+wait) > 0.5 {
			t.Fatalf("a %v pump waits %v: duty %.0f %%", busy, wait, 100*float64(busy)/float64(busy+wait))
		}
	}
}
