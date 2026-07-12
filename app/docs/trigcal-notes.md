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
- Envelope-band edge margin (see below): the trace centre is stable; the outer
  columns can shimmer on the fastest envelope band (5 ms/div, few periods).

# Triggering reliability (discern.go / engine_loop.go / envroll.go)

Three fixes made triggering trustworthy across the band ladder, all validated on
the reference unit with the FPGA cal signal.

## Small-signal lock (decimated bands)
A real signal under ~1.6 divisions (a 2.4 Vpp cal signal is ptp 8..32 at 2..10
V/div) never locked in NORM: `centerCross` found the edge but the lock gate
required raw ptp ≥ `nativeEdgeMinPtp` (40), so it held forever. Raw ptp cannot be
lowered safely — a noisy flat rail's ptp (up to ~18 at σ≈2.5) can EXCEED a small
real signal's, so amplitude cannot separate them. `signalPresent()` gates on
`ptp ≥ SigK·noiseFloor` instead, where `noiseFloor` is the median absolute SECOND
difference (cancels the linear ramp regardless of slope/period → a PERIOD-
INDEPENDENT noise estimate that also survives aliased many-period screens). Kept
as an OR with the original ptp≥40 fast-path so nothing that locked before
regresses. Decimated only; native-fast keeps raw ptp (record spans <1 period).
Bench: real signals ratio ≥16, flat rails ≤7 (σ to 3.5). Tunable `SigK` (def 8).

## Slope flip (dither-on-ramp)
At ~1-period-on-screen bands (100 µs/div) the anchor still flipped rising⇄falling
on ~10% of frames: on a slow ramp the disc holds many periods that dither through
the level in noise, and over the old fixed-count w=8 window a momentary down-blip
on a RISING ramp passed as a "falling" crossing. Replaced with noise-scaled
HYSTERESIS: a crossing confirms only if, going outward, the trace reaches the far
state (±`hystK`·noiseFloor) without bouncing back. A falling crossing must reach
lvl+h going backward — a rising trace goes low backward, so a rising-ramp dither
can no longer confirm as falling. Scale-free; also rejects the single-sample blip
the old test targeted. HW: 0 flips across 50µs–2ms/div × both slopes.

## Triggered envelope (5–50 ms/div)
Envelope bands were untriggered by design (a repetitive signal showed a solid
min/max band — random-phase acquisitions). Now `envFrame` trigger-anchors the
drained record (centerCross + signalPresent) and publishes it as a normal edge-
centred trace, falling back to the min/max scatter only when there is no trigger
(aliased / flat / level-off-signal). The record spans exactly one screen with no
centring margin, so the trace CENTRE is rock-stable but the outer columns repeat-
extend as the anchor wanders — negligible on 20/50 ms/div (many periods), up to
~20% width on 5 ms/div (few periods). A future refinement could add capture
headroom (larger `EnvFillTarget` + a windowed sub-region) for perfect edges, at
~1.5× fill time. Roll bands (≥100 ms/div) remain untriggered live scroll.
