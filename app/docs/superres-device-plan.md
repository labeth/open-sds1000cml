# Super-res: device port + reference-locked stacking — plan (rev 2)

Goal: bring super-res "stack & crunch" to the standalone scope (LCD + panel) at
parity with the web, add **reference-locked stacking** and **target-based
stopping**, make the two surfaces **converge to the same stack**. Process:
plan → design → **review** (done, rev 2 folds it in) → implement → review → verify
→ validate. All file:line refs under `app/`.

## 1. Requirements

R1 Device super-res (Go), front-panel reachable, LCD-rendered.
R2 Stop targets, order **bit → stacks → time**, both surfaces. bits/stacks are
   crunch-rate independent; time is wall-clock.
R3 Reference-locked stacking — SINGLE a frame you like, arm super-res using THAT
   frame as the alignment reference, locked (no re-seed off it).
R4 Smart matching — stack only pattern-matching frames; on burst→slow→burst
   single'd on a burst, stack bursts, reject slow.
R5 Translation tolerance — a matching burst shifted "some distance" from the
   reference trigger still stacks (align by pattern, reject genuine non-matches).
R6 Triggers — device UTILITY; web ARM. UTILITY toggles like SINGLE.
R7 Device UX — softkeys map to super-res while active; live status; ADJUST
   (intensity) knob-push toggles the stack-review view; review usable with the
   existing zoom/cursors. No on-LCD peak-pick/model-fit (v1).

## 2. Reality (from the design review)

The web engine `superres.js` exists and is tested, BUT the three things this
design leans on are **not built**: reference-lock (`srSeedRef` is a comment,
`userRef` is read but never set), a **discriminative** match gate, and real
translation tolerance. Default behaviour today is the *inverse* of R3/R4: on a
slow-majority signal the auto re-seed (>70% reject) drops a burst reference and
re-adopts the slow majority. Build these first.

## 3. Design (corrected)

### 3.0 The parity contract (was overstated — this is the core correction)

- **`stacks` = STRICT parity.** `st.frames` is an exact counter. Same *ordered*
  frame list + same *seeded* reference + dither off + single-threaded fixed feed
  order → bit-identical integer intermediates, accept/reject set, and the
  `float32` `mean` array. This is what the golden-vector test asserts, and the
  contractually consistent stop target.
- **`bits` = CONVERGENCE only (±tolerance).** `bitsGained` is stride-dependent,
  the odd/even half-stack (`st.frames & 1`) makes one flipped accept cascade the
  A/B parity of the whole tail, and `log2` is not correctly-rounded across V8 vs
  Go. So bits is "reach ~the same level," pinned to the `interp` kernel, never
  bit-exact. Live device-vs-host cross-checks compare **levels with tolerance**,
  never sample equality.
- **`time` is wall-clock, excluded from the guarantee** (by design).
- Live device and host consume *independent frame subsets* (each skips to the
  newest published frame at its own rate) — so live results **converge**, they
  are not identical. Only a replay of the *same ordered frames* is deterministic.

### 3.1 Reference-lock (build it)

`srSeedRef(st, sig1, sig2, edgeX)`: validate the frozen frame (reject flat
`hi−lo<12`, reject `srClipped`), set `c[ch].ref`, `refEdgeX`/`edgeX`,
**`userRef=true`**. The re-seed guard (`!userRef`, already added) then never
drops it. ARM-from-frozen seeds **before** RUN resumes. Crunch-rate independence
holds **only** for a locked `userRef`.

### 3.2 Discriminative match gate (build it — plain NCC fails R4)

A plain NCC over the ±2048 window centred on the trigger edge is dominated by
the *shared* triggering transition (burst and slow both fired on it): measured
false-accepts 0.955–0.996. Fix: **remove the shared low-frequency content before
scoring** — detrend (subtract a fitted low-order trend, or the aligned reference-
edge template) / high-pass both traces, then NCC on the residual, so only the
burst's *distinguishing* content is scored. Use a **fixed** cut (0.6–0.7) when
`userRef` (the adaptive median−3·MAD ratchet over-rejects genuine weak/translated
bursts once locked). Score at the **sub-sample-refined** alignment, not the
integer-lag peak. Fixture: burst→slow→burst locked on a burst → slow rejected.

