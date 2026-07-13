# Changelog

## Unreleased

### Added
- **Sigrok-oracle decoder test suite.** The protocol decoders are now
  cross-validated against libsigrokdecode (via sigrok-cli) on identical
  synthetic waveforms — 65 subtests across UART, I2C, SPI, CAN (+FD base),
  FlexRay and USB-LS, covering the classic torture cases: fractional
  samples/bit everywhere, parity/frame/CRC corruption with error POSITIONS
  pinned, ring-glitched and adversarial auto-baud, break, clock stretching,
  SDA glitches, all four SPI modes, gap thresholds straddled mid-word, RTR,
  recessive ACK, stuff maximizers, DLC>8, sync/startup flags, DTS tails,
  NAK/STALL, zero-length DATA, corrupted PID, keep-alive trains. Every
  payload is anchored against the generated truth as well as sigrok. A
  dedicated `oracle-decode` CI lane installs sigrok-cli and hard-fails on
  skips. The exercise also pinned four libsigrokdecode PD bugs (can: phantom
  RTR data bytes, stub CRC check, FD length table on classic DLC>8; i2c:
  phantom address after an SDA glitch) where the repo decoders are correct.
  See `app/docs/decode-oracle.md`.
- **Sigrok export from the web UI.** Three new one-click exports next to
  PNG/CSV: **`.sr`** (the srzip session format — opens directly in PulseView
  and sigrok-cli, carrying channel names, the true sample rate, and calibrated
  float32 volts), **VCD** (analog channels as `real` vars, for GTKWave and
  recent libsigrok), and **float32 WAV** (calibrated volts, importable by
  sigrok and any DSP tool). Pure client-side encoders (`sigrok_export.js`,
  zero deps, node-tested against the byte layouts libsigrok's readers parse);
  same contract as the CSV export — the frame on screen, live, frozen, a
  capture under review, or a superres view. See `app/docs/sigrok-export.md`.

### Fixed
- **CAN auto-baud no longer halves the rate on low-transition payloads.**
  Found by the sigrok oracle: a legal frame whose payload (0x33/0xCC
  patterns) leaves one single-bit gap in a sea of 2-bit ones made the old
  10th-percentile inference seed on the 2-bit cluster — auto decode then
  hallucinated a clean-looking garbage frame with `ok=true`. `inferCANspb`
  (Go + JS twin) now uses the UART cluster walk with integer-multiple
  validation; the exact killer vector is pinned red/green in the oracle.
- **USB-LS auto-bitrate survives an idle bus.** A realistic keep-alive
  train (2-bit SE0 every millisecond) flooded the old percentile estimator
  until the bit period collapsed and decode hard-failed on a capture sigrok
  reads fine. Same cluster-walk fix (Go + JS twin), with >16-bit gaps
  excluded as non-evidence; pinned in the oracle with a 60-keep-alive train.
- **FlexRay frames with a corrupted frame CRC-24 no longer decode as clean.**
  Found by the sigrok oracle: the decoder verified only the 11-bit header
  CRC, so payload/trailer corruption was invisible. Both the Go decoder and
  its JS twin now seal header+payload with the CRC-24 (poly 0x5D6DCB, init
  0xFEDCBA), flag `!FCRC` frames, and gate `OK` on both CRCs.
- **Exported time axes are no longer 2× compressed on 1–200 ns/div.** Those
  bands capture at 2 ns/sample but size the display window at the 1 ns nominal
  (spec 04 §6), so the frame's `col_span_s` understates the real span 2× and
  the CSV export inherited the error. The frame reply now carries `dt_s` — the
  true capture time per served point — and the CSV and sigrok exports use it;
  the display keeps the spec'd nominal.

## v0.0.5 — 2026-07-12

Web display and super-resolution gating: the browser now works from the **full
raw record** in one trigger-locked coordinate, and the super-res gate stacks
exactly the region you mark. Validated on hardware.

### Fixed
- **The web trace is trigger-locked at every zoom.** The display serves the full
  raw record (no server-side re-centred window); the client re-centres on the
  trigger edge each frame. The raw record is **not phase-stable** — the trigger
  lands on a different sample each acquisition — so at home the client already
  re-homed onto the edge, but once you **zoomed** the window froze and the
  wandering edge slid the trace off-centre ("jumps around, frame-locked not
  trigger-locked"). Zooming now **follows the edge** (shifts the frozen window by
  the frame-to-frame edge delta), so the trigger point stays put at any zoom.
  Measured on the live signal, edge-aligned frames match to **0.6–1.0 codes RMS**.
- **The super-resolution gate stacks exactly what you mark.** Dragging the gate
  markers, then arming, used to stack a different region (or looked auto-gated) —
  the markers were stored as *absolute* record fractions while the display
  re-centres on an edge that swings ~1 screen per frame, so a marker drifted off
  its feature and the armed gate never matched. Markers are now **edge-relative**
  (an offset from the trigger edge), the same coordinate as the trace and the
  raw-fed stacker seed, so what you drag is what gets stacked.

### Changed
- The deep web frame is served **verbatim** (full record, real trigger position)
  instead of a re-centred fixed-length window; centring is a client display
  transform. The dead server-side `deepWindow` re-center path is removed.


Triggering is now reliable and WYSIWYG across every timebase and voltage level.
Every fix was validated on hardware against a controllable FPGA-driven cal signal
(amplitude / offset / frequency shaped per detent), unit-tested, and adversarially
reviewed. See `app/docs/trigcal-notes.md` for the characterization method.

### Fixed
- **Trigger-level calibration corrected to a single global constant**
  `code = 31437 − 911·V` (volts at the BNC). The previous per-detent "×25 law"
  was a measurement artifact (rounded sine peaks + offset-DAC contamination in
  the old method); clean characterization — firing-band **centre** vs the
  calibrated Vmean across 0.05–1.0 V/div, spanning both attenuator tiers — shows
  codes-per-display-volt is constant. The set trigger voltage now lands where the
  trace actually crosses it: worst-case WYSIWYG level error drops **0.041 V →
  0.012 V**, at the vertical-cal noise floor.
- **Small on-screen signals now lock.** A real signal under ~1.6 divisions (a
  2.4 Vpp signal is peak-to-peak 8–32 codes at 2–10 V/div) never locked in NORM —
  the edge was found but the lock gate required raw ptp ≥ 40. Raw ptp cannot tell
  a small real signal from a noisy flat rail (the rail's ptp can EXCEED the
  signal's), so the gate now tests **coherence**: `ptp ≥ k·noiseFloor`, where
  noiseFloor is a period-independent median-second-difference estimate that also
  survives aliased (many-period) screens. Flat / quiet screens still HOLD.
- **The rising⇄falling slope flip is gone.** On a slow ramp the discrimination
  record dithers through the level in ADC noise; the old fixed-count confirmation
  occasionally accepted a down-blip on a *rising* ramp as a "falling" edge (~10 %
  of frames at ~1-period-on-screen bands). Confirmation is now **noise-scaled
  hysteresis** — a crossing must transit ±k·noiseFloor without bouncing back —
  which is scale-free and rejects the dither. Zero flips across the decimated
  ladder, both slopes.
- **Channel offset is folded into the trigger discrimination level and the
  on-screen markers**, so the trigger point and its vertical/horizontal markers
  line up with the trace at any offset.

### Changed
- **Envelope timebases (5–50 ms/div) now show a triggered waveform.** They were
  untriggered by design — a repetitive signal displayed a filled rail-to-rail
  min/max band (random-phase acquisitions). The band now trigger-anchors the
  drained record and captures a centring margin, so the display is edge-stable
  across the full width; it falls back to the min/max envelope only when a signal
  genuinely cannot be triggered (aliased, flat, or the level off the signal). The
  counter-limited slowest band reports its honest displayed s/div. Roll
  (≥100 ms/div) remains an untriggered live scroll (standard).

## v0.0.3 — 2026-07-11

### Fixed
- **The "ACQ STUCK — POWER-CYCLE" state is gone, root-caused end to end.** A
  native-fast half-record (valid ≈ 10,240/20,480 with a frozen dead tail) is
  the FSM's normal **untriggered parked state** — the pre-trigger half fills
  free-running and only a comparator edge runs the post-trigger half. The
  "permanently stuck" scope was a trigger level parked outside the signal's
  effective comparator band, **persisted in scope-settings.json** (hence
  surviving restarts, band changes and even power-cycles); any real level write
  cures it instantly. The engine now publishes untriggered captures as honest
  free-run views of their live half (never dead-tail garbage, on any surface),
  re-captures and DEGRADED/stuck telemetry gate on a fired trigger, and the
  badge no longer claims a power-cycle is needed.
- **Two decimated wait-gate bugs found by an on-hardware fuzz campaign**
  (baseline 28 published half-records per 15 min of adversarial control churn →
  0 after the fixes): the fill counter **resets when the comparator fires** on
  decimated bands, so completion gates latched during the pre-trigger free-run
  no longer satisfy a triggered record (the post-trigger record time is waited
  from the edge); and a fired trigger now **extends the wait deadline** so an
  edge landing late in the budget is not halted mid post-fill. Both are
  band-scoped: on native-fast the counter reads 0 post-edge and the reset would
  pin every frame at the full wait budget.
- **AUTOSET verifies its trigger level closed-loop.** The volts→DAC fit is
  exact only at 1–2 V/div; when the computed level does not fire, autoset now
  scans the DAC range and centres the level on the band that empirically fires
  (12/12 triggered from adversarial states; previously 0/12).

### Changed
- **Native frame rate restored: ~19–20 fps across the real-time bands (was
  4–5 in the degraded state), CPU 51% unloaded (was ~96% at the start of the
  campaign).** The untriggered-era workarounds — 40 ms maturation floor, 2 ms
  fill-extra and halt-settle busy-spins, settle busy-wait, manual GC — are all
  retired after hardware A/B showed each unnecessary or harmful once captures
  gate on trigger evidence (native-fast waits are now uniform ~6 ms).
- Acquisition instrumentation records the publish decision, NORM/AUTO and
  timebase per capture, so a flagged half-record is attributable in the field.

### Validated
- Certification sweep on the real unit: all 31 control verbs, every on-LCD
  view (X-Y/FFT/Bode/spectrogram/decode/super-res/measure/cursors/math/refs/
  persistence), SCPI paths and burst endpoints — 69 steps clean; a 5-minute
  all-features-armed soak at 100% CPU saturation — 21/21 clean; ~2,900
  seeded operator-fuzz iterations + four focused acquisition hunts — zero
  capture-integrity failures on the final build.

### Fixed (pre-campaign, since v0.0.2)
- **Vertical recalibration to the reverse-engineered vendor laws** (offset,
  gain/display scale, trigger) — from an RE pass on the vendor firmware (spec
  commit f6c3eb7) plus bench validation:
  - **Offset** is a single input-referred DAC with a **fixed** slope in volts:
    `code = clamp(zero − 100·V)` — **100 DAC codes per input-volt, not scaled by
    V/div** (`offsetCodesPerVolt`). The coarse attenuator tiers only the *range*
    via a per-tier clamp: **±1.6 V** on the sensitive ×1 ranges (≤200 mV/div),
    **±40 V** on the attenuated ×25 ranges (≥500 mV/div). Bench-validated on the
    unit: one −1.6 V command writes a fixed **160 DAC codes at every range**, and
    the trace moves **input-referred** — **0.80 / 1.59 / 3.22 div at 2 V / 1 V /
    500 mV/div** (1:1 with the signal, because the per-range analog gain already
    scales offset authority ∝1/VDIV). The RE's scaled `100·(V/VDIV)` form
    (spec commit c98d5d1) would give 0.4/1.6/6.5 div (16:1) and is disproven; the
    per-range gain double-counts the 1/VDIV if the code is scaled too. **The
    offset DAC is dead on the sensitive ×1 ranges** — a large DC saturates the
    front-end before the ADC, so autoset coarsens off-center/railed channels down
    to an attenuated range where offset works. (Law history: an RE pass gave
    50 codes/div → corrected to 100 → then scaled→fixed on the bench; supersedes
    an earlier stepped-K/lever attempt that topped out near ±0.15 V.)
  - **Display/gain scale is 25 codes/div, not 32** (the offset & trigger DACs run
    at 2× = 50/div). This was the ~22% voltage under-read — a 3 V cal wave read
    2.31 V. The render grid is now 200 codes = 8 divisions (the ADC's 256 codes
    span 10.24 div; the trace clips at the graticule edge beyond ±4 div).
  - **Trigger DAC left unchanged:** the RE "50 codes/div" claim is wrong for the
    trigger — bench-measured ~938 codes/V (the comparator fired over ~2100 DAC
    codes across a 3 Vpp wave, 14× the 50/div prediction). Only the on-screen
    level-marker mapping follows the 25-codes/div grid.
  - Specs 05/06/10 corrected (spec 06 §5.2 carries a bench-correction flagging the
    fixed-per-volt result vs the RE's scaled "per-division" trap); the
    `SCOPE_OFFSET_K`/`SCOPE_OFFSET_LEVER` env knobs are removed.
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
