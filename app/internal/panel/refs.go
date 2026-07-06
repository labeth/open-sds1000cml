package panel

import "open-sds/app/internal/engine"

// refWave is a saved snapshot of both channels' display codes, overlaid dim for
// comparison (parity with the web REF A/B). Screen-space: it lines up while the
// timebase/scale are unchanged — the bench "save a good trace, tweak, compare".
type refWave struct {
	c1, c2 []uint8
	has    bool
	show   bool
}

// RefWave is the render-side view of a saved reference.
type RefWave struct {
	C1, C2 []uint8
	Show   bool
}

// captureRef snapshots the current frame into reference slot (0=A, 1=B).
func (c *Controller) captureRef(slot int) {
	if c.frameFn == nil || slot < 0 || slot > 1 {
		return
	}
	c.frameFn(func(f *engine.Frame) {
		if f == nil || len(f.C1) == 0 || f.IsEnv {
			return
		}
		valid := f.Valid
		if valid < 1 {
			valid = 1
		}
		if valid > len(f.C1) {
			valid = len(f.C1)
		}
		r := refWave{has: true, show: true}
		r.c1 = append([]uint8(nil), f.C1[:valid]...)
		if len(f.C2) >= valid {
			r.c2 = append([]uint8(nil), f.C2[:valid]...)
		}
		c.mu.Lock()
		c.refs[slot] = r
		c.mu.Unlock()
	})
}

// RefView returns a copy-free snapshot of both reference slots for the renderer
// (the slices are immutable once captured).
func (c *Controller) RefView() [2]RefWave {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [2]RefWave
	for i, r := range c.refs {
		if r.has {
			out[i] = RefWave{C1: r.c1, C2: r.c2, Show: r.show}
		}
	}
	return out
}
