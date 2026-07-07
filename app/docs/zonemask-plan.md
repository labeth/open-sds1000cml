# Zone trigger + mask testing — design (rev2)

Two high-end capture-qualification features in one package, both living in the
ENGINE's publish path (unlike superres/eye, which are display-side consumers):
every captured frame is tested at the full acquisition rate (~10–19/s), whether
or not it is published.

- **Zone trigger** (Keysight-flagship UX): draw zones on the display; a frame
  QUALIFIES by intersecting (or avoiding) them. Qualifying frames publish;
  others hold — a graphical software trigger the HW comparator cannot express.
- **Mask testing**: a per-column min/max envelope mask; every captured frame is
  tested, pass/fail counted, failures captured into a ring with timestamps,
  optional stop-on-fail. The golden mask comes from an N-frame envelope,
  dilated by ±time/±voltage tolerances.

rev2 records the design-review outcome (multi-lens + adversarial verify) and
the breaker findings; every change below marked (review) or (breaker) is
implemented and regression-locked by a test.

## 0. Preconditions (what the features honestly require)

- **Phase lock** (review): mask testing and zone dt-coordinates are only
  meaningful when the trigger anchors ONE unique point of the repetition.
  A mid-level crossing on a multi-edge pattern (UART, PRBS) anchors a random
  edge and the "golden envelope" degenerates to rail-to-rail — statistically
  void, every frame passes. The user contract is the same as a real scope's
  mask wizard: trigger on-signal, level on the unique feature, holdoff over
  the repetition's inner edges. The 50-family breaker models exactly this
  (level = mid + amp/2 with a quiet-time precondition).
- **DC coupling for the drawn/built channel** (review): AC/GND coupling is a
  display-only transform on this clone; zone/mask test RAW capture codes.
  The web UI refuses to draw zones or build a mask while the channel is not
  DC-coupled (what you see would not be what is tested).
- **Detectability floor** (breaker): an amplitude mask can only promise to
  catch a defect that ESCAPES the dilated envelope by more than the noise.
  Defects that stay inside the local swing (a dropout-to-mid where the
  signal crosses mid; a freeze inside an oscillating region opened up by
  horizontal dilation) are invisible to ANY per-column envelope — that is
  physics, not a bug. The breaker's referee encodes this rule.

## 1. Engine (Go, app/internal/engine — single implementation, no JS mirror)

### 1.1 Representation
- Zones: up to 4 rects in EDGE-ANCHORED time × code space:
  `Zone{DtLoS, DtHiS, CodeLo, CodeHi, Avoid, Ch}`.
  Time is SECONDS RELATIVE TO THE TRIGGER EDGE (portable across bands); the
  test maps to sample columns via EdgeX + SampleS per frame.
- Mask: `Mask{Lo, Hi []uint8, WinCols, Ch, TdivS, SampleS, VdivKey, OffKey}` —
  per-display-column bounds, edge-anchored like the display window
  (window() semantics: column j reads raw sample left+j,
  left = round(edgeX − posFrac·win)); a frame fails a column if any sample
  falls outside [Lo, Hi].
- **Mask identity** (review): WinCols alone is NOT an identity — every
  ≥200 µs/div band clamps to the same 2048 columns while seconds/column
  differ 10×. SetMask stamps install-time TdivS + SampleS + the channel's
  V/div bits + offset DAC shadow; maskEval skips (and counts MaskSkip) on
  any mismatch. A silently-skipping test wears the success signature of a
  clean run, so skips are COUNTED and surfaced, never silent (review).

### 1.2 Test point
In oneFrame after discrimination and the publish-policy switch:
- Zone eval only on locked frames; per zone, scan its mapped column range for
  any sample inside the code band → intersect/avoid verdict; AND across zones.
- Mask eval on locked frames in MaskTest/MaskStopFail; column-major scan,
  early-out on first violation; record the first violating
  (column, code, raw sample) for the UI.
- **Dead-tail cap** (review): deep drains go dead past validDepth (repeated
  last sample); maskEval caps the testable range at liveDepth so the verdict
  never judges garbage.
- **AcqAverage skip** (review): AVERAGE rewrites the published samples AFTER
  this test point — the verdict would judge data the user never sees.
  Counted as MaskSkip.
- Budget: simple byte compares over ≤2048 columns — well under 1 ms on the
  ARM926 (measured in the unit suite on-host; on-target it is noise next to
  the drain).

### 1.3 Policy + state
- Zone trigger mode: qualifying frames publish; non-qualifying HOLD. NORM
  holds strictly; AUTO publishes an unqualified liveness frame every
  zoneFallback=60 holds (screen stays alive). Applies to ALL publishes —
  lock=false AUTO fallbacks are also throttled, or zones would be bypassed
  by exactly the frames that can't qualify (review).
