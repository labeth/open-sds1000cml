// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import (
	"testing"
	"time"
)

// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011, REQ-SDS-025
func TestHoldoffRespondsToOperator(t *testing.T) {
	for _, name := range []string{"clear", "stop", "force", "shutdown"} {
		t.Run(name, func(t *testing.T) {
			e, _ := newTestEngine(t, newFakeBus())
			e.SetHoldoff(10)
			sleep := e.clk.Sleep
			e.clk.Sleep = func(d time.Duration) {
				sleep(d)
				switch name {
				case "clear":
					e.SetHoldoff(0)
				case "stop":
					e.SetRunning(false)
				case "force":
					e.ForceTrigger()
				case "shutdown":
					e.stopReq.Store(true)
				}
			}
			start := e.clk.Now()
			e.paceHoldWithFloor(start, true, 0)
			if elapsed := e.clk.Now().Sub(start); elapsed != 50*time.Millisecond {
				t.Fatalf("%s waited %v behind stale holdoff", name, elapsed)
			}
		})
	}
}

// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func TestSRAMUntriggeredHasNoHoldoff(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	e.SetHoldoff(10)
	start := e.clk.Now()
	e.paceHoldWithFloor(start, false, 0)
	if elapsed := e.clk.Now().Sub(start); elapsed != 0 {
		t.Fatalf("untriggered AUTO frame delayed %v", elapsed)
	}
}
