package engine

import "testing"

// Write() must hand back a slot with the mode-specific metadata cleared, so a
// stream window's StreamSeq/WindowNs/GapNs (or a triggered frame's TrigPos) can't
// leak into the next producer that leaves those fields untouched.
func TestArenaWriteClearsModeMetadata(t *testing.T) {
	a := newArena(16)
	f := a.Write()
	f.StreamSeq, f.WindowNs, f.GapNs, f.TrigPos = 5, 100, 50, 7 // as if a stream+trig frame filled it
	// The next producer takes its private slot via Write(); it must be clean of
	// the mode-specific metadata regardless of what the previous frame set.
	g := a.Write()
	if g.StreamSeq != 0 || g.WindowNs != 0 || g.GapNs != 0 || g.TrigPos != 0 {
		t.Fatalf("Write() left stale metadata: StreamSeq=%d WindowNs=%d GapNs=%d TrigPos=%d",
			g.StreamSeq, g.WindowNs, g.GapNs, g.TrigPos)
	}
}

func TestArenaPublishConsume(t *testing.T) {
	a := newArena(16)

	// Nothing published yet: consumer gets its (empty) held slot, not fresh.
	if _, fresh := a.Consume(); fresh {
		t.Fatal("fresh before any publish")
	}

	w := a.Write()
	w.C1[0] = 42
	w.Seq = 1
	a.Publish()

	f, fresh := a.Consume()
	if !fresh || f.Seq != 1 || f.C1[0] != 42 {
		t.Fatalf("consume: fresh=%v seq=%d c1[0]=%d", fresh, f.Seq, f.C1[0])
	}

	// No new publish → the held frame is re-presented, not fresh.
	f2, fresh := a.Consume()
	if fresh || f2 != f {
		t.Fatal("held frame not re-presented")
	}

	// Triple-buffer no-tear: after the consumer took frame 1, two more
	// publishes must never drain into the consumer's held slot.
	held := f
	heldVal := held.C1[0]
	for seq := uint64(2); seq <= 3; seq++ {
		w := a.Write()
		if w == held {
			t.Fatal("producer handed the consumer's held slot")
		}
		w.C1[0] = uint8(seq * 10)
		w.Seq = seq
		a.Publish()
	}
	if held.C1[0] != heldVal {
		t.Fatal("consumer's held frame was overwritten (tear)")
	}
	f3, fresh := a.Consume()
	if !fresh || f3.Seq != 3 {
		t.Fatalf("drop-newest: got seq %d fresh=%v, want 3 fresh", f3.Seq, fresh)
	}
}
