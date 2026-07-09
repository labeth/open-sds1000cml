# Changelog

## Unreleased

### Fixed
- **Vertical offset was inert on the sensitive ranges (≤200 mV/div).** The
  offset DAC injects *downstream* of the coarse attenuator relay, so its
  input-referred authority is ~46× weaker on the ×1 (sensitive) ranges than on
  the attenuated ranges. The firmware used one fixed slope (K = 262 codes/V,
  calibrated on the attenuated 1 V boot detent), leaving the offset ~46× too
  weak below 500 mV/div — effectively dead: no DC could be centred there. `K`
  now steps on the coarse-attenuator bit (`Detents[idx].Atten`): 262 attenuated,
  262·lever (≈ 11 996, lever ≈ 45.8) on the sensitive ranges; the panel offset
  knob steps a constant DAC-code count per range. Bench-validated on hardware:
  sensitive-range offset authority `g` rose from ~0.045 to ~2.0, matching the
  attenuated ranges (attenuated K unchanged). The sensitive-range offset span
  is now the true hardware ~±0.15 V (a larger DC still needs an attenuated range
  or external removal). Spec 06 §5.2 corrected. Env-tunable via
  `SCOPE_OFFSET_K` / `SCOPE_OFFSET_LEVER`. (Note: a separate firmware-wide
  32/50/64-codes-per-div convention incoherence — display vs SCPI vs cal — is
  now documented but unchanged here.)
- **FFT on fast (native-fast) timebases used the interpolated display window.**
  At a fast t/div the display is a short interpolated window (e.g. ~50 ns), so
  the FFT ran over a few real samples on a bogus multi-GHz axis (blocky, no
  fidelity). FFT mode now sources the full un-interpolated capture (the raw
  feed), restoring the true Nyquist and full frequency resolution regardless of
  the display zoom.
- **Reliability hardening (whole-stack fuzz campaign).** A long-holdoff or
  no-trigger acquisition no longer trips the OTA health watchdog into killing
  a healthy app (the health heartbeat now advances through long waits, not
  only on new frames). Fixed a decoder hang on a malformed serial config, a
  lock-ordering deadlock between a status poll and a mask install, and several
  frame-serving crashes reachable from degenerate frame geometry; request
  bodies on every control endpoint are now size-capped; and a remotely
  triggerable crash in the VXI-11/SCPI server's length parsing (an integer
  overflow on the 32-bit target) is closed. Added fuzz/chaos test suites (API,
  frame-serve, SCPI command + waveform readout, VXI-11 RPC, decoders,
  measurements, super-resolution, LCD render, panel, engine concurrency, and a
  browser UI monkey) that lock these down.

### Added
- **Decode-triggered super-res — stack a decoded protocol byte.** A software
  trigger on a *decoded value* (the serial-trigger idea, generalized to
  super-res): with the Decode card set to UART, "decode trig" stacks every
  occurrence of a target byte among free-run data — e.g. finds the one 'H' buried
  in "Hello world!" + random bytes each repeat, sub-sample aligns each occurrence
  and super-resolves it. Because the *decoder* (not a waveform match) decides
  what an occurrence is, one-bit-away lookalikes ('h' 0x68, 'I' 0x49) are never
  stacked. It's the aperiodic-event analogue of the free-run clock ETS, and
  reuses the gated multi-hit stacker (a new decode-driven hit-list). Validated
  live on a 10 Mbps UART: stacked 33 'H' events (2.2/frame — only the real 'H',
  not the decoys), +2.8 bits ENOB; re-targeting the decoy byte stacks it
  independently.
- **Super-res a non-triggerable clock (phase-coherent equivalent-time).** A
  "clock (ETS)" mode in the super-res panel reconstructs a periodic signal the
  scope *cannot trigger* — a clock above the ~65 MHz trigger comparator or near
  the ADC Nyquist (few samples/cycle), where trigger-edge/NCC alignment can't
  lock. It measures each free-run frame's fundamental phase with a single-bin DFT
  at the clock frequency (precise even at ~3 samples/cycle — it integrates every
  cycle in the record) and folds every sample onto one period at its phase, the
  way a sampling scope does random-interleaved equivalent-time. The clock
  frequency auto-detects (spectrum averaged over several frames so an aliased
  harmonic doesn't win) or can be set. Validated live on a free-run 150 MHz clock:
  auto-detected 150.007 MHz, reconstructed the 6.666 ns period at 19 GSa/s
  equivalent with +4.4 bits ENOB while the scope read AUTO/DEAD (untriggerable);
  with BW compensation on, the −30 dB-attenuated fundamental was boosted ×9.7
  toward true amplitude. Method: `app/docs/ets-clock-plan.md`.
- **Analog-falloff compensation for super-res (DSP bandwidth enhancement).** A
  super-res option that de-embeds the scope's measured analog rolloff from the
  crunched stack, recovering resolution the front end cost. It reshapes the
  crunched spectrum toward flat with a zero-phase Wiener inverse filter, so every
  view inherits it (Y-T edges sharpen, the FFT harmonic comb flattens,
  measurements read the true amplitude). It runs on the *stack* because it spends
  the stack's extra ENOB as high-frequency boost — a single frame has no headroom.
  The chain response was measured on the bench by a square-wave harmonic-comb
  method (−3 dB ≈ 16 MHz, dominated by an unterminated-jumper interconnect pole in
  series with the scope's own ~92 MHz front end; the FPGA driver's ~175 MHz share
  is < 1 dB below 82 MHz and left in as real signal). The recovered-bandwidth
  target is **adaptive**: it spends the stack's *measured* noise reduction (bits
  gained) as boost and recovers the highest −3 dB that budget allows, so a longer
  stack automatically reaches higher (+2.3 bit ≈ 76 MHz, +5 bit ≈ 150 MHz). Three
  goal **presets** (max bandwidth / max ENOB / fast capture) set every super-res
  knob for a tradeoff. Validated live: the raw stack droops to −8.7 dB by 55 MHz;
  compensated is flat within ±2 dB to 55 MHz, and an auto target on a +3 bit stack
  recovered 16 → 88 MHz. Method + measurements: `app/docs/falloff-comp-plan.md`.
