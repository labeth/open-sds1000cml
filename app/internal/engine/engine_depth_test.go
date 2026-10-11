// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
package engine

import "testing"

// TRLC-LINKS: REQ-SDS-008, REQ-SDS-009
func TestMemDepth(t *testing.T) {
	// The configurable decimated drain depth (fps↔data): a deeper setting drains
	// more samples per frame. Clamped to [decimWin, maxRecordCols] — the
	// fabric finalizes at most PRETRIG_MAX words, never the physical depth.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	if got := e.SetMemDepth(14336); got != 14336 {
		t.Fatalf("SetMemDepth(14336) = %d", got)
	}
	e.bringUp()
	e.oneFrame(false)
	if f, _ := e.Consume(); f.Valid != 14336 {
		t.Fatalf("deep drain: Valid=%d, want 14336", f.Valid)
	}
	if got := e.SetMemDepth(999999); got != maxRecordCols {
		t.Fatalf("clamp high = %d, want %d", got, maxRecordCols)
	}
	if got := e.SetMemDepth(10); got != decimWin {
		t.Fatalf("clamp low = %d, want %d", got, decimWin)
	}
}

// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func TestSingleForcesFullDepth(t *testing.T) {
	// A SINGLE capture ignores the shallow mem-depth setting and drains the FULL
	// record — the one frame you keep carries everything to zoom into. The
	// record asked for must be one the fabric finalizes whole: no short drain.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	e.SetMemDepth(decimWin) // shallowest
	e.SetSingle()
	e.bringUp()
	e.oneFrame(false)
	if f, _ := e.Consume(); f.Valid != maxRecordCols || !f.Coherent {
		t.Fatalf("SINGLE drain: Valid=%d coherent=%v, want %d/true", f.Valid, f.Coherent, maxRecordCols)
	}
	if s := e.Snapshot(); s.ShortDrains != 0 {
		t.Fatalf("SINGLE drain asked the fabric for more than it finalizes: %d short drains (pre=%d post=%d)",
			s.ShortDrains, fb.pre, fb.post)
	}
}

// TRLC-LINKS: REQ-SDS-009
func TestStreamMode(t *testing.T) {
	// Stitched streaming: SetStreamMode forces the deep record + un-paces, and
	// stitchFrame publishes EVERY window raw + edge-agnostic with continuity
	// metadata (StreamSeq advances, WindowNs set) for the client to stitch.
	fb := newFakeBus()
	e, _ := newTestEngine(t, fb)
	if !e.SetStreamMode(true) {
		t.Fatal("SetStreamMode(true) not applied")
	}
	e.bringUp()
	for i := 0; i < 3; i++ {
		e.stitchFrame(false)
	}
	s := e.Snapshot()
	if s.Published != 3 || !s.Stream {
		t.Fatalf("stream: published=%d stream=%v, want 3/true", s.Published, s.Stream)
	}
	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("stream frame never reached the arena")
	}
	if f.StreamSeq == 0 || f.WindowNs == 0 {
		t.Fatalf("stream continuity metadata: seq=%d window_ns=%d", f.StreamSeq, f.WindowNs)
	}
	if f.Valid != maxRecordCols {
		t.Fatalf("stream drains the full record: Valid=%d, want %d", f.Valid, maxRecordCols)
	}
	if f.EdgeX >= 0 {
		t.Fatalf("stream window must be edge-agnostic (raw contiguous), EdgeX=%v", f.EdgeX)
	}
	e.SetStreamMode(false)
}

// TRLC-LINKS: REQ-SDS-008, REQ-SDS-011
func TestSingleShotStopsAfterCapture(t *testing.T) {
	// SINGLE arms NORM and must STOP after the first triggered publish.
	fb := newFakeBus() // doneOnGo=true → NORM triggers immediately
	// A 2-period square in the 2048 window (like a real multi-wave display),
	// so the slope validation sees a clean adjacent plateau.
	fb.mu.Lock()
	fb.wave = func(i int) (uint8, uint8) {
		if (i/512)%2 == 0 {
			return 200, 60
		}
		return 56, 190
	}
	fb.mu.Unlock()
	e, _ := newTestEngine(t, fb)
	e.SetSingle()
	if s := e.Snapshot(); !s.Single || !s.Running {
		t.Fatalf("after SetSingle: single=%v running=%v", s.Single, s.Running)
	}
	e.bringUp()
	e.oneFrame(true) // NORM triggered → publishes, then single stops
	s := e.Snapshot()
	if s.Published != 1 {
		t.Fatalf("single did not capture: published=%d", s.Published)
	}
	if s.Single || s.Running {
		t.Fatalf("single did not stop: single=%v running=%v", s.Single, s.Running)
	}
	// RUN cancels a pending single-shot.
	e.SetSingle()
	e.SetRunning(true)
	if s := e.Snapshot(); s.Single {
		t.Fatal("RUN did not cancel the pending single-shot")
	}
}

// SINGLE waits in NORM, and the next RUN brings back the mode it replaced;
// an explicit AUTO/NORM choice meanwhile is kept (bug hunt 2026-10-10: AUTO
// stayed NORM for good after one SINGLE).
// TRLC-LINKS: REQ-SDS-009, REQ-SDS-011
func TestSingleRestoresTriggerMode(t *testing.T) {
	e, _ := newTestEngine(t, newFakeBus())
	norm := func() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.norm }
	e.SetNorm(false)
	e.SetSingle()
	if !norm() {
		t.Fatal("SINGLE should wait in NORM")
	}
	e.SetRunning(false) // the single completed
	e.SetRunning(true)
	if norm() {
		t.Fatal("RUN after SINGLE should restore AUTO")
	}
	e.SetNorm(true)
	e.SetSingle()
	e.SetRunning(true)
	if !norm() {
		t.Fatal("RUN after SINGLE from NORM should stay NORM")
	}
	e.SetNorm(false)
	e.SetSingle()
	e.SetNorm(true) // the user chose NORM during the single
	e.SetRunning(true)
	if !norm() {
		t.Fatal("an explicit mode choice must survive RUN")
	}
}
