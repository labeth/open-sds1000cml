package engine

import "testing"

// The vendor native-fast half-record / period-5 dead-tail / degraded / stuck-FSM
// machinery is DELETED with the round-robin drain: the owned fabric drains a
// clean, contiguous full record through the single auto-inc BURST port and
// freezes it statically (fpga doc §7), so there is no half-capture to detect or
// re-capture. These tests pin the owned equivalent — a clean full-record drain.

// TestBurstDrainIsCleanFullRecord: a native-fast triggered capture drains the
// whole deep record from BURST in order, coherent and never Degraded.
func TestBurstDrainIsCleanFullRecord(t *testing.T) {
	fb := newFakeBus()
	fb.trigOnGo = true
	e, _ := newTestEngine(t, fb)
	b, ok := PlanTdiv(1e-6)
	if !ok {
		t.Fatal("1 µs not in ladder")
	}
	e.band = b
	e.bringUp()
	e.oneFrame(false)

	f, fresh := e.Consume()
	if !fresh {
		t.Fatal("native-fast triggered frame not published")
	}
	if f.Valid != deepRecord || !f.Interp {
		t.Fatalf("drain geometry: valid=%d interp=%v, want %d/true", f.Valid, f.Interp, deepRecord)
	}
	if f.Degraded {
		t.Fatal("owned burst drain must never be Degraded (no half-record)")
	}
	if !f.Coherent {
		t.Fatal("clean full-record drain must be Coherent")
	}
	// Content starts at BURST sample 0 (hi byte C1, lo byte C2).
	if f.C1[0] != 200 || f.C2[0] != 60 {
		t.Fatalf("drain content: C1[0]=%d C2[0]=%d, want 200/60", f.C1[0], f.C2[0])
	}
	// The record is drained entirely from the single BURST port — one pop per
	// sample, exactly cols reads (no round-robin, no over-read).
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if fb.earlyDrain {
		t.Fatal("BURST read before halt (live-buffer trap)")
	}
	if fb.burstReads != deepRecord {
		t.Fatalf("burst pops = %d, want exactly %d (single auto-inc port)", fb.burstReads, deepRecord)
	}
}
