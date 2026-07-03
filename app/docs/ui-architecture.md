# UI/UX architecture — ADR-001 + phased plan

_Produced by a research→design→judge→synthesize workflow (audit of web + LCD UIs, UX research, e2e-gap audit → 3 competing architectures → 3 independent judges → synthesis). All three judges ranked "Structured Vanilla" first, Preact last/second. Grounded against the real codebase._

## Decision

ADR-001 — Keep zero JS framework and no build step; adopt "Structured Vanilla": a ~200-line home-grown substrate (design tokens + primitive CSS classes + one reactive store + a DOM helper + a panel/CONTROLS registry) over the existing native-ES-module, canvas-2D web UI, with a Go-generated single-source palette shared by web and the on-device LCD. Pattern: unidirectional store for chrome + an imperative canvas draw loop that bypasses the store; declarative panel/control registries for extensibility; strict same-origin CSP. Framework proposals (Preact+htm) are REJECTED for this Go-firmware repo above their token phase — their only real win (DOM diffing) barely touches a canvas-dominated hot path, and they add dependency-audit surface and bus-factor to a firmware tree.

## Rationale

All three independent judges ranked Structured Vanilla first and Preact last (or second) for identical reasons, and the codebase confirms the premises. (1) The UI is one ~1308-line hand-authored ui.html served verbatim by a tiny Go binary via per-file //go:embed (web.go serves `/`, `/peaks.js`, `/decode.js`); there is no package.json/bundler anywhere, so `go build` is the ONLY build step — a feature we must not lose. (2) Everything is same-origin off the device's own HTTP server with no reachable CDN, so native ESM is inherently CSP-safe and lets us ADD a strict CSP that is impossible today (the page relies on one inline <script> and 66 inline style= attributes — both counted). (3) The existing e2e harness (decode/fft/deepmem `_browser.mjs` Playwright drivers behind Go wrappers that shell to node against httptest+fakeScope and self-skip when node is absent, plus peaks/decode node ESM tests) drives the REAL ui.html — a load-bearing property vanilla keeps with no build shim. (4) The audit's #1 structural risk is verified real: the trigger concept is RED (`--trig #e8604c`) on web but GREEN (`colTrig rgb(64,255,64)`) on the LCD, and on web `--trig`==`--stop`==`#e8604c` collapses two meanings. A framework does nothing for that; a Go-generated shared palette fixes it permanently. We take B's best DRY ideas (CONTROLS registry, pure-Go golden parity test, frame-as-plain-ref performance rule, Okabe-Ito values, LCD softkey polish) and C's safety discipline (card-by-card migration, don't-touch-display contract, selector aliases, isolated amber-recolor) as grafts onto A — capturing the wins without the runtime. Hard guardrail: store+dom+registry stays ≤~200 lines; crossing ~300 is the signal that the framework trade would have been better.

## Design system

THREE LAYERS, single source of truth = a tokens.json consumed by a `//go:generate` Go program (node-free) emitting BOTH assets/tokens.css (`:root` custom properties) AND render_palette_gen.go (the LCD `col*` vars replacing render.go L11-21). A pure-Go golden test asserts the two agree color-for-color, so web/LCD can never re-diverge.

