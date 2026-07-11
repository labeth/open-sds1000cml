# Per-detent trigger-level calibration — findings and status

## Problem
The trigger-level DAC maps to an input-referred voltage that depends on the
front-end gain in effect, so the historical single global fit
`code = 31434 − 938·V` is only correct near 1 V/div. At other detents a
manually-set trigger level lands at the wrong voltage.

## What shipped (branch `trig-cal`)
The full per-detent infrastructure, defaulting to the old global fit at every
detent (**zero behaviour change** until accurate values exist):
- `analog.FrontEnd`: a per-(channel, detent) `TrigCal{Zero, CPV}` table
  (`code = Zero − CPV·V`), with `TrigVolts` / `TrigCode` / `TrigCalActive` /
  `SetTrigCalDetent` / `TrigCalTable` (`trigcal.go`).
- Every code↔volts conversion routes through it: the web `trig_volts` readout
  and the three browser drag/click/slider senders (via `trig_zero`/`trig_cpv`
  pushed in `/api/status`), the LCD HUD, SCPI `TRLV`, panel autoset, and the
  engine's `trigDispLevel` (per-channel `{zero,cpv}` pushed via `SetChannelVdiv`
  from the front-end `OnVdiv` hook).

## What hardware characterization found (reference unit, FPGA square)
Method: sweep the trigger DAC against a known signal and read the firing-band
edges; the rail-proof variant tracks the firing-band **centre** while stepping
the calibrated offset DAC (band-centre-vs-offset), which extracts codes/volt
without needing the band edges in range.

- **The slope is genuinely per-detent.** Within the ×25 tier (0.5–10 V/div),
  DAC **codes-per-division is ~constant (~1056)**, i.e. `CPV = 1056/AnalogVdiv`.
  Measured cpv: ~1067 @1 V/div, ~533 @2, ~207 @5 (codes/div 1067/1067/1034).
  So the current 938 codes/V is ~2–3× wrong by 5 V/div (bench level error
  > 1.8 V) and off at every detent but ~1 V.
- **The 0 V code is roughly stable (~32100)** across the ×25 tier, but ~700
  codes from the global fit's 31434.

## Why accurate values were NOT shipped (two blockers)
1. **Per-channel, not just per-detent.** The same trigger code fired at 1.74 V
   on C1 but 0.20 V on C2 at 2 V/div — the two channels have different analog
   gain / DC baseline (and C2 carried a ×10 probe). A single per-detent table
   is insufficient; the cal must be per channel.
2. **The ×1 sensitive tier (≤200 mV/div) is unmeasurable with available
   signals.** It sits after the coarse ×25 attenuator, so its codes/division is
   higher; its firing band exceeds the whole DAC code range even for a ~0.9 V
   input, so neither the band edges nor its centre can be found. It needs a
   controlled ~0.1–0.2 V cal signal.

Additional confound: the anchor is the vertical cal's `Vmean`, so vertical-cal
imperfections (and signal clipping at large-signal/low-V-div combos) propagate
into the trigger cal, and wide firing bands make the band centre imprecise.

## What a correct cal needs (next step)
A per-channel on-device characterization routine (autoset-style, using
`SetTrigCalDetent`) driven by a **controlled, amplitude-appropriate cal signal**
per detent — or, better, using the **calibrated offset DAC** (100 codes/input-
volt, fixed) as the voltage reference: at a fixed trigger code, step the offset
to find where the signal edge crosses the threshold, giving a rail-free
`code↔volts` point per (channel, detent). Store the result per unit alongside
the factory cal (a separate U-disk file), load it at boot, install via
`SetTrigCalDetent`. The infrastructure above is ready to carry it.
