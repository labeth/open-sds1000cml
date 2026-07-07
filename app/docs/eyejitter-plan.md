# Eye diagram + jitter analysis — design (rev3: hardened + measured envelope)

The flagship serial-analysis package of high-end scopes (SDA/DPOJET-class paid
options), built on this clone's existing machinery: raw 500 MS/s deep records,
sub-sample edge interpolation (superres), binary transport, FFT, canvas
persistence. Validated the only honest way: the FPGA injects **known** jitter
and the instrument must measure exactly that.

## 1. Signal source (FPGA, ~/ws/fpga/proj)

`prbs.v` + `build-prbs.sh <mbps> [jitter_freq_khz jitter_amp_cycles]`:

- PRBS7 (x^7+x^6+1) NRZ on C1 (ball G1), bit period = integer sysclk cycles
  (100 MHz): 10 Mbps = 10 cyc, 5 Mbps = 20 cyc, 2 Mbps = 50 cyc.
- **Bit clock on C2** (ball M6) — ground truth for CDR validation.
- Jitter injection (DJ): **square-wave** phase modulation (rev2 — the design
  review + implementation both rejected the quantized sinusoid: round(A·sin) is
  trimodal with a non-obvious fundamental). Each bit boundary is delayed by an
  offset alternating 0 ↔ 2·JA sysclk cycles every JP/2 bits:
  TIE pp = 2·JA·10 ns, f_j = bitrate/JP, fundamental = (4/π)·JA·10 ns —
  fully analytic, so the amplitude check is exact, not quantization-fuzzy.
  Gotcha (found on HW): FF init values are unreliable through yosys/nextpnr —
  a plain XOR LFSR locks up if it powers up all-zeros; the feedback includes a
  zero-escape term (`^ (lfsr == 0)`).
- Phase-accumulator design: ideal edge counter runs uninjured; offset added at
  the OUTPUT stage only, so period errors don't accumulate (TIE, not random-walk).

## 2. Capture

- Web-first (like superres): `/api/frame.bin?raw=1` long-poll, native-fast
  class-0x20 records — 20480 samples @ 2 ns = 41 µs.
- At 10 Mbps: 50 samples/UI, ~410 UI/record, ~10–19 records/s → ~4–8k UI/s.
- Trigger on-signal (autoset); trigger phase is random per record — fine, all
  analysis is per-record self-referenced (CDR recovers phase each record).

## 3. Analysis engine — `internal/web/eyejitter.js` (pure, node-testable)

Mirrors the superres.js pattern: no DOM, module.exports for node tests, plain
script for the browser. State object `ejNew()`, per-record `ejFeed()`, results
`ejResult()`.

