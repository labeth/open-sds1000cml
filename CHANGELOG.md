# Changelog

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
