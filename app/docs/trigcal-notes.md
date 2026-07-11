# Trigger-level calibration — findings and status

## Result (branch `trig-cal`)
The trigger-level cal is a **single global constant**: `code = 31437 − 911·V`
(BNC volts, probe not folded), correct at **every** V/div detent. There is no
per-detent or per-tier variation to model. This replaced the older global fit
`31434 − 938·V` and cut the worst-case WYSIWYG level error from **0.041 V to
0.012 V** across the ladder.

## Why it is global (the physics)
The trigger comparator threshold is referred to the **post-gain (post-PGA)**
trace — the same signal the display draws — so a fixed DAC code corresponds to a
fixed number of **display volts** regardless of the V/div detent. Changing V/div
changes the analog gain (and thus the raw ADC codes), but the trigger threshold
scales with it, keeping codes-per-display-volt constant.

## How it was measured (FPGA DAC, clean method)
An Alchitry Au drives a TLC7524CN 8-bit DAC into C1 (see the FPGA signal-source
project). Key enablers over the earlier failed attempt:
- **Amplitude + offset shaping per detent** (`dacgen.v` `AMP`/`OFF` params): a
  triangle sized to sit ~3 divisions on-screen and non-railed at each detent —
  this is what finally made the ×1 sensitive tier (≤0.2 V/div) measurable
  (full-scale rails it; a small shaped signal does not).
- **Triangle, not sine**: a linear ramp crosses every code, giving sharp firing
  bands (no rounded-peak smear).
- **NORM `seq`-advance fire detector**: in NORM the published frame `seq` only
  advances on a real, software-confirmed trigger — an unambiguous binary, free of
  the `acq_log` staleness that corrupted the old scans.
- **Gain-settle gate**: the analog gain updates ~1.2 s AFTER a V/div change; the
  scan waits for the raw code pp to stabilise before measuring (a stale-gain
  frame gave the earlier bogus readings).
- **Band-CENTRE regression, not edges**: `centre_code = Zero − CPV·Vmean`
  regressed across detents. The centre is immune to the edge-rounding bias that
  makes narrow-band (fine-detent) *edge* fits underestimate CPV.

Measured (C1), firing-band centre vs calibrated `Vmean`:

| V/div | display Vpp | band centre | CPV (from fit) |
|------:|------------:|------------:|---------------:|
| 1.0   | 2.48 V      | 32484       |                |
| 0.5   | 2.36 V      | 32451       |                |
| 0.1   | 0.29 V      | 31592       |                |
| 0.05  | 0.15 V      | 31535       |                |

Least-squares over 0.05–1.0 V/div (a 20× span, spanning both the ×1 and ×25
attenuator tiers): **CPV = 911.3 codes/display-volt, Zero = 31437**, residual
WYSIWYG error ≤ 0.012 V.

## Correcting the earlier (wrong) conclusion
The previous notes claimed a per-detent slope `CPV ≈ 1056/AnalogVdiv` and a
per-channel split. Both were **measurement artifacts**:
- The "1/AnalogVdiv" slope came from rounded **sine** peaks (mushy band edges)
  plus **offset-DAC contamination** in the old band-centre-vs-offset method (the
  offset cal's own slope leaked into the trigger slope). The clean triangle +
  offset-0 method shows the slope is flat.
- The ×1 tier is not unmeasurable — it just needs a small shaped signal.

## Caveats / open items
- Measured on **C1 only** (that is the channel wired to the DAC). C2 uses the
  same global constant; if a unit is later found to have a per-channel skew,
  `SetTrigCalDetent` can install a C2 override. No evidence of skew today.
- Vertical-cal imperfection is the noise floor: the same DAC input reads −2.28 V
  at 0.5 V/div vs −2.40 V at 1.0 V/div (~2–5% inter-detent vertical-cal slop), so
  the trigger cal is only as tight as the display it matches. 0.012 V residual is
  at that floor.
- **Small-signal lock** (separate issue, same reliability goal): a trace with
  on-screen pp < ~40 codes (≈1.3 div) fails `centerCross` (`edge_x = −1`) and
  will not lock in NORM even though the HW comparator fires. Tracked separately.
