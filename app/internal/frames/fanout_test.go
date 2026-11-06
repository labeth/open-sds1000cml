// ENGMODEL-OWNER-UNIT: FU-APP-FRAMES

package frames

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labeth/open-sds1000cml/app/internal/engine"
)

// The fan-out snapshots the engine's frame with CopyFrame: the snapshot
// owns its codes.
// TRLC-LINKS: REQ-SDS-020
func TestFanoutSnapshotsEngineFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &engineSource{f: engine.Frame{Seq: 1, Valid: 2, C1: []uint8{1, 2}, C2: []uint8{3, 4}}}
		fo := New()
		stop := make(chan struct{})
		defer close(stop)
		go fo.Run(src, stop)
		if seq := fo.WaitNext(0, time.Second); seq != 1 {
			t.Fatalf("WaitNext = %d, want 1", seq)
		}
		src.f.C1[0] = 99
		fo.WithFrame(func(f *engine.Frame) {
			if f.C1[0] != 1 || f.C2[1] != 4 {
				t.Errorf("snapshot aliases the engine frame: %v %v", f.C1, f.C2)
			}
		})
	})
}

// engineSource presents one fresh frame, then only stale ones.
// TRLC-LINKS: REQ-SDS-020
type engineSource struct {
	f    engine.Frame
	sent atomic.Bool
}

// TRLC-LINKS: REQ-SDS-020
func (s *engineSource) Consume() (*engine.Frame, bool) { return &s.f, !s.sent.Swap(true) }

// TRLC-LINKS: REQ-SDS-020
func TestPrecisionCopyOwnsStorage(t *testing.T) {
	src := &engine.Frame{Valid: 1, C1: []byte{100}, C2: []byte{101}, Q1: []uint16{25601}, Q2: []uint16{25855}}
	var dst engine.Frame
	CopyFrame(&dst, src)
	src.Q1[0] = 0
	src.Q2[0] = 0
	if dst.Q1[0] != 25601 || dst.Q2[0] != 25855 {
		t.Fatal("precision frame alias")
	}
	src.Q1 = nil
	src.Q2 = nil
	CopyFrame(&dst, src)
	if len(dst.Q1) != 0 || len(dst.Q2) != 0 {
		t.Fatal("stale precision after raw frame")
	}
}
