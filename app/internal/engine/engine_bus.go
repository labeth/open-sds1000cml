package engine

import (
	"open-sds/app/internal/bus"
	"time"
)

// ReadMatrix requests a key-matrix snapshot from the bus owner (spec 08 §4):
// non-blocking enqueue (ok=false when the queue is full), 200 ms reply
// timeout — the panel worker simply retries on the next interrupt or tick.
func (e *Engine) ReadMatrix() ([5]uint16, bool) {
	reply := make(chan [5]uint16, 1)
	select {
	case e.matrixReq <- reply:
	default:
		return [5]uint16{}, false
	}
	select {
	case m := <-reply:
		return m, true
	case <-time.After(200 * time.Millisecond):
		return [5]uint16{}, false
	}
}

// SetLEDs stages the panel LED latch word (spec 08 §5): compare-on-change
// with an init flag; the owner flushes the 4-write strobe at the boundary.
func (e *Engine) SetLEDs(word uint16) {
	e.mu.Lock()
	if !e.ledInit || word != e.ledWord {
		e.ledWord, e.ledDirty, e.ledInit = word, true, true
	}
	e.mu.Unlock()
}

// Beats is the liveness heartbeat for the OTA health contract: it advances on
// every loop iteration AND inside every legitimate long wait (holdoff pacing,
// budget polls, recovery bring-up). The health token must key on THIS, not on
// frame count alone — a 10 s holdoff between frames is a healthy scope, but
// with a 3 s supervisor staleness window a frame-keyed token reads as a wedge
// and the agent kills a perfectly healthy app (found by the live storm).
func (e *Engine) Beats() uint64 { return e.beatN.Load() }

// sleepBeating sleeps d in ≤500 ms slices, beating each slice so long pacing
// stays visibly alive to the supervisor; aborts early on a stop request.
func (e *Engine) sleepBeating(d time.Duration) {
	for d > 0 && !e.stopReq.Load() {
		s := d
		if s > 500*time.Millisecond {
			s = 500 * time.Millisecond
		}
		e.clk.Sleep(s)
		e.beatN.Add(1)
		d -= s
	}
}

func (e *Engine) runWord() uint16 {
	if e.normNow() {
		return runNorm
	}
	return runAuto
}

func (e *Engine) w(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS1, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) w3(sel, val uint16) {
	if err := e.b.Write(bus.PlaneCS3, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) r(sel uint16) uint16 {
	v, err := e.b.Read(bus.PlaneCS1, sel)
	if err != nil {
		e.busErr(err)
	}
	return v
}

func (e *Engine) busErr(err error) {
	e.mu.Lock()
	e.stats.BusErrors++
	n := e.stats.BusErrors
	e.mu.Unlock()
	if n <= 5 || n%100 == 0 {
		e.logf("engine: bus error #%d: %v", n, err)
	}
}

func (e *Engine) resetDeadRuns() {
	e.deadRuns = 0
	e.mu.Lock()
	e.stats.DeadRuns = 0
	e.mu.Unlock()
}

// deadEvidence walks the wedge-recovery ladder (spec 03 §11): re-assert
// bring-up every 10 dead frames; at 50, mark Wedged — which stops the health
// token so the agent relaunches us on the still-live fd. On the drain path
// (certain=false) a healthy-but-flat input at a native-fast band is
// indistinguishable from a wedge by fill+ptp alone (the 11-bit counter can
// sit saturated between polls), so Wedged additionally requires a dead
// fabric: CONF_DONE (CS3 0x07 bit7) reading clear. Otherwise we keep
// re-asserting bring-up and surface DeadRuns instead of crash-looping a
// healthy app.
func (e *Engine) deadEvidence(certain bool) {
	e.deadRuns++
	e.mu.Lock()
	e.stats.DeadRuns = e.deadRuns
	e.mu.Unlock()
	if e.deadRuns%10 != 0 {
		return
	}
	e.logf("engine: %d dead frames (fill frozen, flat drain) — re-asserting bring-up", e.deadRuns)
	e.beatN.Add(1)
	e.bringUp()
	e.beatN.Add(1)
	if e.deadRuns%50 != 0 {
		return
	}
	if certain {
		e.logf("engine: %d dead frames at a decimated band — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	if v, err := e.b.Read(bus.PlaneCS3, cs3ConfStatus); err == nil && v&0x80 == 0 {
		e.logf("engine: CONF_DONE lost after %d dead frames — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	e.logf("engine: %d dead frames but CONF_DONE high — flat input or partial wedge; continuing with periodic bring-up", e.deadRuns)
}
