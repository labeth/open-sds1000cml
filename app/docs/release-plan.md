# Release plan — top 10 features + release readiness

_From a gap-analysis workflow (audit web/engine/LCD + real-scope feature research + release-readiness) → synthesis. Goal: releasable open-source v1, device + web._

## Top 10 features

### 1. Probe attenuation (×1/×10/×100)  
`both` · effort M

**Why:** The single biggest correctness gate: with a standard ×10 probe every voltage on the whole instrument reads 10× low (cursors, measurements, trigger level, ground/offset). No credible bench scope ships without it, and it is cheap because it is a pure readout multiplier. SCPI already shadows it (scpi.go attn[ch]) but never applies it — the plumbing half-exists.

**Design:** Add probe factor as a display/readout scalar applied at every volts boundary; never change the analog gain or the code→volts electrical mapping. In analog/frontend.go add probe [2]float64 (default 1), SetProbe(ch,x) and ProbeFactor(ch). In web.go vertScales(): multiply vpc[ch] and off[ch] by probe — because measure() and the frame vpc/off flow from vertScales, all 8 measurements, CSV, cursors and XY inherit it for free. Scale the trigger-level readout by the trig source's probe where engine.TrigLevelVolts is surfaced in hStatus. Device: multiply hud.C1VdivV/C2VdivV by probe in the panel HUD snapshot and append a small '10×' tag in render.go drawHUD; expose probe on a new pgChan softkey page in panel/menu.go (shared with coupling/BWL below). Web: per-channel probe <select> in ui.html vertical card, app.js sends {control:'probe1'/'probe2'}, web.go hSet routes to fe.SetProbe and hStatus returns probe1/probe2. Wire scpi.go 'ATTN' (already parsed into attn[ch]) through to fe.SetProbe.

**Files:** internal/analog/frontend.go, internal/web/web.go, internal/web/ui.html, internal/web/app.js, internal/lcd/render.go, internal/panel/menu.go, internal/scpi/scpi.go, internal/analog/frontend_test.go, internal/web/web_test.go, internal/lcd/render_test.go

### 2. AC/DC/GND input coupling  
`both` · effort M

**Why:** An analog-front-end staple; you cannot view ripple/small AC riding on a DC bias without AC coupling, and GND gives a zero reference. All three audits rank it high, the relay bits are already documented in the code, and SCPI already shadows cpl[ch] — this is exposing a capability the hardware layer already models, not inventing one.

**Design:** In analog/frontend.go add cpl [2]int (0=DC,1=AC,2=GND) and SetCoupling(ch,mode). Rewrite channelByte(idx, ch2) (currently hard-wired 0x20|0x01|0x08) to take coupling: set bit3 (DC) only in DC mode, clear it for AC, and set bit1 (GND) in GND mode; keep bit2 range and bit0 BWL. Re-emit via the existing applyLocked() absolute-relay-word path (never read-modify-write). Device: add 'Coupling' slot to the new pgChan menu page cycling DC/AC/GND for the selected channel. Web: coupling <select> in ui.html, app.js {control:'coupling1'/'coupling2'}, web.go hSet→fe.SetCoupling, hStatus returns it. Wire the existing scpi.go 'CPL' shadow through to fe.SetCoupling. Test by asserting the exact relay word bits per mode in frontend_test.go (no real hardware needed) plus a web_test round-trip.

**Files:** internal/analog/frontend.go, internal/panel/menu.go, internal/web/web.go, internal/web/ui.html, internal/web/app.js, internal/scpi/scpi.go, internal/analog/frontend_test.go, internal/web/web_test.go

### 3. Timing/pulse measurements + shared measure package  
`both` · effort M-L

**Why:** The current 8-param set (web.go measure()) has no rise/fall, +width/−width, overshoot/preshoot, Vtop/Vbase/Vamp, or cross-channel phase/delay — the core timing set a scope is expected to compute. Extracting the algorithm into a shared package is the enabling foundation for putting measurements on the physical LCD (rank 4), so it pays double.

