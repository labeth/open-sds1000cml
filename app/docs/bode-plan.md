# Bode plot / Frequency Response Analysis (FRA) — design (rev1)

A Bode plot shows a DUT's transfer function: magnitude (dB) and phase (degrees)
versus log frequency. It needs a stimulus swept across frequency and a
two-channel measurement — CH1 = the stimulus reference (DUT input), CH2 = the
DUT output.

This scope has NO built-in AWG, so it cannot own the sweep. The honest, useful
design is **external-sweep FRA**: the operator drives the DUT with a swept
signal generator (or the FPGA stepped across frequencies), feeds the input to
CH1 and the output to CH2, and the scope ACCUMULATES a Bode point at each
frequency as the source sweeps. This is exactly the FRA mode scopes without a
generator support ("connect a gen, sweep it, we build the plot").

## 1. The measurement (engine, single source of truth)

Per locked frame, when FRA is armed, compute ONE point from the two channels:

1. **Fundamental f0** — from the REF channel, by mean-crossing period
   detection (cheap, works for square or sine stimuli). No FFT needed.
2. **Single-bin DFT** at f0 on both channels (values centred on their mean):
   `X = Σ v[n]·exp(-j·2π·f0·n·dt)` over the valid record.
   - `gain_dB = 20·log10(|X2| / |X1|)`
   - `phase_deg = arg(X2 · conj(X1))`, wrapped to (−180, 180]
   KEY ROBUSTNESS: gain and phase come from the RATIO X2/X1 at a SHARED f0, so
   a small f0 error smears both integrals IDENTICALLY and CANCELS in the ratio
   — the relative magnitude/phase stay accurate even with an approximate f0.
3. `ok` requires |X1| and |X2| above a noise floor and a plausible f0
   (≥ a few cycles in the record, below Nyquist).

Cost: two O(N) DFT accumulations, only while armed. No per-frame FFT.

## 2. Accumulation (engine)

Points are binned by log-frequency (`binsPerDecade = 30`): a frame at frequency
f updates the bin `round(log10(f)·binsPerDecade)`. A moving sweep fills bins;
re-visiting a frequency updates it (latest wins). The map is exposed sorted by
frequency. This dedups the many frames captured at one generator step and lets
the FPGA's discrete frequency steps build a clean curve.

- API: `SetBodeMode(on, refCh, dutCh)`, `ClearBode()`, `BodePoints() []BodePoint`,
  live current point in `Stats` (bode_freq / bode_gain_db / bode_phase_deg /
  bode_valid / bode_points).
- FRA and normal display coexist; FRA does not change the publish policy.

## 3. Web UI

An FRA card: arm/clear, ref-channel and DUT-channel selects, a live readout of
the current (f, gain, phase), and two stacked plots vs LOG frequency —
magnitude (dB) on top, phase (°) below — with gridlines, auto-ranged axes, and
the swept points connected. Points fetched from `/api/bode`. A full-screen
enlarge (like the eye/FFT) is a nice-to-have.

## 4. Device (LCD + panel)

A BODE view (like X-Y / FFT), reachable from the panel, rendering the same
accumulated magnitude + phase curves the engine holds — one implementation, so
the bench screen and browser agree. Panel entry: a BODE toggle / menu slot;
arm/clear via a softkey.

## 5. Validation ladder

1. Engine unit tests: synthetic two-channel records with KNOWN gain and phase
   — a scaled + phase-shifted sine → the measured (gain_dB, phase_deg) matches
   analytically; a pure time delay τ → 0 dB and phase = −360·f·τ (mod 360);
   noise floor / sub-cycle rejection.
2. Parity: the LCD Go path and the web accumulation agree on the same frames.
3. HARDWARE (FPGA):
   - **Flat**: the same tone on C1 and C2 (both driven from one source) →
     0 dB, 0° at every stepped frequency. Step `build.sh <MHz>` across a band
     → a flat 0 dB / 0° Bode curve.
   - **Pure delay**: an FPGA source driving C1 and C2 = C1 delayed → 0 dB
     magnitude and a phase that RAMPS linearly with frequency (−360·f·τ). This
     is an analytically exact, non-trivial Bode curve achievable with a
     purely-digital FPGA (no DAC).

## 6. Validation results (rev1, on hardware)

FPGA source `bode.v` / `build-bode.sh [delay_ns]` steps a tone through 6
log-spaced frequencies (0.5–5 MHz), holding each ~200 ms, on C1 with C2 = C1
delayed by delay_ns — the scope (FRA armed) accumulates one point per step, a
swept Bode curve from a single flash.

- **Flat** (`build-bode.sh 0`, C1 == C2): magnitude 0.1 dB at every stepped
  frequency (≈ 0 dB, correct); phase a small linear ramp (1°→10° over
  0.5→5 MHz) = the real ~5 ns two-channel skew (FPGA routing + scope front
  end), which a Bode plot honestly shows.
- **Delay** (`build-bode.sh 60`): magnitude flat 0.1 dB; phase ramps linearly
  and negatively with frequency. The phase-derived total delay (117 ns from a
  least-squares fit of unwrapped phase vs frequency) matches an INDEPENDENT
  raw C1→C2 cross-correlation (116 ns) to within 1 ns — so the phase
  measurement is exact; the physical delay is simply larger than the nominal
  FPGA parameter (routing + pipeline). Phase wraps to ±180° as expected.
- **Device parity**: the LCD BODE view (DISPLAY ▸ View ▸ Bode) renders the same
  flat magnitude + ramping phase against the same log-frequency axis.

Unit tests (`bode_test.go`) lock the measurement against synthetic records with
known gain (0/±6 dB), phase (0/±45/±90/+120°), a pure delay (linear ramp),
noise-floor and sub-cycle rejection, and frequency-bin accumulation. The web
renderer's log-tick / range / format helpers are locked by a node test.
