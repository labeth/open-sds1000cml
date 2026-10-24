// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// The display's period grows while the running engine is below its frame
// rate, to 30 Hz at most, shrinks back to the panel's 52 Hz with room above
// it, and holds in between; stopped, the display runs at the panel's rate.
// TRLC-LINKS: REQ-SDS-168
func TestLCDGovern(t *testing.T) {
	run := func(fps float64) engine.Stats { return engine.Stats{Running: true, FPS: fps} }
	p := lcdFramePeriod
	for i := 0; i < 100; i++ {
		p = lcdGovern(p, run(55))
	}
	if p != lcdSlowestPeriod {
		t.Fatalf("slow engine: period %v, want %v", p, lcdSlowestPeriod)
	}
	if q := lcdGovern(p, run(62)); q != p {
		t.Fatalf("engine at 62 fps: period %v, want it held at %v", q, p)
	}
	for i := 0; i < 100; i++ {
		p = lcdGovern(p, run(70))
	}
	if p != lcdFramePeriod {
		t.Fatalf("engine with room: period %v, want %v", p, lcdFramePeriod)
	}
	if q := lcdGovern(lcdSlowestPeriod, engine.Stats{FPS: 0}); q != lcdFramePeriod {
		t.Fatalf("stopped: period %v, want %v", q, lcdFramePeriod)
	}
	if lcdFramePeriod > time.Second/52 || lcdSlowestPeriod < lcdFramePeriod {
		t.Fatalf("bounds %v..%v", lcdFramePeriod, lcdSlowestPeriod)
	}
}