LAYER 1 — TOKENS (exact set).
Surfaces/elevation (fixes 1px-border-only depth): --bg #0b0f14 (body); --surface-1 #11161d (panel); --surface-2 #161c24 (card); --surface-3 #1c2430 (raised/hover); --screen #05080c (graticule); --well #141c26 (inputs/lists); --edge #29333f; --edge2 #26384a (input border); --text #d6dde4; --dim #7c8894; --focus #4fb8e8 (keyboard ring).
Signal/semantic (Okabe-Ito anchored, ONE hue per concept, identical web+LCD): --c1 #f5d90a (yellow); --c2 #35c8e8 (sky) [CVD-safe pair kept]; --math #b98cff (purple, X-Y/math); --cursor #e6ecf2 (near-white, MOVED off the warm band, drawn DOTTED); --trigger #f2a63b (AMBER — reserved, shared by both surfaces, resolves red-vs-green divergence AND the --trig/--stop collision); --run #3fb950 (live/ok); --stop #d55e00 (vermillion — stop/error/stale, now that amber owns trigger); --warn #f5a24c.
Decode-kind categorical (Okabe-Ito): --dec-start #3fb950, --dec-stop #d55e00, --dec-addr #f2a63b, --dec-data #35c8e8, --dec-ack #3fb950, --dec-nak #d55e00, --dec-parity #f5a24c; plus --grid #182430 (replaces the two JS `#182430` literals). JS DECCOL/COMPCOLS/grid are derived at boot from getComputedStyle (the page ALREADY does `getComputedStyle(document.body)` at L338) — no hex literal survives in JS. COMPCOLS becomes luminance/opacity/dash variants of the PARENT channel token, not fresh hues.
Scale tokens: space --sp-1 6px/--sp-2 10px/--sp-3 16px/--sp-4 24px; type --ff-mono, --fs-sm 11px/--fs-base 13px/--fs-lg 15px + tabular-nums; radius --r-1 4px/--r-2 6px; z --z-drawer/--z-overlay/--z-toast; size --tap 44px.
Hex→token inventory to capture (from the real file, so nothing is missed): #141c26→--well, #26384a→--edge2, #05080c→--screen, #232c36→--surface-3(ctrl bg), #3d4a58→hover, the .on triple #2d4a63/#4a7196/#cfe6ff→--on-bg/--on-edge/--on-fg, #182430→--grid, #7c8894→--dim.