**Design:** Move measure() and the meas struct out of web.go into a new internal/measure package that takes ([]uint8, voltsPerCode, offV, sampleS) and returns a struct extended with Vtop/Vbase/Vamp (histogram top/base of the two dominant code bins), RiseS/FallS (10%–90% crossings between base and top), PWidthS/NWidthS/PDuty/NDuty (mid-level crossings), Over/Pre (overshoot/preshoot vs amplitude), plus a two-channel PhaseDeg/DelayS from cross-correlation of C1/C2. web.go hFrame calls measure.Compute; app.js measBody keys list (app.js:734) gains the new rows and a small 'add/remove measurement' picker. Test in a promoted measure_test.go with synthetic square/ramp/two-phase-sines asserting known rise time, duty, and phase; acceptance_browser.mjs asserts the new table rows render.

**Files:** internal/measure/ (new), internal/web/web.go, internal/web/measure_test.go, internal/web/app.js, internal/web/ui.html, internal/web/acceptance_browser.mjs

### 4. On-device MEASURE panel  
`device` · effort M

**Why:** Today the LCD HUD shows only Vpp+freq (render.go vppFreq); at the instrument with no PC attached there is no way to read Vrms/mean/period/rise/fall/duty. This is the primary standalone analysis path for a bench user and the largest web-vs-device asymmetry. It is high-value balance and reuses the rank-3 package directly.

**Design:** Add a pgMeas page in panel/menu.go (there is already an unused ledMeasure 0x0100 lamp to light) selectable via a MEAS softkey; the page's slots pick which measurements are active (store MeasSel on the Controller, expose in MenuView). In lcd/render.go add drawMeas(sf,f,hud) that runs internal/measure over f.C1/f.C2[:Valid] with the probe-scaled vpc and renders a compact left-column list of active params per channel when hud.MeasOpen. Add MeasOpen/MeasSel/vpc fields to the lcd.HUD struct populated from the panel snapshot. Test via render_test.go asserting the rendered text contains the selected params, and end-to-end through /api/screen.png in a web/acceptance test.

**Files:** internal/panel/menu.go, internal/panel/panel.go, internal/lcd/render.go, internal/measure/ (shared), internal/lcd/render_test.go, internal/panel/panel_test.go

### 5. On-device cursors (ADJUST-driven, with Δ readout)  
`device` · effort M

**Why:** Cursors are web-only (~39 refs) and absent from the physical 800×480 screen; manual Δt/1-Δt/ΔV at the instrument is a fundamental bench interaction and a big visible parity gap. Self-contained on the render + panel side and cleanly testable via the render tests.

**Design:** Add a pgCursor page in panel/menu.go: slots select the active cursor (t1/t2/v1/v2) and on/off; the ADJUST knob (already routed via menuAdjust) nudges the selected cursor. Hold cursor fractions on the Controller and surface them in a CursorView / the lcd.HUD. In lcd/render.go add drawCursors(sf,hud) drawing two dashed vertical (time) and two horizontal (voltage) lines plus a readout box computing Δt from hud.TdivS and ΔV from the probe-scaled per-channel vpc (mirroring the web math, but correctly zoom/probe-aware from the start). Test in render_test.go by placing cursors and asserting the lines and Δt/ΔV text; verify through /api/screen.png.

**Files:** internal/panel/menu.go, internal/panel/panel.go, internal/lcd/render.go, internal/lcd/render_test.go, internal/panel/panel_test.go

### 6. Clipping / over-range indicator  
`both` · effort S

**Why:** When a trace saturates at the top/bottom rail, every measurement is silently wrong. A clip flag is the cheapest way to stop users trusting a bad reading, and it lands on both surfaces for balance. Low effort, high safety-of-reading value.

**Design:** Detect per-channel rail saturation (any sample ==0 or ==255 over f.Valid). Device: in lcd/render.go drawHUD, if a channel clips, draw a 'CLIP' badge in that channel's colour and tint the top/bottom graticule edge; compute inline in Render. Web: either compute client-side in app.js from frame.c1/c2 codes or add clip1/clip2 booleans to frameReply in web.go; show a clip badge near the channel label in ui.html. Test with a railed synthetic frame in render_test.go asserting the CLIP text, and a web_test/acceptance assertion on the flag/badge.

**Files:** internal/lcd/render.go, internal/web/web.go, internal/web/app.js, internal/web/ui.html, internal/lcd/render_test.go, internal/web/web_test.go

### 7. Cursor ΔV zoom/probe correctness fix + trace-value & FFT readout  
`web` · effort S

