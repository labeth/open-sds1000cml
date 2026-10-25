// ENGMODEL-OWNER-UNIT: FU-APP-APP
package main

import (
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// With a web page showing the scope the engine comes first: under its rate
// on a busy core the display's period grows, to 20 Hz, then the spectrum
// hold, to a 20th of a second; with room or the core waiting the hold goes
// first, then the period comes back to the panel's 52 Hz. A display still
// under 20 pictures a second paces the engine (to 20 frames a second at
// most) while it falls behind the frames it is given, and stops pacing it
// once over 21 or keeping up.
// TRLC-LINKS: REQ-SDS-168
func TestLCDGovPage(t *testing.T) {
	run := func(fps float64) engine.Stats { return engine.Stats{Running: true, FPS: fps} }
	g := newLCDGov()
	for i := 0; i < 200; i++ {
		g.step(run(55), 25, 0.99, true)
	}
	if g.period != lcdSlowestPeriod || g.hold != lcdLongestHold || g.pace != 0 {
		t.Fatalf("slow engine, busy core, display at 25: %+v", *g)
	}
	for i := 0; i < 200; i++ {
		g.step(run(55), 15, 0.99, true)
	}
	if g.pace != lcdLongestPace {
		t.Fatalf("display under 20: pace %v, want %v", g.pace, lcdLongestPace)
	}
	for i := 0; i < 200; i++ {
		g.step(run(20), 19, 0.99, true)
	}
	if g.pace != 0 {
		t.Fatalf("display keeping up with a paced engine: pace %v, want 0", g.pace)
	}
	for i := 0; i < 200; i++ {
		g.step(run(70), 40, 0.95, true)
	}
	if g.period != lcdFramePeriod || g.hold != 0 || g.pace != 0 {
		t.Fatalf("engine with room: %+v", *g)
	}
	for i := 0; i < 200; i++ {
		g.step(run(31), 31, 0.70, true)
	}
	if g.period != lcdFramePeriod || g.hold != 0 {
		t.Fatalf("engine waiting on its records: %+v", *g)
	}
	g.step(engine.Stats{}, 0, 1, true)
	if *g != (lcdGov{period: lcdFramePeriod}) {
		t.Fatalf("stopped: %+v", *g)
	}
	if lcdSlowestPeriod > time.Second/20 || lcdLongestHold > time.Second/20 || lcdLongestPace > time.Second/20 {
		t.Fatalf("governed below 20 Hz: %v %v %v", lcdSlowestPeriod, lcdLongestHold, lcdLongestPace)
	}
}

// With no page the display runs at the panel's rate, a new spectrum each
// picture; while it falls behind the engine's frames on a busy core the
// engine is paced (to 20 frames a second at most), and the pace comes off
// once the display keeps up.
// TRLC-LINKS: REQ-SDS-168
func TestLCDGovNoPage(t *testing.T) {
	run := func(fps float64) engine.Stats { return engine.Stats{Running: true, FPS: fps} }
	g := &lcdGov{period: lcdSlowestPeriod, hold: lcdLongestHold}
	g.step(run(50), 48, 0.99, false)
	if g.period != lcdFramePeriod || g.hold != 0 {
		t.Fatalf("no page: %+v", *g)
	}
	for i := 0; i < 200; i++ {
		g.step(run(40), 20, 0.99, false)
	}
	if g.pace != lcdLongestPace {
		t.Fatalf("display behind a busy engine: pace %v, want %v", g.pace, lcdLongestPace)
	}
	for i := 0; i < 200; i++ {
		g.step(run(45), 45, 0.99, false)
	}
	if g.pace != 0 {
		t.Fatalf("display keeping up: pace %v, want 0", g.pace)
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
