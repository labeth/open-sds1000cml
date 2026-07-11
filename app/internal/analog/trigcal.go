package analog

// Per-detent trigger-level calibration.
//
// The trigger comparator's threshold DAC maps to an input-referred voltage that
// depends on the front-end gain in effect — so a single global fit
// (code = 31434 − 938·V) is only correct near the detent it was pinned to
// (1–2 V/div). Bench characterization (sweep the DAC against a known signal,
// record the firing-band edges vs the calibrated Vmax/Vmin) shows the slope
// (DAC codes per input-volt) varies strongly across the V/div ladder while the
// 0 V code is roughly constant. This stores a per-(channel, detent)
// {Zero, CPV} so every code↔volts conversion is correct at every detent.

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

// DefaultTrigCal is the pre-calibration global fit (spec 05 §1.2): exact only
// near 1–2 V/div. Every detent uses it until a per-detent cal is installed, so
// an uncalibrated build behaves exactly as before.
var DefaultTrigCal = TrigCal{Zero: 31434, CPV: 938}

// calFor returns the source channel's active cal (its current detent), falling
// back to the global fit for any detent with no stored cal. Caller holds f.mu.
func (f *FrontEnd) calFor(srcCh int) TrigCal {
	c := f.trigCal[srcCh&1][f.idx[srcCh&1]]
	if c.CPV == 0 { // unset → the pre-cal global fit
		return DefaultTrigCal
	}
	return c
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