- Mask test mode: MaskPass/MaskFail/MaskSkip counters, stop-on-fail latch,
  failure RING (last 8 failing frames: C1/C2 copies + seq + monotonic
  timestamp + first-violation point + WinCols/EdgeX/SampleS geometry
  snapshot — the ring must be renderable after the band changes, review).
- **Stop-on-fail force-publishes the failing frame** (review): without it the
  display freezes on the last PASSING frame and the user stares at a clean
  trace wondering what failed.
- Both OFF by default; independent (zone qualify + mask count can run
  together).

### 1.4 BuildMaskFromEnvelope morphology
±tolCols horizontal then ±tolCodes vertical dilation of the accumulated
[lo,hi] envelope. Two rules with teeth:
- **Unobserved columns normalize to [0,255] BEFORE dilation** (breaker): the
  per-frame edge position moves the window; fringe columns may never be
  observed and still carry the accumulator's initial lo>hi. Left alone,
  dilation turns them into inverted-garbage bounds that fail every sample
  (found as a clean-frame false positive: env [247,8]).
- **Tolerance floor** (breaker): tolCols must cover trigger-point jitter
  (noise ÷ slope at the trigger point, in samples); tolCodes ≥ ~3× the noise
  σ plus sub-sample phase error on the steepest slope. The 50-family breaker
  runs tolCols=5/tolCodes=8 against noise pp ≤ 5 codes; the web UI defaults
  match and the fields are user-editable.

## 2. API + web UI

- Dedicated endpoints (review — zones/mask payloads don't fit the scalar
  /api/set contract): `POST /api/zones` (JSON array, clamped, ≤4),
  `POST /api/mask` {lo,hi,win,ch} (empty clears),
  `GET /api/maskfail?i=k` (ring entry k: c1/c2 + geometry + violation).
  /api/set keeps the scalar verbs: zonemode, maskmode, maskclear.
- `/api/status`: zone_mode, zone_count, mask_mode, mask_pass/fail/skip,
  mask_ring, mask_stopped, win_cols.
- Web card "ZONE / MASK": armed draw-zone drag (pointer priority OVER
  box-zoom while armed, review), zone list with intersect/avoid toggles;
  build-mask-from-N-live-frames (raw frame.bin envelope accumulation
  client-side), ±t/±V dilation fields, test/stop-on-fail select, pass/fail
  meter, failure gallery (click → frozen frame with the violation marked).
- Zones render edge-anchored (they track the trigger); mask renders as a
  shaded envelope band when the full window is displayed.
- Coupling guard: see §0.

## 3. Device (LCD + panel)

- LCD (lcd/render.go): mask envelope boundary lines + zone rectangles mapped
  with the SAME window mapping as drawTrace (LCD == web == engine test
  point); the band only renders while the frame's window geometry matches
  the mask (zoom/band change break column alignment — the engine skips those
  frames too). Pass/fail meter in the top-centre gap, flashing on failures,
  plus the panel's build status line.
- Panel: a MASK page reached by re-pressing ACQUIRE past REF (acquire → ref →
  mask cycle — all three acquisition-side surfaces on one physical button).
  Slots: Mode (Off/Test/Stop-F) / Build (from N live frames on the TRIGGER
  SOURCE channel) / Frames (16/32/64/128) / Tol preset (±3s/6V, ±5s/8V,
  ±8s/12V, ±12s/20V) / Reset (shows live pass/fail). The build enforces the
  same DC-coupling guard as the web and force-runs acquisition. Zones are
  web-only in v1 (drawing rects with knobs is a poor fit; the mask flow is
  the device-native use).

## 4. FPGA validation source (counted truth) — redesigned per review

The rev1 idea (PRBS with every-Vth-bit violations) is statistically void:
without phase lock the golden envelope on PRBS is rail-to-rail (§0). The
validation source must be phase-locked BY CONSTRUCTION:

`maskviol.v` + `build-maskviol.sh <period_us> <violate_every> [ext_us] [pw_us]`:
- Base: a pulse train with exactly ONE rising edge per period (the scope
  triggers on it — a unique phase anchor by construction).
- Every Vth period the pulse ends `ext_us` LATE — a width violation that
  adds NO edges. C2 carries a marker (high exactly during the extension)
  as an independent oracle for zone tests.
