package analog

// Trigger-level calibration.
//
// The trigger comparator threshold is referred to the DISPLAYED signal: the
// threshold DAC is compared to the post-gain (post-PGA) trace, so a fixed DAC
// code corresponds to a fixed number of DISPLAY volts regardless of the V/div
// detent. Clean bench characterization (FPGA DAC driving a triangle whose
// amplitude is shaped per detent so it sits on-screen and non-railed; the
// firing-band CENTRE regressed against the calibrated Vmean, which is immune to
// edge-rounding bias) proved this directly: across 0.05–1.0 V/div (a 20× span,
// both the ×1 and ×25 attenuator tiers) the codes-per-DISPLAY-volt slope is
// CONSTANT (911 ± ~10) and the 0 V code is constant (31437 ± ~15). See
// docs/trigcal-notes.md. So the cal is a single GLOBAL {Zero, CPV}, NOT a
// per-detent table — the earlier "codes/div ~1056, slope ∝ 1/AnalogVdiv" theory
// was a measurement artifact (rounded sine peaks + offset-DAC contamination in
// the old offset-slope method). The per-(channel, detent) storage below is kept
// as future-proof override infrastructure but is unused: every detent resolves
// to globalFit.

// numDetents is the V/div ladder length (len(Detents)); a fixed array keeps the
// cal cheap to copy and index.
const numDetents = 12

// TrigCal is one detent's trigger-DAC calibration: code = Zero − CPV·V, where V
// is input-referred volts at the BNC (before the probe multiplier). Zero is the
// DAC code for 0 V; CPV is DAC codes per input-volt.
type TrigCal struct {
	Zero float64 `json:"zero"`
	CPV  float64 `json:"cpv"`
}

// globalFit is the measured global trigger cal: code = 31437 − 911·V (BNC volts,
// probe not folded). Established by clean FPGA-DAC characterization on the
// reference unit (docs/trigcal-notes.md): a per-detent regression of firing-band
// centre vs Vmean across 0.05–1.0 V/div gives CPV = 911.3 codes/display-volt and
// Zero = 31437, constant across the whole ladder. This cuts the worst-case
// WYSIWYG level error from 0.041 V (old 938/31434 fit) to 0.012 V. Because the
// slope is genuinely constant, this single value is correct at every detent.
var globalFit = TrigCal{Zero: 31437, CPV: 911}

// defaultTrigCal is the per-detent override table. It is EMPTY: the measured cal
// is a single global constant (globalFit), so no per-detent values are needed.
// Kept as infrastructure — SetTrigCalDetent can install per-unit/per-channel
// overrides if a future unit is found to deviate — but every detent resolves to
// globalFit today.
var defaultTrigCal [numDetents]TrigCal

// calFor returns the source channel's active cal for its current detent: a
// per-unit override if installed, else the nominal per-detent default, else the
// global fit. Caller holds f.mu.
func (f *FrontEnd) calFor(srcCh int) TrigCal {
	i := f.idx[srcCh&1]
	if c := f.trigCal[srcCh&1][i]; c.CPV != 0 {
		return c
	}
	if c := defaultTrigCal[i]; c.CPV != 0 {
		return c
	}
	return globalFit
}

// DefaultTrigCal reports the nominal cal for a detent (globalFit if unset).
func DefaultTrigCal(detent int) TrigCal {
	if detent >= 0 && detent < numDetents && defaultTrigCal[detent].CPV != 0 {
		return defaultTrigCal[detent]
	}
	return globalFit
}

// TrigVolts converts a trigger DAC code to probe-tip input volts at the source
// channel's current detent. Matches the historical `TrigLevelVolts(code)·probe`
// when the cal is the default fit.
func (f *FrontEnd) TrigVolts(code uint16, srcCh int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calFor(srcCh)
	return (c.Zero - float64(code)) / c.CPV * f.probe[srcCh&1]
}

// TrigCode converts probe-tip input volts to a trigger DAC code (unclamped —
// the engine clamps to [TrigCodeMin,TrigCodeMax]).
func (f *FrontEnd) TrigCode(volts float64, srcCh int) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calFor(srcCh)
	return c.Zero - c.CPV*volts/f.probe[srcCh&1]
}

// TrigCalActive returns the source channel's active {Zero, CPV} (BNC-referred,
// probe NOT folded) — pushed to the engine's centring map and the browser so
// both convert with the same per-detent slope. Returns the raw cal so callers
// keep applying probe exactly as before.
func (f *FrontEnd) TrigCalActive(srcCh int) (zero, cpv float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calFor(srcCh)
	return c.Zero, c.CPV
}

// SetTrigCalDetent installs one detent's cal for a channel (from the loader /
// characterization routine). A zero CPV clears it back to the default fit.
func (f *FrontEnd) SetTrigCalDetent(ch, detent int, c TrigCal) {
	if detent < 0 || detent >= numDetents {
		return
	}
	f.mu.Lock()
	f.trigCal[ch&1][detent] = c
	f.mu.Unlock()
}

// TrigCalTable returns a copy of the full per-channel, per-detent cal (for
// persistence).
func (f *FrontEnd) TrigCalTable() [2][numDetents]TrigCal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.trigCal
}
