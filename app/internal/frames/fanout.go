// Package frames fans the engine's single-consumer arena out to multiple
// readers (web UI + LCD renderer). The arena's Consume contract permits ONE
// consumer (spec 07 §7: the read slot is private to it) — the fan-out is
// that consumer, and everyone else works on its deep copy under an RWMutex.
package frames

import (
	"sync"
	"time"

	"open-sds/app/internal/engine"
)

// Source is the single-consumer frame producer (the engine).
type Source interface {
	Consume() (*engine.Frame, bool)
}

type Fanout struct {
	mu     sync.RWMutex
	latest engine.Frame
	has    bool
	wake   chan struct{} // closed+replaced under mu on every fresh snapshot
}

func New() *Fanout { return &Fanout{wake: make(chan struct{})} }

// Run polls the source at the display cadence (spec 07 §8: 50 ms hard
// minimum) and snapshots fresh frames. Blocks; run as a goroutine.
func (fo *Fanout) Run(src Source, stop <-chan struct{}) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			f, fresh := src.Consume()
			if f == nil || !fresh {
				continue
			}
			fo.mu.Lock()
			copyFrame(&fo.latest, f)
			fo.has = true
			close(fo.wake) // wake WaitNext parkers; close never blocks,
			fo.wake = make(chan struct{}) // so a stuck waiter can't stall this tick
			fo.mu.Unlock()
		}
	}
}

// WaitNext parks until the fan-out snapshots a frame with Seq != last,
// returning the new Seq, or until timeout elapses, returning whatever Seq is
// current (possibly last, possibly 0 if nothing has been published). No lock
// is held while parked, and Run can never block on a parker (it only closes
// the wake channel). The timer is created once, not per wake iteration.
func (fo *Fanout) WaitNext(last uint64, timeout time.Duration) uint64 {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		fo.mu.RLock()
		var seq uint64
		if fo.has {
			seq = fo.latest.Seq
		}
		wake := fo.wake
		fo.mu.RUnlock()
		if seq != 0 && seq != last {
			return seq
		}
		select {
		case <-wake:
		case <-deadline.C:
			return seq
		}
	}
}

// WithFrame runs fn on the latest snapshot under the read lock; fn must not
// retain the pointer. fn receives nil when nothing has been published yet.
func (fo *Fanout) WithFrame(fn func(*engine.Frame)) {
	fo.mu.RLock()
	defer fo.mu.RUnlock()
	if !fo.has {
		fn(nil)
		return
	}
	fn(&fo.latest)
}

func copyFrame(dst, src *engine.Frame) {
	c1, c2 := dst.C1, dst.C2
	e1n, e1x, e2n, e2x := dst.EnvMin, dst.EnvMax, dst.EnvMin2, dst.EnvMax2
	*dst = *src
	dst.C1 = append(c1[:0], src.C1[:src.Valid]...)
	dst.C2 = append(c2[:0], src.C2[:src.Valid]...)
	dst.EnvMin = append(e1n[:0], src.EnvMin...)
	dst.EnvMax = append(e1x[:0], src.EnvMax...)
	dst.EnvMin2 = append(e2n[:0], src.EnvMin2...)
	dst.EnvMax2 = append(e2x[:0], src.EnvMax2...)
}
