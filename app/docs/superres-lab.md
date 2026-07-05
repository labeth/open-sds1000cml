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

Device result (v0.0.1-10, dither off): M1 0.175 · M2 55.9% · M3 0.1645
(−17%) · M4 0.1156 (−19%) · M5 +1.95 · M6 345/345. **KEPT** — target M3
improved ≥15%, M2 dropped below the iid line (structure gone), every
secondary metric improved.

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

Device result (v0.0.1-10, dither on, commanded-value correction):
M1 0.371 (!) · M2 64.6% · M3 0.1833 (+11% vs iter 1) · M4 0.1362 (+18%) ·
M5 +1.57 (−0.38) · M6 219 in 25 s (settle-skips cost 37%). **ROLLED BACK**
(default off). Analysis: M1 OVERSHOT past 0.25 to half-code clustering — the
signature of a ~2-phase dither: the offset DAC applies coarser steps than
the commanded 8 phases and/or the cal volts mapping doesn't match the true
analog shift, so subtracting the COMMANDED value mis-corrects frames and
adds noise instead of removing bias. The mechanism is right (ground truth
holds); the correction source is wrong.

## Iteration 3 — data-driven dither correction (target M1 → 0.25±0.05)

Hypothesis: don't trust the offset cal — measure it. Keep the DAC sweep +
settle-skip, drop the commanded-value subtraction, and let the ALIGNED
gain/offset drift fit (already in srFeed, window-restricted) absorb each
frame's actual vertical shift vs the base-offset reference. Per-frame b-fit
precision ≈ σ/√window ≈ 0.45/√4096 ≈ 0.007 codes — far better than any cal
table, immune to DAC granularity (whatever shift REALLY happened is what
gets removed). Prediction: M1 0.25±0.05, M3 ≤ 0.165 (iter-1 level), M4 ≤
0.12, M5 ≥ +1.9, M6 ~220/25 s (settle cost stays).

Device result (v0.0.1-11, dither on): M1 0.111 (wrong direction) ·
M2 62.2% · M3 0.1706 (+4% vs iter 1) · M4 0.1212 (+5%) · M5 +1.62 (−0.33) ·
M6 220/25 s. **ROLLED BACK** (dither stays default-off; the sweep code
remains dormant behind the toggle).

Post-mortem — two findings worth keeping:
1. The data-driven fit CANCELS dither by construction: b̂ = true shift +
   window-mean quantization error, so normalizing by it re-pins every frame
   to the reference's quantization grid. You cannot measure the correction
   from the same quantized data you are trying to de-bias.
2. The premise was thin: with σ_single ≈ 0.45 codes of real front-end
   noise, the quantizer is ALREADY noise-dithered — Gaussian theory puts the
   residual staircase bias at ~e^(−2π²σ²)/π ≈ 0.006 codes. And M1 on a
   two-level square mostly measures where the top level happens to sit
   relative to a code, not bias — a weak metric. The visible artifact was
   the RIBBON all along (iteration 1's target). Offset dither on this
   hardware/signal attacks a solved problem and only pays overhead; the
   loop rejected it twice for the right underlying reason.

## Iteration 4 — interpolated resampling (target M4/M5; guard M7 rise ≤ +10%)

Hypothesis: the linear-drizzle kernel spans 2 FINE bins (= 2/K raw
samples), so each fine bin averages only ~N/K frames — the ribbon is
under-averaging by design. Resampling each aligned frame onto the fine grid
(linear interpolation between its two neighbouring RAW samples, i.e. a
triangular kernel of width one raw interval) gives EVERY fine bin a
contribution from EVERY frame: per-bin noise drops ~√K (K=32 → ~5.7×,
≈ +2.5 bits), and the phase diversity across frames still carries the
sub-sample information (each frame's interpolant is anchored at its own
jittered sample positions). Cost: linear interpolation low-passes at
sinc²(f/fs_raw) — the stacked edge may soften. New guard metric M7 = 10–90%
rise of the stacked edge in fine bins; keep only if M7 regresses ≤10%.

Device result (v0.0.1-12): interp M1 0.071 · M2 3.7% · M3 0.0003 · M4
0.0164 · M5 +4.22 (12.2 eff bits at 25 s!) · M6 324 · M7 101 fine bins.
Same-build drizzle control: M3 0.0406 · M4 0.0866 · M5 +2.03 · M7 95.
**KEPT** — target M4 −81% (5.3× same-day A/B), M5 +2.2 bits, ribbon
extinct (M3 −99%), guard M7 +6.3% ≤ 10%. The √K under-averaging hypothesis
was the dominant artifact mechanism all along.

## Iteration 5 — Catmull-Rom resampling (target M7; guard M4 ≤ +10%)

Hypothesis: iteration 4's +6% edge blur is the linear interpolant's sinc²
roll-off. A cubic Catmull-Rom kernel (4 raw samples) has a flatter passband
— it should recover most of the rise-time cost at the same
every-frame-every-bin contributor count. Risk: mild ringing at sharp edges
(bounded, Catmull-Rom overshoot ≤ ~7% of a step) — but the REAL edge is
analog-bandwidth-limited (~9 raw samples 10-90), far from the kernel's
ringing regime. Keep if M7 improves ≥3% with M4/M5 within 10%.

Device result (v0.0.1-13, kernel=cubic): M4 0.0185 (+12.8% vs interp) ·
M5 +4.24 · M7 98 vs 101 (−3.0%). **ROLLED BACK** — the target improvement
is at the threshold while the M4 guard is violated; the device edge is
analog-bandwidth-limited (~9 raw samples 10–90), so the kernel's passband
isn't the bottleneck, exactly as the node ground truth suggested. The cubic
kernel stays selectable (st.kernel="cubic") but off by default.

## Result of the loop

| | M1 | M2 | M3 rough | M4 σ_stack | M5 bits | M7 rise |
|---|---|---|---|---|---|---|
| baseline (nearest, v0.0.1-9) | 0.122 | 74% | 0.198 | 0.143 | +1.6 | — |
| final (interp, v0.0.1-12/13) | 0.071 | 3.7% | **0.0003** | **0.0164** | **+4.22** | 101 |

Two of five iterations kept (1: linear drizzle; 4: interpolated resampling),
three rolled back with documented reasons (2, 3: offset dither variants —
the quantizer was already noise-dithered at σ≈0.45 codes and the visible
artifact was never the staircase; 5: cubic kernel — analog bandwidth, not
kernel passband, limits the edge). Net effect on the artifact the loop was
opened for: the zig-zag ribbon is extinct (roughness −99.8%), stacked noise
is 8.7× lower, and a 25 s stack now measures **12.2 effective bits**
(+4.2 over the 8-bit ADC), rising with capture time as ½·log₂(frames).