- **Why width-extension, not an added glitch pulse** (HW finding): the first
  cut fired a full-swing glitch in the quiet region — but that glitch is
  itself a rising edge, and a share of scope triggers anchored IT instead of
  the main pulse (the software holdoff does not change which record edge the
  discriminator picks). Those frames are out of phase across the whole
  window and the mask CORRECTLY failed them at the first phase-disagreement
  column (dt mod P ∈ [0, ~tolCols] — the pulse-top start), but the counted
  truth then convolves with a trigger-anchor mix whose share beats against
  the frame cadence (non-stationary over minutes). A width extension keeps
  the source single-edged: every trigger anchors the one main edge and the
  statistics stay exactly binomial.
- Counted truth: a frame catches a violation iff a late-ending period's
  pulse-end offset (m·P + PW, for the periods m visible in the ±win/2
  window) belongs to the Vth period → p = slots/V exactly (slots = count of
  visible pulse-end positions; 4 slots at the validation band). Catch rate
  over N tested frames is Binomial(N, p); assert within ±4σ. Every caught
  frame's FailSample must map to the pulse-end offset PW..PW+ext ± the
  tolCols dilation shadow (deterministic position check).
- V=0 → clean source, false-positive floor: minutes at 0 fails.

## 5. Validation ladder

1. Engine unit tests (done, green): zones intersect/avoid/AND/contradiction;
   maskEval pass/fail/counters/ring/stop/identity-skip; envelope morphology
   incl. unobserved-column normalization.
2. **50-family breaker** (done, green): 10 shapes × 5 defect classes with
   ground truth; golden mask from 32 clean frames; 16 clean frames must all
   pass (false-positive check), 12 violated frames must all fail with the
   failure localized to the injected sample (+physics referee, §0); zones
   placed on live features must qualify, on measured-empty bands must not.
   Root causes found and fixed by this breaker: unobserved-column
   normalization (§1.4); anchor phase-lock requirement (§0).
3. Web API tests (done, green): endpoint clamping, mask upload/clear/reject,
   ring serving, set verbs.
4. Zone trigger live (HW, done): intersect zone inside the extension window
   (NORM strict) → 97/97 published frames carry the extension in-zone,
   publish rate 20/s → 2.16/s; avoid zone → 329/329 published frames
   extension-free. Zero unqualified leaks either way.
5. Mask HW counted truth (§4, done — maskviol @400 µs, V=7, ext 12 µs,
   band 500 µs/div, window 2048×800 ns = ±819 µs, 4 slots → p = 4/7):
   - clean floor: 3444 frames, 0 false positives, 0 skips (3 min);
   - catch: p̂ = 0.5621 over n = 2350 vs 0.5714 (|Δ| < 4σ = 0.041) — PASS;
     a parallel direct oracle on the raw published stream measured
     p_oracle = 0.5592 ≈ p̂, i.e. the engine catches exactly what is
     physically present (the small deficit vs 4/7 is free-running phase
     sampling skew, per-slot occupancy 0.130–0.157 vs 1/7);
   - position: 8/8 ring entries at dt mod P = 104.4–105.2 µs (pulse end
     100 µs + tolCols dilation shadow) — deterministic position PASS;
   - stop-on-fail: latched in 1.0 s, the force-published frozen frame IS the
     ring's failing frame (seq match), resume releases the latch.
6. Device parity (done): LCD renders the envelope + zone rects + live
   pass/fail meter (screenshot-verified against the same counters as the
   web); panel MASK page (ACQUIRE ×3) drives mode/build/tol/reset live.

## 6. Resolved review findings (record)

| finding | resolution |
|---|---|
| WinCols is not a mask identity across ≥200µs bands | TdivS/SampleS/Vdiv/Off identity stamps + MaskSkip counter |
| silent skip wears a clean run's signature | MaskSkip counted + surfaced in status |
| dead-tail samples judged as data | liveDepth cap in maskEval |
| zone bypass via lock=false AUTO publishes | throttle ALL publishes in zone mode |
| stop-on-fail freezes on stale passing frame | force-publish the failing frame |
| AcqAverage rewrites data post-test | counted skip |
| PRBS validation statistically void without phase lock | §4 redesigned source; breaker anchors level+holdoff |
| zone drawing vs box-zoom drag conflict | armed-drag consumes pointer first |
| ring unrenderable after band change | geometry snapshot per ring entry |
| /api/set scalar contract doesn't fit payloads | dedicated /api/zones, /api/mask, /api/maskfail |
| AC/GND coupling display transform vs raw-code test | web refuses draw/build unless DC (§0) |
| inverted-garbage bounds on unobserved columns | normalize to [0,255] pre-dilation (§1.4, breaker) |
| amplitude-mask detectability limit | physics referee documented (§0) |