### 3.1 Edge extraction
- Mid-level = (min+max)/2 over the record (rail-robust percentiles: use p5/p95).
- All crossings, both slopes, sub-sample position by linear interpolation
  between straddling samples (same primitive as the engine's centerCross).
- Timing noise floor estimate: σ_edge ≈ noise_σ / slew_per_sample × 2 ns
  (~25–50 ps expected on this front end — report it honestly).

### 3.2 Clock recovery (CDR)
- UI estimate: histogram of consecutive-edge intervals; the minimum-interval
  mode = 1 UI (PRBS guarantees single-bit runs); refine by dividing each
  interval by its nearest integer-UI multiple and averaging.
- Ideal grid: least-squares fit of edge times to `t_k = t0 + n_k·UI` where
  `n_k = round((t_k−t0)/UI)` — iterate twice (constant-clock linear-fit CDR,
  the standard reference clock for TIE).
- Cross-check mode: when C2 carries the explicit clock, recover UI from C2
  edges and compare (validation only).

### 3.3 TIE + jitter metrics (per record, aggregated across records)
- TIE_k = t_k − (t0 + n_k·UI); rms + pp aggregated.
- **Histogram** (aggregated, ~100 bins).
- **Spectrum**: TIE resampled onto the uniform UI grid (gaps = data-dependent
  missing edges → linear interp), FFT per record, magnitude-averaged across
  records (Welch-style; trigger phase is incoherent so phases don't add).
  The injected f_j must appear as a peak of the programmed amplitude.
- RJ/DJ (dual-Dirac-lite): DJ = separation of left/right tail modes
  (histogram peak split), RJ = σ of the gaussian core (robust MAD-based).
  Report both with the caveat that full dual-Dirac needs BER extrapolation.
- Period jitter + cycle-cycle jitter (first differences of edge times).

### 3.4 Eye diagram
- Fold every sample at phase `(t − t0_fit) mod 2·UI` into a density map
  (W×H counts, like the persistence layer), accumulated across records.
- Render: log-scaled heat colormap onto a canvas.
- Metrics from the density map: eye height (at eye center: gap between the
  p1/p99 density envelopes of the two rails), eye width (at crossing level:
  gap between crossing clusters), crossing percentage, rise/fall 10–90.

## 4. Web UI — card "EYE / JITTER"

- ARM/STOP (raw feed loop shared pattern with superres; mutually exclusive
  with the superres armed state — both consume the raw feed).
- Auto bit-rate readout + override field (Mbps).
- Eye canvas (density heatmap, ~460×220), TIE histogram + TIE spectrum
  (small canvases), metrics table: UI/bitrate, eye H/W, crossing %,
  TIE rms/pp, RJ(σ)/DJ(δδ), period/c2c jitter, edges/s, records.
- All self-drawn (CSP: no external libs), classes not inline styles.

## 5. Device (stretch, only if time remains)

Go port of fold+render for the LCD (eye on the scope screen), metrics via
/api/status. Deferred by default — the web is the flagship surface here.

## 6. Validation ladder (HW, FPGA-driven)

1. **Clean PRBS 5/10 Mbps**: bit rate detected within a few hundred ppm (two
   INDEPENDENT crystals — "exact" is physically wrong, per the design review);
   eye open; TIE floor reported.
2. **Injected DJ**: f_j spectral peak within ±5% of programmed frequency;
   amplitude within ±20% of programmed (10 ns quantization caveats noted).
3. **CDR cross-check**: recovered UI vs C2 explicit clock — sub-0.1% match.
4. **Noise honesty**: with jitter OFF, RJ ≈ measured TIE floor; DJ ≈ 0.
5. Negative control: eye/CDR refuses gracefully on a non-serial signal
   (sine) — no fabricated bit rate lock (interval histogram has no UI mode).

## 7. Risks / open questions (for the design review)

- 10 ns injection quantization: sinusoid quantized to ±1 cycle steps → strong
  harmonics; is fundamental-only amplitude checking sound? Alternative: PLL
  400 MHz internal clock → 2.5 ns steps (nextpnr PLLE2 known-good).
- TIE spectrum via UI-grid resample with interpolated gaps: PRBS7 max run =
  7 UI → ≤7-sample gaps; acceptable spectral leakage?
- Records are 41 µs with ~59 ms dead time between (fps-limited): spectrum
  averaging is fine, but TIE *trend* across records is meaningless — per-record
  trend only.
- app.js size/perf: fold loop is 20480 samples/record at ~15 fps — trivial;
  density map render throttled like srUpdateStats (500 ms).
- Eye metrics from density percentiles vs BER-extrapolated: report as
  "measured at accumulated density", not BER-scaled (honest labeling).


## 8. Validation results (2026-07-07, real hardware)

Engine ground-truth tests (eyejitter.test.cjs, synthetic PRBS7 with exactly
known jitter): UI ±0.05 samples, TIE floor 71 ps, injected square rms
9.96/10 ns, DJ(δδ) 19.84/20 ns, spectral peak 156.2/156.25 kHz, fundamental
12.34/12.73 ns; negative controls (sine → clean-clock lock with no fabricated
DJ; noise → rejected; mid-run bit-rate change → rejected). ALL PASS.

Live scope (FPGA PRBS7 5 Mbps → C1, ideal clock → C2, Playwright on the web UI):

| leg | measured | expected |
|---|---|---|
| clean: bit rate | 5.00027 Mbps | 5 Mbps ± crystal ppm (54 ppm) |
| clean: TIE floor | 1.10 ns rms (RJ core 112 ps) | chain ISI-dominated |
| clean: DJ | 2.2 ns (real ISI of the ~40 MHz chain) | small |
| inject JA=1 JP=32: TIE rms | 10.03 ns | 10 ns |
| inject: DJ(δδ) | **19.99 ns** | 20.00 ns |
| inject: spectral peak | **156.25 kHz** | 156.25 kHz |
| inject: fundamental | **12.58 ns** | 12.73 ns (4/π·10) |

The instrument measures exactly what the FPGA injects. The clean-leg 2.2 ns
"DJ" is genuine data-dependent jitter (ISI) of the analog chain at 8.75 ns
rise vs 200 ns UI — honestly measured, not an artifact.

Engineering findings kept: RJ/DJ needs a BIMODALITY gate (splitting a gaussian
at its median always fabricates DJ = 1.35σ; test the central-band occupancy
about the midpoint of the side modes first). Test generators must build
waveforms from the EDGE LIST — a cell-indexed builder clips shifted edges at
the ideal boundary and injected jitter never reaches the waveform.


## 9. Adversarial hardening (2026-07-07, 50-family breaker)

`internal/web/eyejitter_breaker.cjs` (dev tool, seconds): 50 signal families —
encodings (PRBS7/15, UART-framed, burst/idle, clock, sparse pulses), unit
intervals (4…2000 samples, fractional), jitter types (square/sine/gaussian/
DCD/ISI at various amplitudes and rates), impairments (noise to swing/4,
baseline wander, AM, clipping, dropouts, runt glitches), degenerate inputs
(sine, chirp, noise, flat, two-tone, staircase). All 50 pass against ground
truth. Root causes found and fixed:

- **Dual-histogram-mode base/top levels** (IEEE-style): percentile levels
  under-read the rare rail of low-duty signals → the mid threshold sat low and
  fabricated ~±0.5-sample DCD on a clean sparse pulse train.
- **Run-length cap 16→64 UI** in the UI estimator: sparse trains and framed
  protocols legitimately idle for tens of UI; >64-UI intervals are excluded
  from the average but no longer count as evidence against the grid.
- **Cross-record interval pool**: an edge-starved record (huge UI: ~10 bits
  per record) cannot estimate UI alone; intervals pool across records and the
  grid resolves after a few.
- **Spectrum gap gate** (≥35% real UI-grid coverage): bursty streams made the
  TIE resampling mostly interpolation, fabricating subharmonic images of the
  injected tone. NRZ tops out at ~50% coverage, bursts sit near ~30%.
- **Eye width = the longest contiguous mid-row gap at the eye centre** (was
  over-counting both eye openings of the 2-UI fold).
- Guards: channel or V/div change mid-run stops honestly (same policy as
  superres). AM→PM conversion at a fixed threshold is documented physics.

Adversarial code review (3-lens workflow, all confirmed findings fixed with
regressions):

- **Period/cycle-cycle jitter over same-polarity edges only** — the
  cross-polarity accumulation let duty-cycle distortion masquerade as period
  instability (a stable clock with 8 ns DCD read "period 8 ns"; now DJ = the
  DCD and period jitter reads its true ~0).
- **TIE statistics never freeze**: exact running sum/sum²/min/max drive rms/pp
  (the first-N buffer froze all TIE stats ~1 min into a run while the panel
  looked live); the histogram/RJ-DJ buffer decimates (halve + double stride)
  so it stays representative of the whole run.
- **Eye vertical mapping frozen at first lock** (with 40%-of-swing headroom):
  the mutating y-scale re-interpreted old deposits and corrupted eye-height mV.
  Level wander now honestly widens the rails instead.
- **Per-record spectrum calibration** (each record's Hann span) before
  averaging — a last-record-only calibration biased the averaged tone
  amplitude when edge coverage varied.
- RJ/DJ display "—" until measurable (≥200 edges); PJ-dominated flag when the
  spectrum's tone explains most of the TIE power (dual-Dirac-lite caveat);
  t/div change mid-run stops with a message instead of rejecting forever;
  records without a timebase are skipped; mV readouts only with a real
  vertical calibration; click any diagram (eye / histogram / spectrum) for a
  full-screen live view with axes and the CDR-corner marker.

## 10. Measured hardware envelope (FPGA PRBS7 ladder)

| configuration | measured | truth / expectation |
|---|---|---|
| 2 Mbps, JA=8 (0.32 UI pp) | TIE rms 79.9 ns, DJ 156.5 ns, peak 62.49 kHz | 80 ns, 160 ns, 62.5 kHz |
| 5 Mbps clean | 5.00014 Mbps, floor 1.10 ns rms, RJ 112 ps | crystal ppm; chain ISI ~2 ns |
| 5 Mbps, f_j = 39 kHz (near 24 kHz corner) | peak 39.06 kHz, amp 13.5 ns | 39.06 kHz, 12.7 ns |
| 10 Mbps clean | 10.00056 Mbps, floor 1.09 ns, RJ 144 ps | crystal ppm |
| 10 Mbps, JA=1 | DJ 19.89 ns, peak 312.52 kHz, amp 12.35 ns | 20 ns, 312.5 kHz, 12.7 ns |
| 25 Mbps clean | locked, eye 112 codes, floor 1.11 ns, RJ 1.34 ns | 20 samples/UI |
| 50 Mbps | honest refusal (no-bit-grid, no false lock) | rise 8.75 ns = 0.44 UI: the ~40 MHz chain closes the eye |

Operating envelope: **2–25 Mbps** solid lock; jitter measured accurately from
the ~1 ns chain floor up to at least 0.32 UI pp; injected tones recovered in
frequency (exact) and amplitude (±10%) from ~1.6× the CDR corner up to
f_edge/2. 50 Mbps is an analog-bandwidth limit, not an algorithm limit — the
engine refuses rather than fabricating a lock.
