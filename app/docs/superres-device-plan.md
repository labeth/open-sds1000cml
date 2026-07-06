# Super-res: device port + reference-locked stacking — plan

Goal: bring super-res "stack & crunch" to the standalone scope (LCD + front
panel) at parity with the web, add **reference-locked stacking** and **target-
based stopping**, and make the two surfaces produce the **same result**. Built
through: plan → design → review → implement → review → verify → validate.

## 1. Requirements (consolidated)

R1. **Device super-res** — port the web stack-and-crunch to run on the instrument
    (Go), reachable from the front panel, rendered on the LCD.
R2. **Stop targets, order bit → stacks → time**, on BOTH web and device. Set e.g.
    "+4 bits" and it stops when reached. **bits and stacks are crunch-rate
    independent** — the device (which crunches slower than the engine produces
    frames) reaches the *same* stack as the web. Time is the wall-clock fallback.
R3. **Reference-locked stacking** — SINGLE until you catch a frame you like, then
    arm super-res using THAT frame as the alignment reference; it is *locked*
    (the auto re-seed must not drift off it).
R4. **Smart matching** — stack only frames whose *pattern* matches the reference;
    reject the rest. On `burst → slow → burst`, single'd on a burst, it stacks
    bursts and rejects the slow parts.
R5. **Translation tolerance** — a matching frame shifted "some distance" from the
    reference's trigger position must still stack (align by pattern/timing, not
    exact trigger). Reject only genuine non-matches.
R6. **Triggers** — device: **UTILITY** button; web: the super-res card **ARM**
    button. UTILITY toggles on/off like SINGLE (press = start, press = cancel).
R7. **Device review UX** — while active the softkeys map to super-res functions;
    a live status line (frames / rejected / +bits / target) with the cancelable
    pattern; the **ADJUST (intensity) knob-push** soft-closes/reopens the stack
    view (like the web "view" toggle); the review shows the super-resolved trace
    usable with the existing zoom/cursors. No on-LCD peak-pick/model-fit (v1).

## 2. Current architecture (what exists)

- **Web engine** `internal/web/superres.js` (707 lines, 13 fns, pure JS, unit-
  tested via `superres_node_test.go` + browser tests). Pipeline per frame
  (`srFeed`): clip-check → coarse trigger-edge align (`base`) → NCC sub-sample
  align+score (`srAlign`) → **lucky** gate (adaptive threshold, floor 0.6) →
  per-channel gain/offset drift-normalize (`srGainOffset`) → linear-weight
  drizzle onto an n·K fine grid (`srAccumCh`). Reference = first non-flat frame;
  an auto **re-seed** drops it if >70% reject. `srResult` computes MEASURED
  `bitsGained` from the odd/even half-stack σ, plus frames/rejected/fill/rate.
- **Web loop** `app.js` `sr{}` + `srIngest`/`srLoop`: a dedicated `?raw=1`
  long-poll feeds `srFeed`; stack-view toggle re-renders the crunched trace.
- **Device**: nothing yet. The Go trace/HUD/menu render (`internal/lcd`) and the
  panel matrix/knob handling (`internal/panel`) are where the device UX lands.

## 3. Design

### 3.1 Shared semantics (device == host)

- **Stop targets**: `{mode: bits|stacks|time, target}`. Check after each stacked
  frame: `bits` → measured bitsGained ≥ target; `stacks` → frames ≥ target;
  `time` → elapsed ≥ target. bits/stacks depend only on the stack, not wall time,
  so both surfaces converge to the same stack. Web ✅ drafted; device mirrors it.
- **Reference lock** (`userRef`): `srSeedRef(st, sig1, sig2, edgeX)` adopts a
  specific frame as the reference and sets `userRef`, which disables the re-seed.
  Web ✅ guard drafted; add `srSeedRef` + the Go equivalent.
- **Match gate**: keep the NCC lucky gate. For a locked reference the floor (0.6)
  is the operative cut — a burst-vs-slow NCC sits well below it → rejected.
