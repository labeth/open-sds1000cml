package engine

import (
	"math"
	"time"
)

// Equivalent-time sampling (spec 04 §3): OPT-IN ONLY, never auto-routed.
// Repetitive-signal density refinement for tdiv ≤ 50 ns: many real
// sub-acquisitions are interleaved by their software-measured trigger phase
// (NEVER the jittery HW position latch). Every output value is a real sample
// average or an interpolation between two real averages — no fabrication.

const (
	etsDrainCols      = 2048
	etsMaxAcqPerFrame = 40
	etsFrameBudgetMs  = 650
	etsEdgeMinPtp     = 40
	etsMaxAccFrames   = 8
	etsTs             = 2.0 // ns per real sample (class 0x20)
)

// etsPlan picks the phase-bin factor for a tdiv (nearest row; default the
// 2 ns row) and derives nCols = 0xA000/factor + 10.
func etsPlan(tdivS float64) (factor, nCols int) {
	rows := []struct {
		tdiv   float64
		factor int
	}{
		{1e-9, 500}, {2e-9, 500}, {5e-9, 500},
		{10e-9, 250}, {20e-9, 100}, {50e-9, 50},
	}
	best, bestD := rows[1], math.MaxFloat64
	for _, r := range rows {
		if d := math.Abs(r.tdiv - tdivS); d < bestD {
			best, bestD = r, d
		}
	}
	return best.factor, 0xA000/best.factor + 10
}

// ETSEligible reports whether ETS may run at this tdiv.
func (b Band) ETSEligible() bool { return b.TdivS <= 50e-9*(1+1e-6) }

func (e *Engine) etsReset() {
	e.etsSum1, e.etsSum2 = nil, nil
	e.etsCnt = nil
	e.etsCov = nil
	e.etsAccFrames = 0
}

