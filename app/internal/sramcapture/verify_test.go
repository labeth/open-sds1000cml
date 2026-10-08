// ENGMODEL-OWNER-UNIT: FU-APP-SRAMCAPTURE
package sramcapture

import (
	"context"
	"testing"
)

// TRLC-LINKS: REQ-SDS-034
func TestStartupCounterChecksFullRecord(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		f := frozenBus()
		f.revision = 10
		f.staged[15] = 500
		f.staged[19] = 2
		f.base = 0
		f.corruptBurstHead = true
		if wrong {
			f.base = 1
		}
		c := client(t, f)
		err := c.VerifyCounter(context.Background())
		if (err != nil) != wrong {
			t.Fatalf("wrong=%v error=%v", wrong, err)
		}
		if f.length != Words || f.staged[6] != 0 {
			t.Fatal("did not use full counter record")
		}
	}
}