LAYER 2 — PRIMITIVE CLASSES (base.css, replace all 66 inline styles).
Buttons: .btn, .btn--sm, .btn--ghost (the 6 clr/copy/clear minis), .btn--run, .btn--stop; toggles use `[aria-pressed=true]` (accessible successor to today's `.on`, styled from --on-bg/--on-edge/--on-fg). Inputs: .field (unifies .rolesel/.dsel/.panel2 number inputs — keep .dsel/.rolesel as ALIASES so no querySelector breaks), .select, .slider (range, accent-color: --trigger), .well. Structure: .panel, .card, .card__title (flex header: justify-content space-between), .card-actions (replaces float:right header clusters), .table (meas), .hint (dim label spans), .mono. Feedback: .badge, .chip, .chip--live/.chip--stale (print the WORD), .toast. Layout utils: .row/.stack/.grp/.spread + width utils .w-xs/.w-sm/.w-md; keep existing .cc1/.cc2 for channel tints. State via ATTRIBUTES not ad-hoc classes: `[data-state=live|stale]` for liveness; `:focus-visible{outline:2px var(--focus)}`. Coarse-pointer sizing via `@media(pointer:coarse){.btn,.slider thumb→--tap}`.
Redundant CVD coding baked into primitives (the two HIGH audit findings): RUN carries `RUN ▶`/`STOP ■` glyph+word; LCD liveness strip prints `LIVE`/`STALE`; cursor lines dotted (shape as an independent second channel); decode-error tokens hatched/outlined on the canvas.

LAYER 3 — HELPERS/STORE/REGISTRY (see architecture): dom.js + store.js + the panel & CONTROLS registries are what let panels bind declaratively to these token-driven primitives through one consistent update path.

## Architecture

MODULE LAYOUT (assets/, served by ONE http.FileServer(http.FS(sub)) replacing the per-file HandleFunc pattern; adding a module needs no new Go code):
- tokens.css (GENERATED), base.css — no per-element styling in HTML.
- dom.js — ~40 lines: h(tag,props,...kids), on(el,ev,fn), bindText/bindClass/bindAttr(el,sel) subscribing to the store.
- store.js — the ONE reactive store: createStore(initial) → {get, update(patch|fn), subscribe(fn), select(sel,fn)}; shallow-merge + rAF-COALESCED notify (a status+frame arriving in one tick = one redraw).
- net.js — getStatus()/getFrame()/setControl(control,value)/injectPanel(); owns the TWO poll loops (frame @90ms, status @1000ms) and is the ONLY writer of server-derived state; preserves the existing document.activeElement guards so a control being dragged is never stomped.
- controls.js — the CONTROLS registry (graft from B): one row {id, apiControl, kind, key, aria, tip} generates the footer widget + keymap entry + title= tooltip + ?-overlay line + aria-pressed binding.
- keymap.js — built from controls.js; ignores keys while input/textarea/select is focused.
- render/{scope,nav,fft,xy,decode,cursors,markers}.js — canvas renderers refactored to PURE render(ctx,state) with NO module-global reads; xForCol/homeWindow/winRange move here with math unchanged.
- panels/{view,measure,cursors,fftC1,fftC2,decode,decoded,captured,trigchip}.js — one module per dock card calling registerPanel.
- main.js — bootstrap: build store, import panels (self-register), mount dock from registry, wire footer from CONTROLS, start polls, subscribe compositor.
web.go: add `assets embed.FS` + FileServer; on hRoot set `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'` (img-src data: + blob preserved for PNG toDataURL / CSV Blob export).

PANEL CONVENTION (extensibility MUST): Panel interface {id, title, order, mount(root)->el, select(state)->slice, render(el,slice), visible(state)->bool}. registerPanel(p) pushes to a registry; mountDock() builds each .card ONCE; a single store subscription toggles the `hidden` ATTRIBUTE from visible(state) — deliberately, because UA `[hidden]{display:none}` keeps getComputedStyle(card).display reporting 'none', so the fft_browser display assertions survive (graft from C/judge3) — and calls render only when select(state) differs. Adding a panel = write panels/foo.js + import it; adding a footer control = one CONTROLS row. NO edits to setMode/applyStatus/redraw.

STATE STORE (one tree, replaces scattered globals st/frame/view/dcfg/cur/fftCh/frozen/userZoomed/lvlDragging/offDragging/lastSeq/reqCols):
{
  conn:'connecting'|'live'|'stale'|'wedged',
  status:{...},                 // /api/status verbatim, written by the 1s poll + optimistic actions
  view:{mode,persist,cursors,c1,c2,win:{a,b},userZoomed,lastSig,reqCols},
  cursors:{t1,t2,v1,v2,drag},
  fft:{1:{peaks,sel,selIdx},2:{...},maxPeaks},
  decode:{...dcfg...},
  ui:{frozen,dragging:{lvl,off},toast}
}
Writers: EXACTLY the two poll loops write server slices; user actions write optimistically then setControl(), reconciled next poll under the activeElement guards.

RENDER LOOP (documented performance invariant, graft from B): the /api/frame reply is held as a PLAIN REF, NOT a store-subscribed slice — the 90ms/~11fps draw bypasses the reactive notify entirely and the compositor iterates the pure render(ctx,state) list imperatively from that ref. Only status @1s and user actions flow through the rAF-coalesced store to drive chrome. Measurement/FFT/decode cards that reflect frame data subscribe to a COARSE few-Hz derived selector (or are nudged by an explicit imperative call from the draw loop), never per-pixel. This kills the current "forgot to call updateMeas/updateCursors/updateDecodeResults/updateCaptureList/updateFFTLists/redraw in the right order" bug class while guaranteeing the store never re-runs panel renders at frame rate.

## Layout / information architecture

RIGHT RAIL (#dock): card order = View → Measure → Cursors → FFT C1 → FFT C2 → Decode → Decoded → Captured, each a Gestalt unit via .card + an elevation tier (surface-2 card on surface-1 panel on bg) instead of 1px borders alone. Progressive disclosure extended: FFT cards only in FFT mode, X-Y/decode cards mode-gated (already partly done), and decode advanced fields (baud/bits/parity/CPOL/CPHA/threshold/watch) hidden behind a gear toggle showing only proto+roles+auto-threshold by default. NEW header trigger-state chip mirrors the LCD state machine (AUTO/NORM/SNGL/WAIT/T'D/STOP from render.go L446-461) so a user glancing between bench LCD and browser sees identical state. #hstat gets aria-live=polite, #wedged role=alert.

CONTROL BAR (footer): reorder into WORKFLOW order left→right mirroring the LCD HUD reading order — Acquire (RUN/SINGLE) → Trigger (mode/type/slope/source/ets/level/pos) → Horizontal (time/div) → Vertical (C1 vdiv+offset, C2 vdiv+offset) → Acq/Mem (acq/acqn/memdepth). Gestalt separation: `.grp{flex-wrap:nowrap}` so a group never splits mid-wrap; inter-group gap 16px clearly > intra-group 6px; optional subtle panel bg per group. Fitts: pin RUN/STOP + SINGLE to the bottom-left corner (effectively infinite target) and never let them wrap away; @media(pointer:coarse) raises targets to --tap 44px and enlarges range thumbs. Progressive disclosure: show acqn only for AVERAGE/ERES, de-emphasize memdepth/ETS until the timebase makes them relevant; qualifier panel2 stays revealed only for PULSE/SLOPE/VIDEO.

RESPONSIVE: @media(max-width:760px) collapse #dock to a toggleable bottom-sheet drawer and turn the footer into an overflow-x:auto toolbar in its own container, guaranteeing the body never scrolls sideways and the canvas keeps ≥60% viewport; dense decode/capture blocks scroll inside their cards.

ON-DEVICE LCD (render.go, the OTHER surface, generated from the SAME tokens so it cannot drift): trigger recolored to amber matching web; liveness strip (L320-329) prints LIVE/STALE text in addition to color and shifts stale toward vermillion; softkey menu (drawMenu L339) F1-F5 slot y-centers set to the PHYSICAL bezel-button centers rather than an even i/5 division, and the selected slot becomes a FILLED inverted highlight bar (not the current 1px colTrig outline). Primary readouts (per-channel Vpp/freq + the trigger-state word, drawHUD L435/L462) rendered at scale 2 for arm's-length legibility; focused channel gets a 2px stroke with the other channel dimmed for figure/ground beyond hue.

## Test strategy (acceptance suite)

A REAL acceptance suite, device-independent via httptest+fakeScope, plus one live smoke.

SHARED PAGE-OBJECT HARNESS: a single assets-agnostic `scope_po.mjs` exporting `openScope(url) -> {page, po}`. `po` is a page-object over the real ui.html with intent methods — run()/single(), setMode(m), toggle(id), setControl(id,val), dragLevel(dv), readStatusLine(), readTrigChip(), readMeas(), readDecode(), pressKey(k), export(kind), isCardVisible(id) [asserts getComputedStyle(card).display], hasFocusRing(), announced(region) [reads aria-live text]. Every `<path>_browser.mjs` imports it, so a DOM-id change is fixed in ONE place. Each Go wrapper spins `httptest.NewServer(New(fakeScope{frameGen:…}).Handler())` with a path-appropriate synthetic frame (the existing i2cWave/fakeScope pattern), shells to `node <path>_browser.mjs $URL`, self-skips on missing node/Playwright, and fails unless the driver prints ALL PASS.

ONE SPEC PER USER PATH (the acceptance set):
1. boot_liveness — load, status announces connecting→live, canvas role=img label updates from Vpp/freq/trig, STOPPED overlay on stop.
2. acquire — RUN toggles running, SINGLE arms, trigger-state chip mirrors AUTO/NORM/SNGL/WAIT/T'D/STOP.
3. trigger — mode/type/slope/source/level/pos; qualifier panels reveal for PULSE/SLOPE/VIDEO; a level drag is not stomped by the next poll.
4. vertical_horizontal — C1/C2 vdiv+offset, time/div, C1/C2 enable.
5. cursors — enable, drag t1/t2/v1/v2, readouts, dotted style.
6. fft — FFT mode, peak select/clear, component overlays attributed to parent channel (extends fft_browser.mjs).
7. xy — X-Y mode renders, math hue.
8. decode — protocol select, roles, transcript+byte count, Copy, advanced gear disclosure (extends decode_browser.mjs).
9. deepmem_nav — memdepth change, navigator wheel-zoom + reset, window mapping (extends deepmem_browser.mjs).
10. export — PNG data: + CSV blob succeed UNDER the strict CSP.
11. keyboard — Space/R/S/1/2/C/F/X/Y/P/[ ]/, ./arrows/Esc produce the SAME store transition as clicking; suppressed while an input is focused; ? overlay lists them.
12. a11y — aria-live status/wedged announced, aria-pressed mirrors toggles, labels on every range/bare-select, :focus-visible reachable by Tab.
13. responsive — at 700px the dock collapses to a drawer, footer scrolls horizontally, body never scrolls sideways, canvas ≥60% viewport, targets ≥44px.
14. panel_visibility — cards show/hide by mode via [hidden]; getComputedStyle display flips 'none'↔'block'.

ALWAYS-RUN GO GUARDRAILS (pure Go, NOT gated behind the optional node harness — critical because the browser suite self-skips on the device/CI): token-parity golden (tokens.css == render_palette_gen.go color-for-color); no-inline-style + no-inline-script lint over ui.html (xfail in P0, flips green by P2); CSP-header-present-and-non-empty; extend render_test.go with an LCD golden covering amber trigger + LIVE/STALE strip + bezel-aligned softkeys. Store unit tests (node ESM, store.js is pure): subscribe/notify, rAF coalescing, selector diffing, optimistic-then-reconcile.

GATE: `go test ./...` green on host before every phase ships; a manual :8080 device smoke (-stage; /tmp is read-only per project memory) for anything touching render or polling — boot, first-paint, one live frame, fps/latency unchanged, PNG/CSV export.

## Phased plan

### Phase 0 — Acceptance harness + always-run guardrails (safety net; task #19)

**Goal:** Lock CURRENT behavior with a real acceptance suite BEFORE any refactor, so every later phase has a regression net that does not depend on the optional node harness.

**Steps:**
- Write the shared scope_po.mjs page-object and refactor decode/fft/deepmem drivers onto it.
- Add one _browser.mjs spec + Go wrapper (httptest+fakeScope) per user path capturing TODAY's behavior: boot_liveness, acquire, trigger, vertical_horizontal, cursors, xy, export, panel_visibility (extending the 3 existing drivers for decode/fft/deepmem).
- Add PURE-GO guardrail tests that ALWAYS run: token-parity, no-inline-style/no-inline-script lint (xfail/skip), CSP-header-present (xfail) — plus store unit-test scaffold.
- Wire a documented :8080 live-smoke checklist (-stage).

**E2E gate:** Full per-path acceptance suite (14 specs) green against the UNMODIFIED ui.html; Go guardrail stubs present (xfail where the invariant isn't met yet).

**Validation:** `go test ./...` green on host; existing decode/fft/deepmem stay green through the harness refactor; nothing user-visible changes. Commit.

### Phase 1 — Tokens single source of truth + LCD reconciliation (tasks #20a, #22a)

**Goal:** Fix the verified #1 bug (web-red vs LCD-green trigger; --trig==--stop) by generating both palettes from one tokens.json, so they can never re-diverge.

**Steps:**
- Add tokens.json + a //go:generate Go program emitting assets/tokens.css and render_palette_gen.go (replacing render.go L11-21).
- Split --trigger (amber #f2a63b) from --stop (vermillion #d55e00); reconcile LCD colTrig to amber; move --cursor to near-white.
- Extend the existing getComputedStyle pattern so JS DECCOL/COMPCOLS/grid derive from CSS vars — remove the #182430 and COMPCOLS/DECCOL hex literals.
- Flip the token-parity golden test green; update the LCD render golden.
- Isolate the intentional amber recolor as its OWN screenshot-gated commit (color is not pixel-asserted).

**E2E gate:** Token-parity golden + LCD render golden green; the Phase-0 path suite green except the intended trigger-color change; a before/after :8080 screenshot for the amber step.

**Validation:** `go test ./...` green; on-device LCD render unchanged except color. Commit per step (generator, then isolated recolor).

### Phase 2 — Primitive CSS, kill inline styles, external module, strict CSP (task #20b)

**Goal:** Replace all 66 inline styles with token-driven primitives, move logic to an external module, and add a strict CSP — with zero behavior change.

**Steps:**
- Add base.css primitives; make .card h3 a flex header; keep .dsel/.rolesel/.panel2 as ALIASES.
- Migrate inline style=→class CARD BY CARD (View→Measure→Cursors→FFT→Decode→Decoded→Captured→footer), running `go test ./app/internal/web/...` after EACH card, one commit per card.
- LOAD-BEARING CONTRACT: do NOT touch any style="display:none" hook or JS .style.display write; grep-diff asserts no id/$()/getComputedStyle target is renamed.
- Move the inline <script> to an external app.js module; migrate serving to assets embed.FS + FileServer; add the strict CSP header.
- Flip no-inline-style/no-inline-script + CSP-present tests green.

**E2E gate:** no-inline lint + CSP-present tests green; the full path suite (esp. export path: PNG data:/CSV blob) green UNDER the strict CSP; per-card commits keep each step revertible.

**Validation:** Pixel-identical UI; `go test ./...` green after each card; on-device export verified at :8080. Commit per card + one for the CSP/module move.

### Phase 3 — Store + dom + net + pure renderers + perf guardrail (task #21a)

**Goal:** Route everything through one store on one update path (removing the forgot-to-call-updateX bug class) while keeping the 90ms canvas loop off the reactive path.

**Steps:**
- Introduce store.js/dom.js/net.js (cap store+dom+registry ≤~200 lines); route the two poll loops + all actions through the store; preserve the activeElement guards.
- Document + enforce the invariant: frame is a PLAIN ref, the 90ms draw bypasses reactive notify; cards subscribe to a coarse few-Hz selector.
- Convert renderers to pure render(ctx,state) ONE AT A TIME, with deepmem/fft/decode specs as non-negotiable pins per renderer (xForCol/homeWindow/winRange math unchanged).
- Replace scattered updateMeas/updateCursors/updateDecodeResults/updateCaptureList/updateFFTLists/redraw with subscriptions + rAF-coalesced redraw.
- Add store node unit tests (subscribe/notify, coalescing, selector diff, optimistic-reconcile).

**E2E gate:** Store unit tests + the full path suite green; behavior identical through the new single update path.

**Validation:** `go test ./...` green; on-device fps/latency unchanged at :8080 (measure first-paint + steady fps). Commit per renderer, then the store wiring.

### Phase 4 — Panel registry + CONTROLS registry + accessibility (task #21b)

**Goal:** Make adding a panel/control a one-file change and make the app screen-reader/keyboard navigable — proving the convention with the trigger-state chip.

**Steps:**
- Convert each dock card to a Panel module via registerPanel; mountDock builds cards once; visible(state) toggles the [hidden] ATTRIBUTE (getComputedStyle display stays 'none').
- Fold the footer into the CONTROLS registry (id/apiControl/kind/key/aria/tip).
- Add the a11y layer: aria-live=polite on #hstat, role=alert on #wedged, canvas role=img with a label rebuilt from live Vpp/freq/trig, visually-hidden <label for> on every range/bare-select, aria-pressed mirroring toggles, :focus-visible ring.
- Add the header trigger-state chip mirroring the LCD AUTO/NORM/SNGL/WAIT/T'D/STOP as the first use of the convention.

**E2E gate:** New a11y spec (announcements, aria-pressed, labels, role=img, tab-focus ring) + panel_visibility spec (cards toggle via [hidden], display flips) green.

**Validation:** `go test ./...` green; adding/removing a panel or control demonstrably a one-file change. Commit registry + a11y separately.

### Phase 5 — UX polish on the new substrate (remaining audit items; task #22b)

**Goal:** Land the now-cheap medium/low audit items, each independently shippable with its own targeted spec.

**Steps:**
- Keyboard shortcuts + ? overlay + title tooltips generated from CONTROLS (Space/R run-stop, S single, A trig-mode, 1/2 channels, C cursors, F FFT, X/Y modes, P persist, [ ]/, . divs, arrows for focused adjust, Esc goHome); suppress while inputs focused.
- @media(pointer:coarse) 44px targets; footer Gestalt grouping (nowrap groups, larger inter-group gap, workflow order); responsive dock→drawer + overflow-x footer below 760px.
- Move cursors off the warm band to near-white DOTTED; add surface elevation tiers; toasts for PNG/CSV/copy; redundant coding (RUN ▶/STOP ■, LIVE/STALE, hatched decode-error tokens).
- LCD render.go polish: bezel-aligned F1-F5 softkey y-centers, filled inverted selected softkey, scale-2 primary readouts, focused-channel 2px stroke.

**E2E gate:** keyboard spec + responsive spec (drawer, no horizontal body scroll, ≥44px, canvas ≥60%) green; a targeted spec per polish item; LCD golden updated for softkey/readout changes.

**Validation:** `go test ./...` green; on-device LCD visual check at the bench. Each item its own commit/PR.

## Risks

- Phase 3 is the real risk: converting renderers to pure render(ctx,state) can subtly change draw order or the xForCol/homeWindow/winRange zoom mapping. Mitigate HARD — refactor ONE renderer at a time with deepmem/fft/decode as pins, and NEVER merge Phase 3 with the CSS sweep.
- The browser e2e suite SELF-SKIPS when node/Playwright is absent (likely on the device and possibly CI). Therefore the new load-bearing invariants — token-parity, no-inline lint, CSP-present — MUST be pure-Go tests that always run, never gated behind the node harness. Non-negotiable.
- Scope creep into a mini-framework forfeits the whole rationale for beating Preact. Hard cap: store+dom+registry ≤~200 lines; crossing ~300 is the signal to reconsider the framework trade.
- Migrating a display:none hook or a JS .style.display write to a class during the CSS sweep would flip a getComputedStyle(card).display e2e assertion. Mitigate: leave ALL display toggles inline/JS through Phase 2; only Phase 4 changes visibility to the [hidden] attribute (UA [hidden]{display:none} keeps getComputedStyle 'none').
- N small same-origin ESM requests hit the device's tiny HTTP server on first paint. Mitigate: keep module count low, set long Cache-Control on the assets FileServer, and MEASURE on-device first-paint/fps at :8080 before shipping Phase 3 — not just a note.
- Strict CSP could break blob/data-URI export (toDataURL PNG, CSV Blob). Mitigate: allow img-src 'self' data:, keep the export path spec, and verify it under CSP on device before shipping Phase 2.
- The token generator can be bypassed by hand-editing render_palette_gen.go — the pure-Go golden parity test fails the build, making drift unmergeable.

## Open questions

- Physical F1-F5 bezel-button y-centers are not in the repo — Phase 5's softkey alignment needs a one-time measurement on the actual SDS1102CML+ panel (or a photo) to set drawMenu slot centers exactly.
- CSP style-src: do we require 'self' only (all CSS externalized) or must we keep a nonce/'unsafe-inline' for any dynamically-set style (e.g. canvas sizing)? Confirm no runtime inline style survives Phase 2 so 'self'-only holds.
- Is a Node toolchain guaranteed on CI, or only on the developer host? If CI lacks node, the acceptance suite skips there — confirm whether we need a CI job that installs Playwright so paths are gated, not just locally validated.
- Should the header trigger-state chip poll its own state or derive it from the existing /api/status fields (running/norm/single/trigd)? Confirm /api/status already carries enough to mirror the LCD state machine without a new endpoint.
- Deep-memory + navigator interaction with the frame-as-plain-ref rule: confirm the navigator's window (view.win) belongs in the store (chrome) while the frame stays a ref, so nav drag stays reactive without routing frames through the store.

## Live-smoke checklist (on-device, after render/serving/poll changes)

Device at 192.168.1.209 (web UI :8080). `/tmp` is read-only → deploy with
`otactl … -stage /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging update-app dist/app-arm`.

1. Page boots at http://192.168.1.209:8080/ (first paint < ~1 s; note the go:embed asset count/size delta).
2. One live frame renders; steady fps ≈ 19–20 unchanged; no console errors.
3. RUN/STOP + SINGLE work; trigger-state matches the LCD.
4. Mode switch Y-T / X-Y / FFT; per-channel FFT boxes select + overlay.
5. Decode a live bus (autodetect) still works; watch/capture still captures.
6. PNG + CSV export succeed (under the strict CSP once Phase 2 lands).
7. Deep-memory navigator zoom/pan; memdepth change.
8. LCD: trigger amber, LIVE/STALE strip, softkey menu render intact.

## Progress log

- **Phase 0 (safety net) — DONE.** Shared page-object harness `scope_po.mjs`
  (findPlaywright + openScope + intent methods); the 3 legacy drivers
  (decode/fft/deepmem) deduped onto it; new `acceptance_browser.mjs` covering the
  previously-uncovered paths (boot/liveness, acquire, trigger, vertical/horizontal,
  cursors, view modes + panel visibility, export) via httptest+fakeScope; pure-Go
  always-run guardrails `ui_lint_test.go` (inline-style budget ratchet=66,
  inline-script budget=1, CSP-present skipped until Phase 2). All green.
- **Phase 1 (single-source palette) — DONE.** `tokens.json` is the source of
  truth; `gen_tokens.go` (`go generate ./internal/web`) emits `tokens.css` (web
  `:root`, served + linked) and `../lcd/palette_gen.go` (LCD `col*`). The trigger
  is now AMBER (#f2a63b) on BOTH surfaces (was web-red / LCD-green), split from
  `--stop` (vermillion #d55e00); `--cursor` moved to near-white. `TestPaletteParity`
  (pure-Go, always-run) makes web↔LCD drift + stale generation unmergeable.
  Device-verified: LCD trigger amber, web tokens resolve from the linked stylesheet.
