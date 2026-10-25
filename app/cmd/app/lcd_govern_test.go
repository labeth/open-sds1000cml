// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// The display's period grows while the running engine is below its frame
// rate with the core busy, to 15 Hz, and then the spectrum hold grows, to
// half a second; with room above the rate or the core waiting (the record,
// not the display, holds the engine back) the hold goes first, then the
// period comes back to the panel's 52 Hz; in between both hold; stopped,
// the display runs at the panel's rate with no hold.
// TRLC-LINKS: REQ-SDS-168
func TestLCDGovern(t *testing.T) {
	run := func(fps float64) engine.Stats { return engine.Stats{Running: true, FPS: fps} }
	p, h := lcdFramePeriod, time.Duration(0)
	for i := 0; i < 30 && p < lcdSlowestPeriod; i++ {
		p, h = lcdGovern(p, h, run(55), 0.99)
		if h != 0 {
			t.Fatalf("hold %v before the period reached its longest (%v)", h, p)
		}
	}
	for i := 0; i < 100; i++ {
		p, h = lcdGovern(p, h, run(55), 0.99)
	}
	if p != lcdSlowestPeriod || h != lcdLongestHold {
		t.Fatalf("slow engine, busy core: period %v hold %v, want %v %v", p, h, lcdSlowestPeriod, lcdLongestHold)
	}
	if q, g := lcdGovern(p, h, run(62), 0.95); q != p || g != h {
		t.Fatalf("engine at 62 fps: %v %v, want them held at %v %v", q, g, p, h)
	}
	if q, g := lcdGovern(p, h, run(55), 0.87); q != p || g != h {
		t.Fatalf("slow engine, core between: %v %v, want them held at %v %v", q, g, p, h)
	}
	q, g := p, h
	for g > 0 {
		q, g = lcdGovern(q, g, run(31), 0.70)
		if g > 0 && q != lcdSlowestPeriod {
			t.Fatalf("period %v moved while the hold %v went", q, g)
		}
	}
	for i := 0; i < 100; i++ {
		q, g = lcdGovern(q, g, run(31), 0.70)
	}
	if q != lcdFramePeriod || g != 0 {
		t.Fatalf("engine waiting on its records: %v %v, want %v 0", q, g, lcdFramePeriod)
	}
	for i := 0; i < 200; i++ {
		p, h = lcdGovern(p, h, run(70), 0.95)
	}
	if p != lcdFramePeriod || h != 0 {
		t.Fatalf("engine with room: %v %v, want %v 0", p, h, lcdFramePeriod)
	}
	if q, g := lcdGovern(lcdSlowestPeriod, lcdLongestHold, engine.Stats{FPS: 0}, 1); q != lcdFramePeriod || g != 0 {
		t.Fatalf("stopped: %v %v, want %v 0", q, g, lcdFramePeriod)
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
