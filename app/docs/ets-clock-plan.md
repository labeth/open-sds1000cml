# Super-res a non-triggerable clock (phase-coherent equivalent-time)

Reconstruct a periodic signal the scope **cannot trigger** — a clock above the
~65 MHz trigger comparator and/or near the ADC Nyquist (few samples/cycle),
where the normal trigger-edge + NCC alignment can't lock. Demonstrated on a
free-run **150 MHz** clock (3.3 samples/cycle at 500 MSa/s).

## Why the normal stacker fails

Super-res aligns each frame to a reference sub-sample and stacks. That needs
(a) a coarse anchor (the trigger edge) and (b) enough samples/cycle for NCC to
disambiguate. A 150 MHz clock has **neither**: it can't trigger (free-run, random
phase per frame) and at 3.3 samples/cycle NCC is period-ambiguous. So the frames
never align and nothing stacks.

## The technique — measure phase, fold

Every free-run frame still samples the *same periodic signal*, just at a random
absolute phase. And a single-bin DFT at the clock frequency measures that frame's
phase **precisely** — it integrates over all ~6000 cycles in the record, so the
phase estimate is milliradian-accurate even though each cycle has only 3 samples.

So: for each frame, measure its fundamental phase φ (DFT at f), then **fold** every
sample x[n] onto a one-period grid at phase `(f·n·dt + φ/2π) mod 1`. Including φ
aligns all frames to a common phase reference. Averaging the thousands of samples
that land in each phase bin reconstructs one period at high effective resolution
and drops the noise ~√N — exactly a sampling scope's random-interleaved
equivalent-time reconstruction. No trigger, no NCC.

## Engine (`superres_ets.js`)

* `srEtsRefineFreq(x, dt, fGuess)` — coarse-to-fine + parabolic search maximizing
  |X(f)|; the max-likelihood single-tone estimate. f must be accurate to
  ~1/(record length) or the fold smears across the record — the search gets there.
* `srEtsFeed(st, c1, c2)` — measure the align channel's phase, fold both channels.
  An **SNR gate** (tone magnitude vs two off-tone probes at f·0.71 and f·1.29)
  rejects frames with no real tone; it scales correctly with record length (a
  tone's DFT grows ∝ N, white noise's ∝ √N).
* `srEtsResult(st)` — one reconstructed period per channel + measured ENOB from
  the odd/even half-stack.

## UI (`app_superres.js`, `#srEts`)

A "clock (ETS)" checkbox in the super-res panel + an optional frequency field
(blank = auto-detect). Arming a free-run acquisition folds every frame; the view
shows the reconstructed period (two tiled) at high effective sample rate; the BW
compensation applies (recovering the attenuated high-frequency amplitude); it
auto-switches the trigger to AUTO (free-run).

**Auto-detect gotcha (fixed):** a square clock's above-Nyquist harmonics ALIAS
back into band (150 MHz's 3rd → 450 MHz → aliases to 50 MHz), and on any *single*
free-run frame that alias can outweigh the −30 dB-attenuated fundamental — so
single-frame peak-pick locked onto 50 MHz. Fix: incoherently average the spectrum
over the first 8 frames before locking (the true tone wins the average, matching
measure.py's global-peak result). A user-set frequency skips detection.

## Validation

* **Unit** (`superres_ets.test.cjs` / `TestSuperresEtsJS`): synthetic free-run
  frames at random phase + noise (3.3 samples/cycle) — recovers f to <50 ppm,
  reconstructs the amplitude exactly, gains +5.6 measured bits, keeps a square's
  3rd harmonic (~1/3 fund), suppresses a sine's harmonics, rejects a pure-noise
  frame.
* **Live HW** (`ets_live.mjs`, the real free-run 150 MHz clock): auto-detected
  **150.0068 MHz**, 62 frames, 0 rejects, **+4.4 bits ENOB**, 6.666 ns period,
  19.2 GSa/s equivalent, fill 100% — while the scope reads AUTO / DEAD (it truly
  can't trigger). With BW comp on, the −30 dB-attenuated fundamental was boosted
  **×9.7** (recovered −3 dB 133 MHz, +19.8 dB) toward true amplitude. Two clean
  reconstructed sines on C1/C2.

The ETS + BW-compensation combo is the payoff: ETS pulls the clock out of a
free-run acquisition and averages down the noise (buying ENOB → boost budget),
and the compensation spends that budget to undo the analog rolloff that made a
150 MHz signal only 0.09 V at the scope in the first place.

## Sibling: decode-triggered super-res (aperiodic events)

The clock ETS folds free-run frames at a *periodic* reference (the clock phase).
Its aperiodic sibling — **"decode trig"** in the same panel — software-triggers on
a *decoded protocol value* and stacks each occurrence. With the Decode card set to
UART, entering a target byte (hex `48` or `H`) makes the stacker, per free-run
frame, decode the align channel, find every occurrence of that byte, sub-sample
align each (the gated multi-hit stacker with a decode-supplied hit-list —
`srGateFeed({hitCenters})`), and stack. Because the *decoder* decides what an
occurrence is, one-bit-away lookalikes are never stacked.

Test source: `uart.v` sends "Hello world!" + decoys ('h' 0x68, 'I' 0x49) + random
non-'H' bytes at 10 Mbps, so the 'H' is a rare event in noise. Validated live:
software-triggered on 'H', stacked 33 occurrences (2.2/frame — only the real 'H'),
+2.8 bits ENOB; re-targeting 'h' stacks it independently. RATE NOTE: on this
bench the ~16 MHz analog chain rounds edges to ~22 ns, so decoding tops out around
~25 Mbps — a literal 150 Mbps stream is an undecodable smear; the clock ETS covers
the pure-150 MHz-periodic case, decode-trig covers decodable data.

## Deferred

* Longer captures → more bits → fuller amplitude recovery (the ×9.7 here is
  budget-limited at +4.4 bits; a minutes-long fold reaches the full ~×30).
* More vertical swing: at a sensitive V/div the raw 0.09 V tone fills more codes
  (better raw SNR per sample) — worth setting before a long fold.
* decode-trig: I2C/SPI (v1 is UART); decode-driven align could also feed the
  eye diagram (a decoded-byte-triggered eye).
* Device/LCD port (Go).
