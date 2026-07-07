# Eye diagram + jitter analysis — design (rev2, as built + validated)

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
