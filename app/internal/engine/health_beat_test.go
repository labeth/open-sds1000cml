package engine

import (
	"testing"
	"time"
)

// The OTA health contract: the beat counter must advance THROUGH every
// legitimate long wait, or the agent (3 s staleness) kills a healthy app.
// Holdoff pacing is the worst case — user-settable to 10 s between frames
// (live-storm finding: a frame-keyed token got the app killed mid-storm).
func TestHealthBeatsThroughHoldoff(t *testing.T) {
	fb := newFakeBus()
	e, clk := newTestEngine(t, fb)
	e.SetFramePeriod(0)
	if got := e.SetHoldoff(10); got != 10 {
		t.Fatalf("holdoff clamp: %v", got)
	}
	b0 := e.Beats()
	start := e.clk.Now()
	e.paceHold(start, true) // a triggered frame: sleeps the full 10 s holdoff
	if d := clk.t.Sub(start); d < 10*time.Second {
		t.Fatalf("holdoff not applied: %v", d)
	}
	beats := e.Beats() - b0
	// 10 s in ≤500 ms slices = ≥20 beats; the supervisor needs ≥1 per 3 s.
	if beats < 15 {
		t.Fatalf("only %d beats through a 10 s holdoff — the health token would go stale", beats)
	}
	// beats also advance on plain frames
	e.SetHoldoff(0)
	e.bringUp()
	b1 := e.Beats()
	e.oneFrame(false)
	if e.Beats() == b1 {
		t.Fatal("no beat on a normal frame")
	}
}
