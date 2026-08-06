package engine

import (
	"time"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/iface"
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

// runWord composes the RUN register word: MODE (AUTO/NORM) plus the RUN bit
// (spec 03 §5.1). It replaces the vendor run-word magic (0x0001/0x0003).
//
// Envelope/roll bands ALWAYS arm in AUTO, regardless of the user's NORM selection:
// they publish every frame and are never fabric-trigger-centred, and an aliased /
// flat / off-level signal (the exact case the min/max band exists for) can never
// satisfy a NORM trigger — under NORM the record would never cohere and both the
// raw BURST and the envelope channel would read coherent-gated zeros (a blank
// band). Software still decides triggered-vs-min/max per frame from the drained
// record, so forcing AUTO here costs nothing.
func (e *Engine) runWord() uint16 {
	mode := modeAuto
	if k := e.band.Kind(); e.normNow() && k != KindEnvelope && k != KindRoll {
		mode = modeNorm
	}
	w := iface.RunWithMode(mode) | iface.RunWithRun(true)
	// Fabric FAST-SIGNAL GENERATOR (proving-only, default off): OR the previously-FREE
	// RUN[6]/RUN[7] so BOTH the per-frame normal arm AND the combine baseRun (which is
	// this runWord()) carry the enable. Off => this is byte-for-byte the RUN word above.
	if e.siggenEn.Load() {
		w |= 1 << combine.RunSiggenEnBit
		if e.siggenRamp.Load() {
			w |= 1 << combine.RunSiggenShapeBit
		}
	}
	return w
}

func (e *Engine) w(sel, val uint16) {
	if err := e.b.Write(iface.CS1, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) w3(sel, val uint16) {
	if err := e.b.Write(iface.CS3, sel, val); err != nil {
		e.busErr(err)
	}
}

func (e *Engine) r(sel uint16) uint16 {
	v, err := e.b.Read(iface.CS1, sel)
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
	if v, err := e.b.Read(iface.CS3, cs3ConfStatus); err == nil && !iface.ConfDoneDone(v) {
		e.logf("engine: CONF_DONE lost after %d dead frames — marking wedged (agent will relaunch)", e.deadRuns)
		e.mu.Lock()
		e.stats.Wedged = true
		e.mu.Unlock()
		return
	}
	e.logf("engine: %d dead frames but CONF_DONE high — flat input or partial wedge; continuing with periodic bring-up", e.deadRuns)
}
