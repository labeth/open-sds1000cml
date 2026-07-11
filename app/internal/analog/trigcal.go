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

// globalFit is the historical single-detent fit (spec 05 §1.2): code = 31434 −
// 938·V. Exact only near 1–2 V/div. Kept as the ultimate fallback.
var globalFit = TrigCal{Zero: 31434, CPV: 938}

// defaultTrigCal is the NOMINAL per-detent trigger cal. It is EMPTY (every
// detent falls back to globalFit) until a reliable characterization exists, so
// the shipped build behaves exactly as before. The infrastructure (SetTrigCal-
// Detent, the per-detent routing) is ready to carry accurate values.
//
// Hardware characterization on the reference unit (see docs/trigcal-notes.md)
// established the SHAPE of the correct cal but not per-unit-shippable values:
//   - ×25 tier (0.5–10 V/div): codes-per-division is ~constant (~1056), i.e.
//     code = zero − (1056/AnalogVdiv)·V, confirming the slope varies as 1/V-div
//     within a tier. The current global fit's constant 938 codes/V is therefore
//     ~2–3× wrong at 5 V/div (bench: >1.8 V level error) and off at every
//     detent except ~1 V/div.
//   - Two blockers to shipping values: (a) the mapping is ALSO per-CHANNEL (the
//     same code fired at 1.74 V on C1 but 0.20 V on C2 — different analog
//     gain/DC per channel), so a single per-detent table is insufficient; and
//     (b) the ×1 sensitive tier (≤200 mV/div) could not be measured (its firing
//     band exceeds the DAC code range even for a ~0.9 V input). A correct cal
//     needs a per-channel routine driven by a controlled, amplitude-appropriate
//     cal signal (or the calibrated offset DAC as the reference), stored per
//     unit — not a hardcoded nominal table.
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