### 3.3 Translation tolerance (build it — do NOT widen maxLag)

Widening `maxLag` 8→64 silently misaligns: period-aliasing (aligns 14 periods
off at score 0.955) and partial-overlap smear (capped at the maxLag rail). Keep
the **local** NCC refine small (≤ period/2) around a trustworthy `base`, prefer
the argmax nearest k=0, reject ambiguous peaks (2nd-best within ε of best). Make
a **full-record normalized matched-filter / xcorr that returns a location + peak
sharpness** the PRIMARY locate step (align to the found location, never a
maxLag-capped lag; reject cleanly beyond ±X% of record; this also handles the
`edgeX=−1` no-trigger case). Fixture proves the max tolerated shift.

### 3.4 Stop targets (fix bits determinism on the web first)

`bits` currently checks `sr.lastBits`, refreshed only inside the 500 ms-throttled
`srUpdateStats` → the stop runs on wall-clock cadence and overshoots by a
rate-dependent frame count. Fix: compute a **deterministic stop-bits every
stacked frame** with a *fixed* reduction (fixed stride + gates, identical both
surfaces); keep the throttled strided `srResult` for display only. `stacks`/
`time` check per-frame. Mirror exactly in Go.

### 3.5 Device integration

- **Frame feed = RAW acquisition**, mirroring `rawBinMsg`: `f.C1[:f.Valid]`,
  `f.C2[:f.Valid]`, `cols=f.Valid`, `edgeX=f.EdgeX`, `sampleS=f.SampleS`, same
  `Vpc`/`OffV` — NOT the drawn/windowed trace. **Dedup on engine `Seq`.**
- **Crunch OFF the render lock.** Inside `WithFrame` copy raw `C1/C2[:Valid]` +
  `EdgeX/SampleS/Seq` under the arena lock, release, hand off to the stacker's
  **own goroutine** (bounded) — never align+accum in the 50 ms render tick or it
  freezes the LCD and stalls the producer. Keep the panel goroutine free so
  UTILITY-cancel stays responsive mid-crunch.
- **Geometry-change stop**: mirror the web meta-change stop (cols/sampleS/vpc);
  **auto-cancel SR on AUTO / tdiv / vdiv / memdepth** changes (they rescale the
  frame mid-stack). Document the acquisition state machine
  `{idle, single-frozen, SR-active, review} × {RUN/STOP, SINGLE, UTILITY}`; guard
  every shadow write under the controller mutex (this surface produced the
  autoset LED race).
- **Memory**: `srNew` ≈ 13 MB @K=8, 26 MB @K=16 for n=20480. Cap K on device;
  use float32 accumulators and/or **align-channel-only stacking** (documented
  parity tradeoff) to fit the ARM budget.

### 3.6 Device UX (decouple SR-active from the menu page)

- **UTILITY** (`0x66:3`) toggles an **SR-active flag** (arm-from-frozen-reference
  / cancel), which drives `ledUtility` — NOT `menuPage`. Re-pressing UTILITY
  re-opens `pgSuperres` **without disarming**; **long-press = cancel/reset**. So
  the user can visit `pgHoriz`/`pgCursor` (zoom/cursors) and return without
  losing the stack (or drive zoom/cursors from knobs while SR active).
- **`pgSuperres` = exactly 5 slots**: Ch / grid×K / Stop-mode / Stop-target /
  Reset. Add it to `pageSlots()` and `pushLEDs()`. For the Stop slots,
  distinguish the **F-press** path (cycle mode) from **ADJUST-rotate** (edit the
  highlighted numeric target) — every existing page treats press==knob, so this
  needs bespoke handling. Per-mode ladders (bits +0.5; stacks decades; time s).
- **ADJUST knob-push** (`0x65:1`) toggles the review view.
- **Cuts (v1):** dither (fights the offset DAC live → breaks the guarantee),
  kernel selection + cubic (ship `interp` only — bits needs it), K as a live
  control (fixed quality setting), model-fit/peaks. These frees the softkeys and
  shrink the parity surface.
