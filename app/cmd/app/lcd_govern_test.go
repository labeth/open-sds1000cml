// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// The display's period grows while the running engine is below its frame
// rate with the core busy, to 15 Hz at most, shrinks back to the panel's
// 52 Hz with room above it or with the core waiting (the record, not the
// display, holds the engine back), and holds in between; stopped, the
// display runs at the panel's rate.
// TRLC-LINKS: REQ-SDS-168
func TestLCDGovern(t *testing.T) {
	run := func(fps float64) engine.Stats { return engine.Stats{Running: true, FPS: fps} }
	p := lcdFramePeriod
	for i := 0; i < 100; i++ {
		p = lcdGovern(p, run(55), 0.99)
	}
	if p != lcdSlowestPeriod {
		t.Fatalf("slow engine, busy core: period %v, want %v", p, lcdSlowestPeriod)
	}
	if q := lcdGovern(p, run(62), 0.95); q != p {
		t.Fatalf("engine at 62 fps: period %v, want it held at %v", q, p)
	}
	if q := lcdGovern(p, run(55), 0.87); q != p {
		t.Fatalf("slow engine, core between: period %v, want it held at %v", q, p)
	}
	waiting := p
	for i := 0; i < 100; i++ {
		waiting = lcdGovern(waiting, run(31), 0.70)
	}
	if waiting != lcdFramePeriod {
		t.Fatalf("engine waiting on its records: period %v, want %v", waiting, lcdFramePeriod)
	}
	for i := 0; i < 100; i++ {
		p = lcdGovern(p, run(70), 0.95)
	}
	if p != lcdFramePeriod {
		t.Fatalf("engine with room: period %v, want %v", p, lcdFramePeriod)
	}
	if q := lcdGovern(lcdSlowestPeriod, engine.Stats{FPS: 0}, 1); q != lcdFramePeriod {
		t.Fatalf("stopped: period %v, want %v", q, lcdFramePeriod)
	}
	if lcdFramePeriod > time.Second/52 || lcdSlowestPeriod < lcdFramePeriod {
		t.Fatalf("bounds %v..%v", lcdFramePeriod, lcdSlowestPeriod)
	}
}

// cpuTimes reads /proc/stat's first line: every time summed, idle and
// iowait apart; anything else is refused.
// TRLC-LINKS: REQ-SDS-168
func TestCPUTimes(t *testing.T) {
	total, idle, ok := cpuTimes([]byte("cpu  100 2 30 400 5 6 7 0 0 0\ncpu0 100 2 30 400 5 6 7 0 0 0\n"))
	if !ok || total != 550 || idle != 405 {
		t.Fatalf("total %d idle %d ok %v, want 550 405 true", total, idle, ok)
	}
	for _, bad := range []string{"", "intr 1 2 3", "cpu  1 2\n"} {
		if _, _, ok := cpuTimes([]byte(bad)); ok {
			t.Fatalf("%q read as cpu times", bad)
		}
	}
}
