// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"math"

	"github.com/labeth/open-dso-core/sample"
)

// Frequency Response Analysis (Bode plot) — docs/bode-plan.md. With FRA armed,
// every locked frame yields ONE transfer-function point between a REFERENCE
// channel (DUT input) and a DUT-output channel: the fundamental frequency, the
// magnitude ratio in dB, and the phase difference in degrees. Points accumulate
// into log-frequency bins as an external/FPGA source sweeps, building the Bode
// curve. The engine is the single source of truth; the web and the LCD render
// the same accumulated points.

// SetBodeScaleSource supplies calibrated, probe-referred channel scales.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) SetBodeScaleSource(fn func(int) float64) {
	e.bode.Mu.Lock()
	e.bode.Scale = fn
	e.bode.Mu.Unlock()
}

// SetBodeMode arms/disarms FRA and sets the reference + DUT channels. Arming
// with a fresh channel pair does NOT clear the accumulated curve (the operator
// may re-arm mid-sweep); use ClearBode to reset.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) SetBodeMode(on bool, refCh, dutCh int) {
	e.bode.SetChannels(refCh&1, dutCh&1)
	if on {
		e.bodeMode.Store(BodeOn)
	} else {
		e.bodeMode.Store(BodeOff)
	}
}

// ClearBode empties the accumulated curve.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) ClearBode() { e.bode.Clear() }

// BodePoints returns the accumulated curve sorted by frequency.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) BodePoints() []BodePoint { return e.bode.Points() }

// bodeEval computes the transfer-function point for a locked frame and, if
// valid, updates its log-frequency bin (core.BodeState.Eval). Runs on the
// engine goroutine.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) bodeEval(f *sample.Frame, valid int, sampleS float64) {
	e.bode.Eval(f, valid, sampleS, func(ch int) float64 { return math.Float64frombits(e.chVdivBits[ch&1].Load()) })
}
