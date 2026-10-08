// ENGMODEL-OWNER-UNIT: FU-APP-FRAMES
package frames

import (
	"sync/atomic"
	"testing"
	"time"

	"open-sds/app/internal/engine"
)

// tickSource publishes a fresh frame with an advancing Seq each time `fresh`
// is armed; otherwise it re-presents the last frame as stale.
// TRLC-LINKS: REQ-SDS-020
type tickSource struct {
	seq   atomic.Uint64
	fresh atomic.Bool
	f     engine.Frame
}

// TRLC-LINKS: REQ-SDS-020
func (s *tickSource) Consume() (*engine.Frame, bool) {
	if !s.fresh.Swap(false) {
		return &s.f, false
	}
	s.f.Seq = s.seq.Add(1)
	s.f.Valid = 4
	s.f.C1 = []uint8{1, 2, 3, 4}
	s.f.C2 = []uint8{4, 3, 2, 1}
	return &s.f, true
}

// TRLC-LINKS: REQ-SDS-020
func TestWaitNextWakesOnPublish(t *testing.T) {
	src := &tickSource{}
	fo := New()
	stop := make(chan struct{})
	defer close(stop)
	go fo.Run(src, stop)

	src.fresh.Store(true)
	if seq := fo.WaitNext(0, time.Second); seq != 1 {
		t.Fatalf("WaitNext(0) = %d, want 1", seq)
	}
	// Parked waiter wakes on the NEXT publish.
	done := make(chan uint64, 1)
	go func() { done <- fo.WaitNext(1, time.Second) }()
	time.Sleep(20 * time.Millisecond) // let it park
	src.fresh.Store(true)
	select {
	case seq := <-done:
		if seq != 2 {
			t.Fatalf("parked WaitNext = %d, want 2", seq)
		}
	case <-time.After(time.Second):
		t.Fatal("parked WaitNext never woke")
	}
}

// TRLC-LINKS: REQ-SDS-020
func TestWaitNextTimesOutIdle(t *testing.T) {
	src := &tickSource{}
	fo := New()
	stop := make(chan struct{})
	defer close(stop)
	go fo.Run(src, stop)

	src.fresh.Store(true)
	if seq := fo.WaitNext(0, time.Second); seq != 1 {
		t.Fatalf("setup publish: seq = %d", seq)
	}
	t0 := time.Now()
	seq := fo.WaitNext(1, 120*time.Millisecond) // nothing new coming
	if el := time.Since(t0); el < 100*time.Millisecond || el > 500*time.Millisecond {
		t.Fatalf("timeout took %v, want ~120ms", el)
	}
	if seq != 1 {
		t.Fatalf("idle WaitNext = %d, want unchanged 1", seq)
	}
}

// TRLC-LINKS: REQ-SDS-020
func TestWaitNextConcurrentWithReaders(t *testing.T) {
	src := &tickSource{}
	fo := New()
	stop := make(chan struct{})
	defer close(stop)
	go fo.Run(src, stop)

	// Hammer WithFrame while frames publish and waiters park (race detector food).
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			fo.WithFrame(func(f *engine.Frame) {})
			time.Sleep(time.Millisecond)
		}
		close(done)
	}()
	last := uint64(0)
	for i := 0; i < 3; i++ {
		src.fresh.Store(true)
		got := fo.WaitNext(last, time.Second)
		if got == last {
			t.Fatalf("iteration %d: no advance from %d", i, last)
		}
		last = got
	}
	<-done
}

// TRLC-LINKS: REQ-SDS-020
func TestPrecisionCopyOwnsStorage(t *testing.T) {
	src := &engine.Frame{Valid: 1, C1: []byte{100}, C2: []byte{101}, Q1: []uint16{25601}, Q2: []uint16{25855}}
	var dst engine.Frame
	copyFrame(&dst, src)
	src.Q1[0] = 0
	src.Q2[0] = 0
	if dst.Q1[0] != 25601 || dst.Q2[0] != 25855 {
		t.Fatal("precision frame alias")
	}
	src.Q1 = nil
	src.Q2 = nil
	copyFrame(&dst, src)
	if len(dst.Q1) != 0 || len(dst.Q2) != 0 {
		t.Fatal("stale precision after raw frame")
	}
}
