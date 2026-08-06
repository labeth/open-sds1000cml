package engine

import (
	"time"

	"open-sds/app/internal/combine"
	"open-sds/app/internal/iface"
)

// CombineReq is the geometry to arm one in-fabric super-res COMBINE drain.
type CombineReq struct {
	GridL, K int // fine grid = GridL*K bins; drain = combine.DrainWords(GridL,K) words
	DwellMs  int // accumulate window (clamped 5..30 ms)
}

// CombineOut is one drained grid: raw bin-major LSW-first words + host metadata.
type CombineOut struct {
	Words   []uint16 // len == combine.DrainWords(GridL,K); nil when ok=false
	Frames  int      // monotonic drain ordinal (device combine passes drained)
	SampleS float64  // coarse s/sample of the current band (fine dt = SampleS/K)
}

type combineReqMsg struct {
	req   CombineReq
	reply chan combineOutMsg
}
type combineOutMsg struct {
	out CombineOut
	ok  bool
}

// CombineDrain requests one owner-goroutine COMBINE arm+dwell+halt+drain (model of
// ReadMatrix): non-blocking enqueue (ok=false when the queue is full), 250 ms reply
// timeout sized for the ~50 ms loop-sleep + ≤30 ms dwell + poll + drain budget. ok=false
// on timeout / overflow / short-drain — the caller SKIPS the tick (never crunches a
// partial grid).
func (e *Engine) CombineDrain(r CombineReq) (CombineOut, bool) {
	reply := make(chan combineOutMsg, 1)
	select {
	case e.combineReq <- combineReqMsg{req: r, reply: reply}:
	default:
		return CombineOut{}, false
	}
	select {
	case m := <-reply:
		return m.out, m.ok
	case <-time.After(250 * time.Millisecond):
		return CombineOut{}, false
	}
}

// serviceCombine services AT MOST ONE pending CombineDrain at the loop boundary (Run
// calls it right after serviceCommands, before oneFrame — no capture is committed here).
// Empty channel => immediate no-op, so a build with combine off is byte-for-byte today.
func (e *Engine) serviceCombine() {
	var msg combineReqMsg
	select {
	case msg = <-e.combineReq:
	default:
		return
	}
	out, ok := e.doCombineDrain(msg.req)
	msg.reply <- combineOutMsg{out: out, ok: ok}
}

func (e *Engine) doCombineDrain(r CombineReq) (CombineOut, bool) {
	n := combine.DrainWords(r.GridL, r.K) // 64*4*12 = 3072
	ms := r.DwellMs
	if ms < 5 {
		ms = 5
	} else if ms > 30 {
		ms = 30
	}

	// Baseline: XFORM_CTRL is never written elsewhere → reset default 0x0000; RUN's
	// baseline is the composed run word (mode + run_en/bit2).
	baseRun := e.runWord()

	// Hold quiet across the whole drive (like drain): render/web/panel pause so nothing
	// contends the single core / GPMC during arm+dwell+drain.
	e.quiet.Lock()
	defer e.quiet.Unlock()

	// ARM (sr_drain_close §4): interleave_en|il-trig_en, run_en|combine_en, OP_GO opens
	// the accumulate window (clear-sweep on OP_GO).
	e.w(selXformCtrl, combine.ArmXform(0)) // (1<<2)|(1<<9)
	e.w(selRun, combine.ArmRun(baseRun))   // baseRun|(1<<5)
	e.w(selOpcode, opGo)

	// DWELL: capture waits in FILL (no ticks) while sr_accum integrates; beat so the
	// supervisor sees liveness across the dwell.
	e.sleepBeating(time.Duration(ms) * time.Millisecond)
	if e.stopReq.Load() {
		e.combineRestore(baseRun)
		return CombineOut{}, false
	}

	// HALT freezes sr_accum → stages 3072 bin words → capture post_full finalizes →
	// coherent=1, STATUS_A.done=1, BURST_REMAIN={READY=1, REMAIN=n}.
	e.w(selOpcode, opHalt)

	ok, remain := false, 0
	deadline := e.clk.Now().Add(30 * time.Millisecond)
	for e.clk.Now().Before(deadline) {
		s := e.r(selStatus)
		br := e.r(selBurstRem)
		if iface.StatusADone(s) && iface.BurstRemainReady(br) {
			remain = int(iface.BurstRemainRemain(br))
			ok = true
			break
		}
		e.beatN.Add(1)
		e.clk.Sleep(e.pollEvery)
	}
	if !ok || remain < n {
		e.combineRestore(baseRun)
		return CombineOut{}, false // not done / short → skip (no partial crunch)
	}

	// FRESH cache-cold buffer per drain (EDMA is not cache-coherent), NO prime read:
	// the first BURST pop is bin0.align.cnt.
	buf := make([]uint16, n)
	e.b.BurstWordsInto(buf, n)
	e.combineDrains++
	out := CombineOut{
		Words:   buf,
		Frames:  e.combineDrains,
		SampleS: e.band.CaptureIntervalNs() * 1e-9,
	}
	e.combineRestore(baseRun)
	return out, true
}

// combineRestore returns the fabric to baseline (sr_drain_close CANCEL): clear
// combine_en, XFORM back to its 0x0000 reset default (BURST = raw record), idle. With
// RUN[5]=0 the datapath is byte-for-byte today.
func (e *Engine) combineRestore(baseRun uint16) {
	e.w(selRun, baseRun)    // combine_en (bit5) cleared
	e.w(selXformCtrl, 0)    // BURST back to raw-record mode
	e.w(selOpcode, opReset) // idle
}
