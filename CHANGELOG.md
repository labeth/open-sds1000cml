# Changelog

## Unreleased

### Fixed
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
- **Serial / protocol trigger** — trigger on a *decoded* value, not just an
  edge: publish only frames whose UART / I²C / SPI stream contains an
  operator-specified pattern (I²C address + R/W ± data byte; a byte or byte
  sequence for UART/SPI), re-centred on the match. It runs in the acquisition
  engine (reusing the pure `internal/decode` package the LCD/web decoders share),
  so it works for SINGLE/NORM and device-standalone, and composes with the zone
  trigger. NORM/SINGLE hold strictly for the match; AUTO keeps a liveness
  fallback (use AUTO for async UART, NORM for clocked I²C/SPI). Configured from
  the web "Serial Trigger" card. Validated live against the FPGA protocol
  generators: matched the exact UART sequence and the SPI payload byte on real
  captures, rejected absent patterns, and gated the display correctly (AUTO
  liveness heartbeat vs NORM strict hold).
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
