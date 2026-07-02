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
}

func New() *Fanout { return &Fanout{} }

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
			fo.mu.Unlock()
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