- **Serial / protocol trigger** — trigger on a *decoded* value, not just an
  edge: publish only frames whose UART / I²C / SPI stream contains an
  operator-specified pattern (I²C address + R/W ± data byte; a byte or byte
  sequence for UART/SPI), re-centred on the match. It runs in the acquisition
  engine (reusing the pure `internal/decode` package the LCD/web decoders share),
  so it works for SINGLE/NORM and device-standalone, and composes with the zone
  trigger. NORM/SINGLE hold strictly for the match; AUTO keeps a liveness
  fallback (use AUTO for async UART, NORM for clocked I²C/SPI). It is a sub-panel
  of the Decode card and REUSES the live decode config (protocol, channels, baud,
  CPOL/CPHA/MSB, threshold) — decode must be on, and you enter only the match
  pattern. Hardened via an adversarial-review pass: the match resolves before the
  zone/mask/average gates so all share one anchor (a rejected frame can no longer
  trip mask stop-on-fail or smear an average); only valid `data` bytes match
  (never framing/parity-error bytes) and a multi-byte pattern must be contiguous
  on the wire (no bridging idle gaps); addr/baud are range-clamped server-side and
  the arm handler installs the config before arming. Validated live against the
  FPGA protocol generators (UART + SPI): matched sequences/bytes, rejected absent
  patterns, correct AUTO-liveness vs NORM-strict gating, and no wedge — with only
  ~5 % fps cost at normal memory (a bounded cost on deep memory).
- **Zone trigger** — draw up to 4 rectangles on the display (web); frames must
  intersect (or avoid) every zone to publish: a graphical software trigger the
  hardware comparator cannot express. NORM holds strictly; AUTO keeps a
  liveness fallback. Validated live: zero unqualified leaks in both polarities.
- **Mask testing** — build a golden min/max envelope from N live frames
  (web or on-device), dilate by ±time/±voltage tolerances, then test EVERY
  captured frame at the full acquisition rate: pass/fail/skip counters, a
  failure ring with capture-and-view, stop-on-fail that freezes the offending
  frame on screen. Guards make the verdict honest: band/vertical identity
  stamps (a stale mask counts skips instead of lying), dead-tail exclusion,
  average-mode exclusion, DC-coupling precondition. On-device MASK page
  (re-press ACQUIRE past REF) + LCD envelope/zone render + live meter.
  Validated with a counted-truth FPGA source: 0 false positives over a clean
  soak, catch rate on the binomial expectation (n=2350), every capture
  localized to the known violation offset, and two 50-waveform adversarial
  breaker campaigns (envelope morphology; zone differential fuzz + publish
  policy + window geometry — see docs/zonemask-plan.md).
- Masks and zones re-anchor automatically when V/div or offset changes (they
  are physically volts); a test that can only skip says MASK STALE instead
  of looking happy, and slow env/roll timebases count their structural
  zone/mask bypass instead of hiding it.

## v0.0.2 — 2026-07-05

### Added
- **Super-resolution (stack & crunch)** — equivalent-time stacking of a repetitive
  waveform (sub-sample alignment → lucky-frame selection → drizzle onto a fine grid)
  recovers resolution below the 8-bit ADC. Measured ~5 extra bits (≈13-bit effective)
  on a repetitive signal. A first-class view: toggle on/off, run on either channel,
  and do FFT / measurements / X-Y on the stacked result. Diminishing returns near
  Nyquist, where alignment jitter — not noise — is the limit.
- **Zoomable FFT** — navigator strip, rubber-band box-zoom, clickable peak
  measurements, a live dB/frequency pointer readout, and physics markers (raw Nyquist
  + the ~100 MHz analog-bandwidth line) on stacked spectra.
- **Rubber-band box-zoom in time *and* voltage** on the Y-T view.
- **Trigger holdoff** as a front-panel control (device parity).

### Changed
- **Binary long-poll frame transport** — the browser now renders at the engine's
  frame rate at every timebase and memory depth (deep captures used to crawl).
- **Render hot-paths capped** — every interaction stays under ~18 ms regardless of
  settings, even fully zoomed out on a deep record with FFT and a large stack active
  (previously hundreds of ms to several seconds).
- Fast-flow measurement recompute throttled to 10 Hz for smoother interaction.

### Fixed
- Clipping detector was mis-calibrated for this hardware.

## v0.0.1

First public release — clean-room acquisition engine, 33-detent timebase,
triggering (edge/pulse/slope/video + holdoff), vertical front end, measurements,
web UI (Y-T / X-Y / FFT, cursors, math, references, decode), on-device LCD + front
panel, SCPI/VXI-11 host interface, and the OTA supervisor + USB boot flow.