**Why:** A confirmed latent correctness bug: app.js updateCursors computes ΔV from screen-fraction × full-scale but ignores the per-channel vertical zoom the trace is actually drawn at, so ΔV is wrong whenever a channel is zoomed. Cursors also lack absolute trace-value and any Hz/dB readout in FFT mode. High-confidence, small, and it makes an already-shipped feature trustworthy.

**Design:** In app.js:748-749 divide the full-scale by the channel zoom: vFull1 = 256*(frame.vpc1)/(st.zoom1||1) (and vFull2 with zoom2) — the trace is drawn via drawTrace(...,zoom) so a screen fraction maps to vFull/zoom volts. Fold probe (rank 1) in automatically since vpc is already probe-scaled. Add an absolute trace-value readout (sample under each cursor via the same window/index math) and an optional snap-to-trace toggle; in FFT mode report the cursor's Hz and dB using the existing fft_browser scaling. Test in acceptance_browser.mjs by zooming a channel, placing cursors a known fraction apart, and asserting ΔV; add a small node unit around the extracted math.

**Files:** internal/web/app.js, internal/web/ui.html, internal/web/acceptance_browser.mjs

### 8. CSV export in scaled volts/seconds  
`web` · effort S

**Why:** CSV currently dumps raw 0–255 ADC codes (app.js:1266), which is useless for analysis and inconsistent with everything else once probe/coupling scaling exists. Making it real volts/time is trivial and expected by anyone exporting data.

**Design:** In app.js eCSV (line 1263) change the header to t_s,c1_v,c2_v and emit volts = (code-128)*vpc - offV per channel using frame.vpc1/off1_v/vpc2/off2_v (already probe-scaled by rank 1); keep the existing dt time base. Preserve a raw-codes option behind a checkbox if desired. Test via an acceptance/node assertion on the emitted header and a couple of scaled sample values against a known synthetic frame.

**Files:** internal/web/app.js, internal/web/ui.html, internal/web/acceptance_browser.mjs

### 9. Reference waveforms (REF A/B save + overlay/compare)  
`web` · effort M

**Why:** A bench staple entirely missing: save a live trace and overlay/compare it against live. Highly visual (great for the release screenshots) and self-contained on the web side, so it lands real user value without waiting on the pending state-store refactor.

**Design:** In app.js add ref = {A:null,B:null}; a 'Save A/Save B' captures a deep copy of the current frame.c1/c2 plus their vpc/off/col_span_s; render them in the main draw loop with drawTrace in distinct dimmed colours honouring their stored scale, with show/hide + clear controls in ui.html. Optionally persist to localStorage so refs survive reload (a pragmatic partial answer to the missing setup persistence). Test in acceptance_browser.mjs: save a ref from one frame, change the live signal, assert both the ref and live polylines are present (state + pixel check).

**Files:** internal/web/app.js, internal/web/ui.html, internal/web/acceptance_browser.mjs, internal/web/scope_po.mjs

### 10. Trigger holdoff  
`engine` · effort M-L

**Why:** A hard requirement flagged high in every audit and absent entirely: holdoff time is how you get a stable lock on bursts, modulated, and multi-edge repetitive waveforms, and every comparison scope (Rigol/Siglent) ships it. Ranked last because it touches the acquisition FSM and is the riskiest change.

**Design:** In engine.go add holdoffNs atomic.Int64 and SetTrigHoldoff(ns); in the arm/qualify path (engine.go run loop + qualify.go) record the monotonic time of the last accepted trigger and refuse to re-arm/accept the next until holdoffNs has elapsed. Surface Holdoff in engine.Stats. panel/menu.go: add a 'Holdoff' slot to the TRIGGER page cycling 0/100ns/1µs/10µs/100µs/1ms via ADJUST. web.go hSet 'holdoff' + ui.html/app.js control + hStatus readback. Test in engine_test.go with a synthetic source containing closely-spaced edges, asserting accepted triggers are spaced ≥ holdoff; a web_test round-trip on the control.

**Files:** internal/engine/engine.go, internal/engine/qualify.go, internal/panel/menu.go, internal/web/web.go, internal/web/ui.html, internal/web/app.js, internal/engine/engine_test.go, internal/web/web_test.go

## Implementation order