// etsFrame runs one equivalent-time frame: up to 40 sub-acquisitions or
// 650 ms, each a real capture on the ordinary halt engine, phase-binned by
// the software crossing and interleaved into the persistent accumulator.
func (e *Engine) etsFrame(norm bool) {
	factor, nCols := etsPlan(e.band.TdivS)
	if len(e.etsCnt) != nCols || len(e.etsCov) != factor {
		e.etsSum1 = make([]float64, nCols)
		e.etsSum2 = make([]float64, nCols)
		e.etsCnt = make([]int32, nCols)
		e.etsCov = make([]bool, factor)
		e.etsAccFrames = 0
	}
	if e.etsScratch1 == nil {
		e.etsScratch1 = make([]uint8, etsDrainCols)
		e.etsScratch2 = make([]uint8, etsDrainCols)
	}

	Wns := screenDivsH * e.band.TdivS * 1e9
	if Wns <= 0 {
		Wns = float64(nCols) * etsTs
	}
	etColTime := Wns / float64(nCols)
	rising := e.trigRising.Load()
	deadline := e.clk.Now().Add(etsFrameBudgetMs * time.Millisecond)

	covered := func() int {
		n := 0
		for _, c := range e.etsCov {
			if c {
				n++
			}
		}
		return n
	}

	var lastPtp int
	for a := 0; a < etsMaxAcqPerFrame && e.clk.Now().Before(deadline); a++ {
		if e.interrupted() {
			return
		}
		e.armEngine()
		e.waitCapture(false) // ETS is AUTO-only (spec 04 §3.1)
		if e.stopReq.Load() {
			return
		}
		haltOK := e.halt()
		for i := 0; i < etsDrainCols; i++ {
			w := e.b.DrainRead(uint16(drainBase + i%5))
			e.etsScratch1[i] = uint8(w >> 8)
			e.etsScratch2[i] = uint8(w)
		}
		e.armEngine()
		if !haltOK {
			continue
		}

		disc := e.etsScratch1
		if int(e.trigSrc.Load()) == 1 {
			disc = e.etsScratch2
		}
		_, _, p := ptp(disc)
		lastPtp = p
		if p < etsEdgeMinPtp {
			continue // flat/slow source: no phase information in this capture
		}
		xref := centerCross(disc, midLevel(disc), rising)
		if xref < 0 {
			continue
		}
		bin := int((xref - math.Floor(xref)) * float64(factor))
		if bin >= factor {
			bin = factor - 1
		}
		e.etsCov[bin] = true

		// Interleave BOTH channels' real samples relative to xref.
		k0 := int(xref) - nCols
		k1 := int(xref) + nCols
		if k0 < 0 {
			k0 = 0
		}
		if k1 > etsDrainCols-1 {
			k1 = etsDrainCols - 1
		}
		for k := k0; k <= k1; k++ {
			col := int(math.Round(((float64(k)-xref)*etsTs + Wns/2) / etColTime))
			if col < 0 || col >= nCols {
				continue
			}
			e.etsSum1[col] += float64(e.etsScratch1[k])
			e.etsSum2[col] += float64(e.etsScratch2[k])
			e.etsCnt[col]++
		}
		if covered() == factor {
			break // full phase coverage — stop the frame early
		}
	}

	e.etsAccFrames++
	cov := covered()
	f := e.arena.Write()

	if cov < factor/4 {
		// Faithful flat fallback (spec 04 §3.7): a REAL single-capture
		// window — the record centre — never a patchwork of stale bins.
		off := etsDrainCols/2 - nCols/2
		copy(f.C1[:nCols], e.etsScratch1[off:off+nCols])
		copy(f.C2[:nCols], e.etsScratch2[off:off+nCols])
		f.EdgeX = -1
	} else {
		e.etsRebuild(f, nCols)
		f.EdgeX = float64(nCols) / 2 // the interleave centres the edge at W/2
	}

	// The honesty check runs on the TRIGGER-SOURCE channel, mirroring the
	// sub-acquisition gate — a C2-sourced reconstruction with a flat C1 must
	// not be discarded.
	recon := func() []uint8 {
		if int(e.trigSrc.Load()) == 1 {
			return f.C2[:nCols]
		}
		return f.C1[:nCols]
	}
	_, _, p := ptp(recon())
	if f.EdgeX >= 0 && p < etsEdgeMinPtp {
		// Reconstruction went flat (source changed): fall back honestly.
		off := etsDrainCols/2 - nCols/2
		copy(f.C1[:nCols], e.etsScratch1[off:off+nCols])
		copy(f.C2[:nCols], e.etsScratch2[off:off+nCols])
		f.EdgeX = -1
		_, _, p = ptp(recon())
	}

	f.Valid = nCols
	f.WinCols = nCols
	f.Interp = true
	f.IsEnv = false // mandatory clears: ETS is a real-time frame
	f.EnvCols = 0
	f.Ptp = p
	f.Trigd = false
	f.TrigPos = 0
	f.Coherent = cov > 0
	f.HaltOK = true
	f.RollCodes = false
	f.TdivS = e.band.TdivS
	f.DisplayedS = e.band.TdivS
	f.SampleS = etColTime * 1e-9
	f.Norm = norm

	e.commitStats(f.Coherent, true, lastPtp, 0, 0, 0)
	e.commitPublish(f)

	// Liveness: re-track a changing source by rebuilding the accumulator —
	// once ≥9/10 of the phase bins are covered (densified) OR after the
	// 8-frame liveness ceiling (spec 04 §3.6).
	if e.etsAccFrames >= etsMaxAccFrames || cov*10 >= factor*9 {
		for i := range e.etsCnt {
			e.etsSum1[i], e.etsSum2[i], e.etsCnt[i] = 0, 0, 0
		}
		for i := range e.etsCov {
			e.etsCov[i] = false
		}
		e.etsAccFrames = 0
	}
}

// etsRebuild renders the accumulator: filled columns are means of real
// samples; interior gaps are linearly interpolated between filled columns;
// the ends extend the nearest filled column.
func (e *Engine) etsRebuild(f *Frame, nCols int) {
	render := func(sum []float64, out []uint8) {
		lastFilled := -1
		firstFilled := -1
		for c := 0; c < nCols; c++ {
			if e.etsCnt[c] == 0 {
				continue
			}
			v := uint8(math.Round(sum[c] / float64(e.etsCnt[c])))
			out[c] = v
			if firstFilled < 0 {
				firstFilled = c
			}
			// Interpolate the gap since the previous filled column.
			if lastFilled >= 0 && c-lastFilled > 1 {
				a, b := float64(out[lastFilled]), float64(v)
				for g := lastFilled + 1; g < c; g++ {
					frac := float64(g-lastFilled) / float64(c-lastFilled)
					out[g] = uint8(math.Round(a + (b-a)*frac))
				}
			}
			lastFilled = c
		}
		if firstFilled < 0 {
			return // nothing filled; caller's coverage gate prevents this
		}
		for c := 0; c < firstFilled; c++ {
			out[c] = out[firstFilled]
		}
		for c := lastFilled + 1; c < nCols; c++ {
			out[c] = out[lastFilled]
		}
	}
	render(e.etsSum1, f.C1)
	render(e.etsSum2, f.C2)
}
