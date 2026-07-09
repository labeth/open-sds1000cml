# Analog-falloff compensation (DSP bandwidth enhancement) for super-res

A super-res option that de-embeds the scope's analog rolloff from the crunched
stack, recovering resolution the front end cost. Design, measurement, FPGA-share
attribution, filter design, adaptive/preset behaviour, and validation.

## The idea

The analog front end attenuates high frequencies (a rolloff). The super-res
stack has **spare ENOB** (+2–5 bits, i.e. a noise floor 12–30 dB below one raw
frame). We can spend that headroom as a frequency-domain **inverse filter**:
FFT the crunched fine grid → multiply by a boost that reshapes the measured
channel response toward flat → IFFT. The boost amplifies noise, but the stack is
quiet enough to absorb it. A single frame has no headroom to give — this only
works on a stack.

Because it runs on the crunched grid before the synthetic review frame is built,
**every view inherits it**: Y-T (sharper edges), FFT (flat harmonic comb),
X-Y and all measurements (true amplitude).

## Measuring the falloff — the square-wave harmonic comb

A tone sweep is fragile: the source amplitude and V/div can drift between builds
(we saw 2 MHz read 2.59 Vpk but a separate 10 MHz build read 1.18 — not
comparable). A **50 %-duty square** sidesteps this: it has energy only at odd
harmonics with amplitude exactly ∝ 1/m, so **one capture** samples the chain
response at every odd harmonic, self-normalised against the known 1/m envelope:

    H(m·f0) = amp_meas(m·f0) · m / amp_meas(f0)          (H(f0) := 1)

`~/ws/fpga/proj/falloff.py <fund_MHz>` drives an FPGA square (build.sh), captures
~60 raw frames, incoherent-power-averages the spectrum, and reports H at each
odd harmonic. Two fundamentals (2 & 5 MHz) were run and **overlaid to < 1 dB**
(45 MHz: −8.56 vs −8.42; 65 MHz: −11.28 vs −11.03) — proving source-independence.
`analyze_falloff.py` fits models + attributes the FPGA share.

### Measured result (this bench, C1 via a bare jumper, DC, 1 V/div)

Trustworthy band 2–85 MHz (above ~90 MHz the odd harmonics sink into the noise):

| f (MHz) | 8 | 16 | 24 | 40 | 56 | 72 | 88 |
|---|---|---|---|---|---|---|---|
| H (dB) | −1.2 | −3.0 | −4.7 | −7.5 | −9.1 | −12.7 | −18.8 |

* **Chain −3 dB ≈ 16 MHz.**
* Best fit is a **2-pole product**: `fca ≈ 20.8 MHz × fcb ≈ 92 MHz` (rms 1.2 dB).
  A near-1-pole below 55 MHz, with a ~2 dB shelf at 55–62 MHz (a mild wire
  resonance) the smooth model can't capture — so we bake the **measured curve**
  as a cal table (`SRCOMP_HCAL`, 0–92 MHz @ 4 MHz) with the 2-pole as the
  extrapolation tail.

## Attributing the FPGA share (best guess from specs)

The 2-pole fit decomposes the falloff physically:

* **fcb ≈ 92 MHz** ≈ the SDS1102CML+'s rated **100 MHz** front end — the fit
  found it unaided. This is the *scope*.
* **fca ≈ 20.8 MHz** — the dominant pole. Far below any 100 MHz scope, so it is
  the **interconnect**: an unterminated jumper wire + FPGA source-Z + scope
  input-C (R·C ≈ 50 Ω · ~200 pF ≈ 16 MHz). This is the *setup*, not the scope.
* **FPGA driver ≈ 175 MHz** (Artix-7 XC7A35T LVCMOS33, fast slew, ~1–2 ns edges;
  cross-checked by the 8.75 ns chain rise = 0.35/40 MHz being scope-dominated,
  so the FPGA edge is much faster). At 82 MHz it contributes only **−0.86 dB** —
  a few-percent share.

**Best guess for "the real value":** the true signal the FPGA produced is flat
to ~150 MHz (its own corner). Nearly all the measured falloff is instrument +
setup. We compensate that and stop the target **below the FPGA corner**, so we
recover the FPGA's real output without over-restoring past what it made. The
FPGA's own rolloff is reported, not removed (it is real signal).

## The compensation filter

Zero-phase (magnitude-only; the front end is ~minimum-phase so this recovers
most of the edge) target-reshape, the way scopes do DSP BW enhancement:

    G(f) = Htarget(f) · Hcal(f) / (Hcal(f)² + ε²)        (Wiener-regularised)

