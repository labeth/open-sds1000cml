# Superres artifact lab — the scientific loop

Goal: remove the 8-bit quantization "zig-zag" artifacts from stacked
waveforms. Method: strict hypothesis → implement → ground-truth test →
on-device validate → document, keep on improvement / roll back on regression.
Five iterations.

## Fixed measurement procedure

Device: SDS1102CML+ clone at 192.168.1.209, Arduino 1 kHz square on C1,
5 µs/div native band (100 MS/s), 1 V/div, K=32, 25 s stack (~340 frames).
Metrics from a flat-top segment (edge+40..edge+48 raw samples, 256 fine bins)
of the stacked mean and the stacker's own honest statistics:

| metric | meaning | ideal |
|---|---|---|
| M1 distToCode | mean distance of bin values to integer codes | 0.25 (unbiased); →0 = staircase pull |
| M2 altRate | sign-alternation rate of bin-to-bin diffs | ~67% (iid); ≫ = structured zig-zag |
| M3 roughness | σ of bin-to-bin diffs on the flat top (codes) | ↓ |
| M4 sigmaStack | measured half-stack noise (codes) | ↓ |
| M5 bitsGained | log2(σ_single/σ_stack), measured | ↑ |
| M6 accept | frames stacked / offered | ~1 |

Verdict rule: KEEP if the iteration's declared target metric improves ≥15%
with no other metric regressing >10%; otherwise ROLL BACK (git revert).

## Iteration 0 — baseline (v0.0.1-9, nearest-bin drizzle, no dither)

M1 0.122 · M2 74% · M3 0.198 · M4 0.143 · M5 +1.6 · M6 348/348.
Diagnosis: mild quantizer staircase pull (M1 half of unbiased) + an
independent-noise ribbon at fine-bin resolution (adjacent bins average
disjoint frame subsets), slightly structured (M2 above the 67% iid line).

## Iteration 1 — linear-weighted drizzle (target M3, M2)

Hypothesis: nearest-bin deposit gives adjacent bins disjoint contributor
sets; splitting each sample between the two adjacent bins by fractional
distance (the correct interpolation kernel — no information loss) correlates
neighbours and smooths the ribbon. Expected: M3 ↓ ~30%, M2 → ~67%, M4/M5
unchanged or better, M6 unchanged.

Ground truth (node): flat-noise A/B with identical phases — roughness 0.592
(linear) vs 0.845 (nearest) = −30% ✓.

Device result: PENDING.

## Iteration 2 — offset-DAC dither (target M1; secondarily M4/M5)

Hypothesis: front-end noise (~0.45 codes) is below the ~0.5 LSB needed to
dither the 8-bit quantizer, so averaging cannot converge between codes (the
staircase). Sweeping the offset DAC across frames in sub-LSB steps (8 phases
over 1 LSB, stepped every 2 frames, one settle frame skipped per step, the
commanded offset subtracted back in code space) slides the code thresholds
across the signal — quantization error becomes zero-mean and stacks away.
Risk: offset DAC granularity ~8 steps/LSB at 1 V/div (fewer at small V/div);
staging skew handled by the settle-skip.

Ground truth (node): sub-LSB-slope sine, noise 0.15 LSB, quantized —
slow-region rms error 0.145 → 0.051 codes (2.8×) with simulated dither ✓.

Device result: PENDING.

## Iteration 3 — TBD from iteration 1–2 data

## Iteration 4 — TBD

## Iteration 5 — TBD
