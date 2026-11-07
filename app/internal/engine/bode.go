// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE

package engine

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	core "github.com/labeth/open-dso-core/engine"
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
	e.bode.Mu.Lock()
	e.bode.RefCh = refCh & 1
	e.bode.DutCh = dutCh & 1
	if e.bode.Bins == nil {
		e.bode.Bins = make(map[int]BodePoint)
	}
	e.bode.Mu.Unlock()
	if on {
		e.bodeMode.Store(BodeOn)
	} else {
		e.bodeMode.Store(BodeOff)
	}
}

// ClearBode empties the accumulated curve.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) ClearBode() {
	e.bode.Mu.Lock()
	e.bode.Bins = make(map[int]BodePoint)
	e.bode.LiveValid = false
	e.bode.Mu.Unlock()
}

// BodePoints returns the accumulated curve sorted by frequency.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) BodePoints() []BodePoint {
	e.bode.Mu.Lock()
	defer e.bode.Mu.Unlock()
	out := make([]BodePoint, 0, len(e.bode.Bins))
	for _, p := range e.bode.Bins {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b BodePoint) int { return cmp.Compare(a.FreqHz, b.FreqHz) })
	return out
}

// bodeEval computes the transfer-function point for a locked frame and, if
// valid, updates its log-frequency bin. Runs on the engine goroutine.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) bodeEval(f *sample.Frame, valid int, sampleS float64) {
	if sampleS <= 0 || valid < 8 {
		e.bodeInvalidate("no samples")
		return
	}
	e.bode.Mu.Lock()
	refCh, dutCh, scale := e.bode.RefCh, e.bode.DutCh, e.bode.Scale
	e.bode.Mu.Unlock()

	ref := f.Codes8(0)
	dut := f.Codes8(0)
	if refCh == 1 {
		ref = f.Codes8(1)
	}
	if dutCh == 1 {
		dut = f.Codes8(1)
	}
	if len(ref) < valid || len(dut) < valid {
		return
	}
	ref, dut = ref[:valid], dut[:valid]

	noTone := fmt.Sprintf("no tone on the reference C%d", refCh+1)
	f0 := core.FundamentalHz(ref, sampleS)
	if f0 <= 0 {
		e.bodeInvalidate(noTone)
		return
	}
	cyc := f0 * float64(valid) * sampleS
	if cyc < core.BodeMinCycles {
		e.bodeInvalidate(fmt.Sprintf("%.1f cycles on screen, %g needed: S/DIV slower", cyc, core.BodeMinCycles))
		return
	}
	if f0 >= 0.5/sampleS {
		e.bodeInvalidate("the tone is too fast for this sample rate: S/DIV faster")
		return
	}
	re1, im1 := core.SingleBinDFT(ref, f0, sampleS)
	re2, im2 := core.SingleBinDFT(dut, f0, sampleS)
	mag1 := math.Hypot(re1, im1)
	mag2 := math.Hypot(re2, im2)
	// noise floor: the reference must carry real signal (its DFT bin well above
	// the per-sample quantisation noise scaled by record length).
	floor := 0.5 * math.Sqrt(float64(valid))
	if mag1 < floor {
		e.bodeInvalidate(noTone)
		return
	}
	refScale := math.Float64frombits(e.chVdivBits[refCh].Load())
	dutScale := math.Float64frombits(e.chVdivBits[dutCh].Load())
	if scale != nil {
		refScale, dutScale = scale(refCh), scale(dutCh)
	}
	if !(refScale > 0) {
		refScale = 1
	}
	if !(dutScale > 0) {
		dutScale = 1
	}
	gainDB := 20 * math.Log10((mag2*dutScale)/(mag1*refScale))
	if math.IsInf(gainDB, 0) || math.IsNaN(gainDB) {
		e.bodeInvalidate(fmt.Sprintf("no output on the DUT C%d", dutCh+1))
		return
	}
	// phase of X2·conj(X1) = arg(X2) − arg(X1), wrapped to (−180, 180]
	pr := re2*re1 + im2*im1
	pi := im2*re1 - re2*im1
	phaseDeg := math.Atan2(pi, pr) * 180 / math.Pi

	p := BodePoint{FreqHz: f0, GainDB: gainDB, PhaseDeg: phaseDeg}
	e.bode.Mu.Lock()
	p.Seq = e.bode.Cap + 1
	e.bode.Cap = p.Seq
	if e.bode.Bins == nil {
		e.bode.Bins = make(map[int]BodePoint)
	}
	bin := int(math.Round(math.Log10(f0) * core.BodeBinsPerDecade))
	if len(e.bode.Bins) < core.BodeMaxPoints || func() bool { _, ok := e.bode.Bins[bin]; return ok }() {
		e.bode.Bins[bin] = p
	}
	e.bode.Live, e.bode.LiveValid, e.bode.Why = p, true, ""
	e.bode.Mu.Unlock()
}

// bodeInvalidate marks the live point stale, saying why the capture gave none.
// TRLC-LINKS: REQ-SDS-066
func (e *Engine) bodeInvalidate(why string) {
	e.bode.Mu.Lock()
	e.bode.LiveValid, e.bode.Why = false, why
	e.bode.Mu.Unlock()
}