* `Hcal` = measured cal table (2-pole tail beyond 92 MHz).
* `Htarget` = flat-top super-Gaussian (order 3), −3 dB at `fbw` — bounds the
  high-frequency noise gain (rolls off before Hcal hits the noise).
* `ε`, `gmax` cap the peak boost.

Applied via radix-2 FFT after resampling the fine grid to the nearest power of
two (exact frequency map: bin k ↔ k/T where T = M·dtFine). Gaps (−1) filled
before, restored after; DC held at unity; output floored at 0.

At the default **fbw = 70 MHz**: recovers −3 dB **16 → 61 MHz (3.7×)** for
**+7.6 dB** peak boost, 0.9 dB in-band ripple.

## "Optimize as high as possible" — the adaptive (auto) target

The boost budget **is** the measured noise reduction. G(f) amplifies stack noise
by G(f); the stack is quieter than one raw frame by `2^bitsGained`, so the
largest boost keeping the result no noisier than a single acquisition is
`bitsGained · 6.02 dB`. `srCompAuto(bitsGained, rawNyq, spend)` spends a fraction
`spend` of that and picks the **highest recovered −3 dB** whose peak boost fits.
So a longer, quieter stack automatically reaches higher:

| bits gained | budget | recovered −3 dB |
|---|---|---|
| +1.5 | 7 dB | 62 MHz |
| +2.3 | 11 dB | 76 MHz |
| +3.3 | 16 dB | 95 MHz |
| +5.0 | 24 dB | 149 MHz |
| +7.0 | 34 dB | 174 MHz |

**Ceilings** (why not 200 MHz on this bench): the raw ADC Nyquist (250 MHz —
nothing beyond); the cal is only measured to ~85 MHz (higher is 2-pole
extrapolation — a best guess); near Nyquist super-res *alignment jitter*, not
noise, is the limit; and the signal itself is 30–40 dB down by 150 MHz. The
practical ceiling here is ~170 MHz at a very long stack. To truly reach 200 MHz
you'd fix the **interconnect** (the ~21 MHz wire pole — a proper 50 Ω probe lifts
the chain to the scope's own ~92 MHz), not just stack longer.

## Presets (configure every optimization knob)

`SR_PRESETS` set grid/kernel/stop/dither/comp/target/spend (leaving only the
signal choices — channel, gate — to the user):

* **max bandwidth** — K 64, interp, time 60 s, comp auto @ spend 0.9: accumulate
  bits and spend them as boost (highest recovered −3 dB).
* **max ENOB (clean)** — K 32, interp, stop +4 bits, comp **off**: lowest noise
  floor; no boost (boost trades noise for bandwidth).
* **fast capture** — K 16, interp, time 8 s, comp auto @ spend 0.65: coarse grid
  fills fast, short stop, modest boost.

interp everywhere: it gives the most bits (each fine bin averages every frame),
and its only low-pass is near the *raw* Nyquist (250 MHz) — far above the ≤175 MHz
the compensation targets. drizzle only wins for tones right at Nyquist and costs
bits (less boost budget), so it's not a preset.

## Validation

* **Unit** (`superres_comp.test.cjs`, run by `TestSuperresCompJS`): FFT/IFFT
  round-trip; an attenuated multi-tone restored to unity in-band (40 MHz
  0.42→0.96); gap/DC preservation; resample path; adaptive monotonicity
  (+2.3b→76 MHz, +5b→149 MHz) with boost within budget.
* **Real stacked HW** (`comp_hwvalidate.mjs`): stacked 60 real FPGA-square
  frames with the production `superres.js`, applied `srCompensate` — the raw
  harmonic envelope droops to −8.7 dB by 55 MHz; compensated is **flat within
  ±2 dB from 5–55 MHz** then rolls (+7.6 dB peak boost).
* **Live UI e2e** (`comp_live.mjs`, `comp_presets.mjs`): arm → view → toggle in
  the deployed browser on the real scope — envelope flattens (35 MHz lifted
  +5.8 dB), readout correct, **auto on a +3.0 bit stack recovers 88 MHz vs
  fixed-70's 61 MHz**, all presets set every knob, zero console errors.

## Deferred

* Device/LCD port (Go): the standalone-scope super-res review (`render_superres.go`)
  draws Y-T/FFT/X-Y but not the compensation; a Go port of `srCompensate` + a
  panel toggle would give LCD parity. The web is the primary super-res analysis
  surface, so this is a follow-up.
* Phase (group-delay) correction — v1 is magnitude-only (recovers most of the
  edge on a minimum-phase front end).
* A guided cal routine (drive the harmonic comb from the UI, re-fit `SRCOMP_HCAL`
  for a different probe/interconnect) instead of the baked bench cal.
