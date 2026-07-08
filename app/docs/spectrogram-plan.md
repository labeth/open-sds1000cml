# Spectrogram ("FFT over time") — design (rev1)

A spectrogram / waterfall shows how the spectrum EVOLVES over time: compute the
FFT of each successive capture and stack the magnitude spectra as rows of a 2D
heatmap — X = frequency (0..Nyquist), Y = time (newest at top, scrolling down),
colour = magnitude in dB. It reveals frequency drift, intermittent tones,
modulation, and sweeps that a single FFT snapshot cannot.

## 1. Data model

Each locked, per-sample frame → one Hann-windowed magnitude spectrum (the same
FFT the FFT view already computes) → one colour-mapped ROW. Rows scroll: the
newest is drawn at the top, everything shifts down one pixel, the oldest falls
off. This is a SCROLLING IMAGE, not a stored stack — one new row + a one-row
memmove per frame, cheap enough for the 20 fps device loop and the browser.

Colour: dB relative to the row's own peak, mapped `floorDb..0` through a
perceptual ramp (black → blue → cyan → green → yellow → red → white).

## 2. Device (LCD)

A 5th view mode (DISPLAY ▸ View ▸ Spgm; ViewMode 4). The LCD loop owns a
scrolling MemSurface: each fresh frame in this view, compute the spectrum,
scroll the image down one row, and paint the new top row (frequency across,
colour = dB). Render blits the image with a frequency axis + a dB colour key.
Render stays pure (screenshot-safe) — the accumulation lives in the loop, the
image is handed in via the HUD.

Reuse: `fftRadix2` for the transform; extract `spectrumMags(src, n, stride)`
from the existing `fftTrace`. Effective Nyquist = the FFT view's (`effNyq`).

## 3. Web (host)

A Spectrogram card + `spectrogram.js`: a rolling ImageData waterfall fed by
`spectrum()` (peaks.js) each frame; the same colormap; render scaled onto a
canvas with a frequency axis. Full-screen enlarge (reuse the eye/Bode big-view
shell). Arm/clear + a floor-dB control.

## 4. Verification

Unit tests (Go): a single-tone frame → its spectrogram row peaks at the correct
frequency column; a frequency that STEPS across successive frames → the peak
column MOVES down the rows (the whole point of "over time"); floor/scroll
correctness; the colormap is monotonic in dB. A node test locks the JS
colormap + frequency-bin mapping against the Go one (parity).

## 5. Validation (hardware)

The stepped-frequency FPGA source `bode.v` (steps 0.5→5 MHz every ~200 ms) is
ideal: the spectrogram must show a STAIRCASE — the bright peak jumping between
the six frequencies over time.

## 6. Validation results (rev1, on hardware)

Flashed `bode.v` (stepped 0.5–5 MHz on C1), scope at 10 µs/div (Nyquist high
enough to resolve the steps):

- **Web**: armed the waterfall, accumulated 49 rows over ~7 s. The brightest
  column per row cycled through exactly the six stepped frequencies (0.5, 0.79,
  1.25, 2.0, 3.13 MHz visible) — a clear staircase; the peak MOVES over time,
  which is the whole point of "FFT over time".
- **Device (LCD)**: the SPECTROGRAM view (DISPLAY ▸ View ▸ Spgm) renders the
  same waterfall — the raw record is decimated to ~2048 samples so the 0.5–5 MHz
  steps spread across a 0–5 MHz axis; the stepped peaks are clearly visible as
  distinct columns moving down the rows (screenshot-confirmed).

Unit tests (`spectrogram_test.go`): a single-tone frame peaks at the correct
column; a frequency that STEPS across successive frames moves the bright column
down the rows; the colormap is monotone in brightness. A node parity test locks
the JS colormap + bin mapping against the Go path.