- **Review render**: a dedicated **gap-aware, float-code, min/max decimate-to-
  320px** renderer for the ~1.3M-value `float32` stack with `-1` gap sentinels
  (NOT `drawTrace`, which takes `[]uint8`). Define precedence vs X-Y/FFT ViewMode.
  ASCII-only HUD: `frames/rej +Nb`, `gridxK`.

## 4. Go↔JS numeric parity (the golden-vector test must survive these)

- `Math.round` → `jsRound(x)=math.Floor(x+0.5)` everywhere (`base`, `shiftInt`
  are routinely negative x.5); mirror `|0` as truncate-toward-zero.
- **FMA**: Go fuses `a*b+c` on amd64/arm64 but **NOT on `GOARCH=arm`** (the scope
  target). node==arm, amd64-CI diverges. **Materialize each product into a named
  `float64` local** (structurally suppress fusion) AND/OR run the parity test
  on-arm; a green amd64 CI is not on-target proof.
- **Single-threaded, fixed feed order, single accumulator per bin** — no
  goroutine fan-out over frames/bins (float add is non-associative).
- `srAlign` three-way branch (edges|parabola|int) via `srCrossings`/`srMidSwing`,
  ±1.5 window, rising-then-falling concat, `floor(len/4)` — port verbatim; assert
  `method` per frame in the golden vectors.
- float32 `ref` + drift-normalized frame; float64 accum/scores; float32 `mean`;
  `t=float64(b)/K` via precomputed `invK`; low-side clamp `v<0?0`; `-1` gap
  sentinel. Port the integer gates (`hi−lo<12`, `srClipped` `>6 && <253`, `+2`,
  `nlo*200>n`), `med()`=`s[len>>1]` (upper-middle, not averaged), `1.4826/0.05/
  0.6`, `>=10` warm-up, the seed `scores.push(1)` — exactly.
- Assert **bit-exact** on integer intermediates + accept/reject set + `mean`;
  tolerance only on `log2`-derived scalars (`sqrt` IS correctly-rounded → sigmas
  match).

## 5. Phases (risk pulled forward)

1. Plan + review — **done**.
2. **Spikes (cheap, de-risk before the big build):**
   a. **On-device bcode spike** — press physical UTILITY + push ADJUST, log the
      raw matrix, confirm clean `1→0` edges and no spurious edge during ADJUST
      *rotation*; add `nameCode` cases (`utility`, `adjustpush`) for headless
      drive; have a fallback binding if `0x65:1` is unreliable.
   b. **Web burst/slow/burst + shifted-burst fixture** (node) — locked burst
      reference: assert slow frames rejected, bursts stacked, max tolerated shift.
   c. **ARM micro-benchmark** of Feed+Accum over one 20480×K frame → set K before
      it's wired into the UX.
3. **Web**: `srSeedRef` + discriminative gate + full-record locate + deterministic
   per-frame stop-bits + ARM-from-frozen. Pass fixture (2b). Commit.
4. **Go `internal/superres`**: port core (New/SeedRef/Locate/Align/Accum/Result +
   helpers; skip fit/peaks) with the §4 parity rules; **golden-vector parity test**
   (same ordered frames JS↔Go → bit-exact integer + accept/reject + mean;
   `stacks` exact; bits within tol). Materialize-products for on-arm parity.
5. **Device UX** (§3.5/3.6): raw off-lock feed, SR state machine, UTILITY toggle,
   pgSuperres (5 slots), ADJUST-push view, status HUD + float min/max review
   renderer, geometry-change auto-cancel. Deploy.
6. **Verify/validate on hardware**: FPGA burst signal — SINGLE on a burst,
   UTILITY, watch accepted/rejected + bits climb to target, review the stacked
   trace, screenshot each. Cross-check device bits vs web **levels within
   tolerance** (NOT sample equality). Update the parity matrix.
7. Review + fix loop until clean.

## 6. Open risk

Full-record locate cost per frame on the ARM (matched filter over 20480) — may
need an FFT xcorr or a coarse-stride search; measure in spike 2c. If translation
range can't be met affordably, cap R5 to ±X% and say so.