- 1. Probe attenuation — foundational scalar; do first so every downstream readout (measurements, cursors, CSV, trigger volts) inherits correct scaling and nothing gets reworked.
- 2. Cursor ΔV zoom/probe fix + readouts — quick correctness win, web-only, no new deps, and immediately validates the probe scaling from step 1.
- 3. CSV scaled export — trivial follow-on that reuses the now probe-scaled vpc/off; a fast, visible improvement.
- 4. Clipping/over-range indicator — small, both-surface quick win that makes every other reading trustworthy.
- 5. AC/DC/GND coupling — moderate; relay bits and SCPI shadow already exist. Build the shared pgChan menu page here (probe UI can move onto it too).
- 6. Timing/pulse measurements + shared internal/measure package — the enabling refactor; land and test the algorithms on web before the device consumes them.
- 7. On-device MEASURE panel — depends on step 6's shared package; first big device-parity feature.
- 8. On-device cursors — independent device feature, medium effort; reuses the pgChan/menu and probe-aware scaling patterns already in place.
- 9. Reference waveforms — self-contained web feature; slot it in once the core readouts are correct so REF comparisons are meaningful and screenshot-ready.
- 10. Trigger holdoff — riskiest (touches the acquisition FSM); do last with focused engine_test coverage so an FSM regression can't destabilize earlier work.

## Release-readiness checklist

- [ ] README rewrite (root /home/labeth/ws/open-sds1000cml/README.md): stop saying 'specifications only' / 'Private while in progress'; describe app/ (clean-room scope application, ~50 Go files) and ota/ (agent+otactl+boot anchor, ~37 Go files); add a Supported Hardware section (Siglent SDS1000CML+ / SDS1102CML+) and top-level build→flash→boot→takeover→browse :8080 quickstart linking app/README.md and ota/README.md.
- [ ] LICENSE at repo root: add an OSI-approved license file (none exists anywhere today). Choose deliberately given the reverse-engineered/clean-room nature; without it the code is all-rights-reserved and cannot be used.
- [ ] Clean-room provenance / legal doc: a dedicated PROVENANCE (or NOTICE) file stating what sources were and were not used, that no vendor firmware/binaries were reused, and how the reimplementation stays legally defensible — the stance is asserted conceptually but undocumented.
- [ ] Trademark + warranty disclaimer: state that 'Siglent' and 'SDS1102CML+' are trademarks of their owner, that this project is unaffiliated/unendorsed, and add a no-warranty clause.
- [ ] Safety notes (prominent, root-level): this takes over a mains-powered instrument, claims the hardware watchdog (unserviced → ~60s SoC warm-reset that drops USB hotplug and forces a physical power-cycle), and can wedge the acquisition bus — state clearly 'at your own risk, you can brick the unit, recovery is a mains power-cycle / untakeover'.
- [ ] Screenshots + short demo GIF: zero image files exist for an inherently visual product. Capture the :8080 web UI (YT/XY/FFT, cursors, decode) and the 800×480 LCD via /api/screen.png; embed in README. The new features (on-device MEASURE, cursors, REF overlay) make strong shots.
- [ ] CI (.github/workflows): none exists though 31 test files do. Add a workflow running go vet + go build (ARMv7 cross via the existing Makefiles) + go test ./... for app/ and ota/, plus the Playwright acceptance job (self-skips when node absent), and a status badge.
- [ ] CONTRIBUTING guide: document the hard invariants a naive PR can violate (single GPMC fd owner, inherited-fd-only, never rewrite FPGA config, app↔OTA health contract) and the clean-room contribution rules.
- [ ] Quickstart / onboarding: a single runnable get-started path in the root README (build → flash USB → boot → takeover → browse :8080) tying together the good per-module quickstarts.
- [ ] Community-health files: CODE_OF_CONDUCT.md, SECURITY.md, and issue/PR templates — expected furniture for a credible public GitHub launch.
- [ ] Reconcile go.mod module paths: replace the local 'open-sds/app' and 'open-sds/ota' placeholders with github.com/labeth/open-sds1000cml paths so external references and go tooling match the hosted repo.
- [ ] Version reporting + field-update doc: the OTA agent + untakeover recovery exist but the user-facing update story is thin — document the rollback-safe OTA/USB update path and confirm buildinfo.String() version is surfaced in the UI (already in hStatus) and on the LCD.