- **Translation tolerance**: `srAlign` already *searches* for the best shift
  (`base` coarse trigger diff + `±maxLag` NCC refine). Widen `maxLag` for the
  locked-reference path (8 → ~64) so a burst offset from the trigger is found and
  aligned before scoring; genuine non-matches still score below the floor.

### 3.2 Web (finish reference-lock; stop-targets done)

1. `superres.js`: add `srSeedRef` (+ export); re-seed guarded by `!userRef` ✅.
2. `app.js`: ARM while STOPPED (frozen on a SINGLE) → seed+lock that frame, then
   RUN and stack matches at the wider `maxLag`; ARM while running → current
   auto-adopt (keeps the deep-drain re-seed recovery). Reset `lastBits`.
3. `ui.html`: ARM tooltip — "freeze a frame (SINGLE) first to lock it as the
   match reference". Stop control ✅ (bits/stacks/time selector + target).

### 3.3 Device (the port)

1. **`internal/superres/` (new Go pkg)** — port the core: `New(n,K)`, `Feed`,
   `Align`, `Accum`, `Result`(frames/rejected/bitsGained/fill), `SeedRef`,
   clip/gain-offset/mean-std helpers. Skip model-fit/peaks (not in v1 UX).
   Algorithm-faithful to superres.js; a shared golden-vector test asserts the Go
   stack matches the JS stack bit-for-bit on the same frames.
2. **Panel** (`internal/panel`): wire **UTILITY** (`0x66:3`) as a super-res
   toggle (arm from the frozen/last frame as the locked reference; press again =
   cancel — like SINGLE). While active: a `pgSuperres` softkey page (Ch / grid ×K
   / kernel / **Stop** mode+target / Dither / Reset). Wire the **ADJUST knob-push**
   (`0x65:1`) to toggle the stack-review view. Feed frames to the stacker from the
   render loop's frame source (reuse `frameFn`).
3. **LCD** (`internal/lcd`): a super-res status HUD (frames · rejected · +bits ·
   target · fill) reusing the autoset cancelable-banner idiom; a **stack-review**
   view (ViewMode-style) that draws the crunched fine-grid trace via `drawTrace`,
   working with the existing horizontal zoom + cursors.
4. **Stop targets + reference** identical to web semantics (§3.1).

### 3.4 Consistency guarantee

Same K, same kernel, same stop target (bits or stacks), same reference frame →
same stack, independent of crunch rate. Validate by stacking the *same* recorded
frames through both the JS and Go engines and comparing `bitsGained` + the
crunched samples within tolerance (golden-vector test).

## 4. Process / phases (tracked as tasks)

1. **Plan** (this doc) → review.
2. **Design review** — adversarial pass on §3 (algorithm fidelity, matching
   correctness on burst/slow/burst, device UX on 5 softkeys, consistency).
3. **Web reference-lock** — implement, browser-test (burst-vs-slow fixture:
   assert slow frames rejected, bursts stacked, target-stop fires), commit.
4. **Go superres pkg** — port + golden-vector parity test vs JS, unit tests.
5. **Device UX** — UTILITY toggle, pgSuperres page, ADJUST-push view, status +
   stack-review render; deploy.
6. **Verify on hardware** — FPGA burst signal; SINGLE on a burst, UTILITY, watch
   frames accepted/rejected + bits climb to target; review the stacked trace;
   screenshot each step. Cross-check device bits vs web bits on the same signal.
7. **Review + fix** loop until clean; update the parity matrix.

## 5. Risks / open questions

- **Go DSP fidelity** — subtle NCC/drizzle differences vs JS. Mitigation: golden-
  vector parity test, port line-by-line.
- **Translation range** — a burst far from the trigger needs a wide search; a
  full-record FFT cross-correlation is the fallback if `maxLag≈64` is too narrow.
  Start with widened `maxLag`; measure on the real burst signal.
- **ARM-from-frozen ordering** — must seed the reference from the frozen frame
  BEFORE resuming RUN, or the first live frame becomes the reference.
- **Device CPU** — stacking 20k-sample frames × K on the ARM per frame; measure,
  and stride/throttle the crunch if it can't keep the 50 ms LCD tick (it need not
  — stacking slower than the engine is explicitly fine; that's why bits/stacks
  targets exist).
