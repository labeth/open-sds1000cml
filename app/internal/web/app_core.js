// ENGMODEL-OWNER-UNIT: FU-WEB-APP-CORE
// app_core.js — frame/status apply, redraw dispatch, measurement/status UI updates (classic script; shares app.js globals).

"use strict";

// scheduleRender coalesces render work onto ONE requestAnimationFrame tick, so a
// burst of frame arrivals or pointer-move events collapses to a single paint at
// the display rate — and, crucially, the paint runs in a fresh task that yields
// to input between frames instead of blocking inside a network/pointer callback.
// The expensive protocol decode is throttled so it never dominates the loop.
let _renderRaf = 0, _lastDecodeMs = 0;
// TRLC-LINKS: REQ-SDS-071
function scheduleRender() {
  if (_renderRaf) return;
  _renderRaf = requestAnimationFrame(() => {
    _renderRaf = 0;
    const now = performance.now();
    // decode is the heaviest per-frame work; refresh it at most ~8×/s (the
    // overlay a few frames stale is imperceptible). Skipped entirely when off —
    // the proto→off change clears the overlay once, so it stays clear.
    if (dcfg.proto !== "off" && now - _lastDecodeMs > 120) { computeDecode(); _lastDecodeMs = now; }
    redraw();
    updateMeas();
    updateCursors();
    if (typeof wsTick === "function") wsTick(now); // the shown workspace's readouts (stats, spectrum, difference)
  });
}

// TRLC-LINKS: REQ-SDS-071
function redraw() {
  refreshAria(); // keep aria-pressed in sync with view toggles (which call redraw)
  updateStatusLine(); // time/div follows the zoom → keep it live, not just per status poll
  glBeginFrame();     // clear the scope's WebGL frame; ctx routes every draw to the GPU
  if (view.mode === "SPG") { drawSpgMain(); glEndFrame(); return; }   // SPECTRUM's waterfall (app_views.js)
  if (view.mode === "BODE") { drawBodeMain(); glEndFrame(); return; } // SPECTRUM's Bode plot (app_views.js)
  if (view.mode === "XY") { drawXY(); drawCursors(); drawBoxZoom(ctx); glEndFrame(); return; }
  if (view.mode === "FFT") { drawFFT(); drawCursors(); drawBoxZoom(ctx); glEndFrame(); drawNav(); return; }
  if (view.persist && GLR && frame && !frame.is_env) {
    // Afterglow: grid + refs paint on the screen; the live traces accumulate
    // (with per-frame fade) in a GL framebuffer that is then composited over the
    // grid; channel/trigger markers sit on top. See R.persistFade/Composite.
    drawGrid(ctx); drawRefs(ctx);
    GLR.persistFade(0.14);
    if (view.c2) drawTrace(ctx, frame.c2, C2COL, st ? st.zoom2 : 1, !!(st && st.inv2));
    if (view.c1) drawTrace(ctx, frame.c1, C1COL, st ? st.zoom1 : 1, !!(st && st.inv1));
    drawMath(ctx);
    GLR.persistComposite();
    drawChannelMarkers(ctx); drawTrigMarkers(ctx);
    drawYTPeaks(ctx); drawDecode(ctx); drawCursors(); drawSrGate();
    if (window.zm) drawZones(ctx);
    drawBoxZoom(ctx); glEndFrame(); drawNav();
    return;
  }
  drawYT(ctx);
  drawYTPeaks(ctx);
  drawDecode(ctx);
  drawCursors();
  drawSrGate();
  if (window.zm) drawZones(ctx);
  drawBoxZoom(ctx);
  glEndFrame();       // flush the scope batch; #nav is its own canvas, drawn after
  drawNav();
}

// updateMeas refreshes the measurement readouts the shown workspace has: SCOPE's
// quick readouts (the core set, both channels) and MEASURE's all-values table.
// Every value cell carries data-meas="<ch>:<key>" so one reader finds it
// wherever it is shown.
// TRLC-LINKS: REQ-SDS-204, REQ-SDS-217
function updateMeas() {
  const clip1 = !!(frame && frame.clip1), clip2 = !!(frame && frame.clip2);
  const sig = clip1 + ":" + clip2;
  if (sig !== measDomSig || !measCells) {
    measDomSig = sig;
    // TRLC-LINKS: REQ-SDS-204
    const clipTag = c => c ? ` <span class="clip" title="signal is clipping — increase V/div or probe; measurements unreliable">⚠ CLIP</span>` : "";
    // TRLC-LINKS: REQ-SDS-204
    const half = (keys) => `<div class="qcol"><span></span><span class="hd cc1">C1${clipTag(clip1)}</span><span class="hd cc2">C2${clipTag(clip2)}</span>` +
      keys.map((k) => `<span class="k">${k}</span><span class="v cc1" data-meas="1:${k}"></span><span class="v cc2" data-meas="2:${k}"></span>`).join("") + `</div>`;
    $("quickBody").innerHTML = half(MEAS_CORE.slice(0, 4)) + half(MEAS_CORE.slice(4));
    // all values: every parameter in two columns side by side, nine and nine as
    // on the scope, so every value shows without scrolling and C1/C2 sit close
    // to their names
    // TRLC-LINKS: REQ-SDS-217
    const cell = (k) => k ? `<th>${k}</th><td class="cc1" data-meas="1:${k}"></td><td class="cc2" data-meas="2:${k}"></td>` : "<th></th><td></td><td></td>";
    const hd = `<th></th><td class="cc1">C1${clipTag(clip1)}</td><td class="cc2">C2${clipTag(clip2)}</td>`;
    let html = `<tr class="hd">${hd}<td></td>${hd}</tr>`;
    const rows = Math.ceil(MEAS_ALL.length / 2);
    for (let i = 0; i < rows; i++) html += `<tr>${cell(MEAS_ALL[i])}<td></td>${cell(MEAS_ALL[rows + i])}</tr>`;
    $("measBody").innerHTML = html;
    measCells = [...document.querySelectorAll("#quickBody [data-meas], #measBody [data-meas]")].map((el) => {
      const [ch, k] = el.dataset.meas.split(":");
      return { el, ch: +ch, k };
    });
  }
  const m1 = frame && frame.m1, m2 = frame && frame.m2;
  for (const c of measCells) {
    const t = fmtMeas(c.k, c.ch === 1 ? m1 : m2);
    if (c.el.textContent !== t) c.el.textContent = t;
  }
}

let lastCurHTML = "";

// ---- cursors anchored to the signal ----
// In Y-T the cursors keep the signal's time (s from the trigger) and volts
// (at the probe tip, on cur.at.ch), as the scope's own do: a zoom, a
// timebase, a V/div or an offset change moves them on the screen to the same
// instants and levels. cur.t1..v2 are their screen fractions now (beyond
// 0..1 when off the screen), recomputed per draw.

// curTimeAt is the time from the trigger (s) at screen fraction x.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curTimeAt(x) {
  const edge = frame.edge_frac >= 0 ? frame.edge_frac : 0.5;
  return ((view.win.a + x * (view.win.b - view.win.a)) - edge) * (frame.col_span_s || 0);
}

// curXAt is the screen fraction of time t from the trigger.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curXAt(t) {
  const edge = frame.edge_frac >= 0 ? frame.edge_frac : 0.5;
  return (edge + t / frame.col_span_s - view.win.a) / (view.win.b - view.win.a);
}

// curScale is channel ch's volts per code (at the tip), detent zoom and offset.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curScale(ch) {
  return { vpc: (ch === 2 ? frame.vpc2 : frame.vpc1) || 1 / 25, z: (st && (ch === 2 ? st.zoom2 : st.zoom1)) || 1,
    off: (ch === 2 ? frame.off2_v : frame.off1_v) || 0 };
}

// curVoltAt is channel ch's volts at canvas fraction y.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curVoltAt(y, ch) {
  const k = curScale(ch);
  return (codeAtY(y, k.z) - 128) * k.vpc - k.off;
}

// curYAt is the canvas fraction of channel ch's volts v.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curYAt(v, ch) {
  const k = curScale(ch);
  return yFor(128 + (v + k.off) / k.vpc, k.z) / CH;
}

// curAnchored reports whether the cursors map to the signal now (Y-T, a
// frame with a known record span).
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curAnchored() {
  return view.mode === "YT" && !!frame && frame.col_span_s > 0 && view.win.b > view.win.a;
}

// curSync places the cursors on their signal times and levels; the first
// time, it takes them from where they are on the screen.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curSync() {
  if (!curAnchored()) return;
  if (!cur.at) {
    const ch = view.c1 || !view.c2 ? 1 : 2;
    cur.at = { t1: curTimeAt(cur.t1), t2: curTimeAt(cur.t2), v1: curVoltAt(cur.v1, ch), v2: curVoltAt(cur.v2, ch), ch };
    return;
  }
  cur.t1 = curXAt(cur.at.t1); cur.t2 = curXAt(cur.at.t2);
  cur.v1 = curYAt(cur.at.v1, cur.at.ch); cur.v2 = curYAt(cur.at.v2, cur.at.ch);
}

// curAnchor takes cursor k's (t1, t2, v1, v2) signal value from its screen
// place, after a drag.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function curAnchor(k) {
  if (!curAnchored() || !cur.at) return;
  cur.at[k] = k[0] === "t" ? curTimeAt(cur[k]) : curVoltAt(cur[k], cur.at.ch);
}

// updateCursors fills MEASURE's cursor table: the A/B time and voltage cursor
// positions and their differences, per channel.
// TRLC-LINKS: REQ-SDS-202, REQ-SDS-217
function updateCursors() {
  const body = $("curBody");
  if (!view.cursors || !frame) {
    const t = `<tr><td class="e" colspan="3">${view.cursors ? "Waiting for a capture." : "Cursors are off — turn them on (C) and drag the dashed lines on the scope."}</td></tr>`;
    if (lastCurHTML !== t) { lastCurHTML = t; body.innerHTML = t; }
    return;
  }
  curSync();
  // Time of a cursor relative to the trigger (t = 0 at the edge), volts at a
  // channel's tip: the anchored values themselves.
  const ta = curTimeAt(cur.t1), tb = curTimeAt(cur.t2), dt = Math.abs(tb - ta);
  // TRLC-LINKS: REQ-SDS-217
  const v = (y, ch) => cur.at && ch === cur.at.ch && curAnchored() ? cur.at[y] : curVoltAt(cur[y], ch);
  const dv1 = Math.abs(v("v2", 1) - v("v1", 1)), dv2 = Math.abs(v("v2", 2) - v("v1", 2));
  const html =
    `<tr><th>A / B</th><td>${eng(ta, "s")}</td><td>${eng(tb, "s")}</td></tr>` +
    `<tr><th>Δt</th><td colspan="2">${eng(dt, "s")}</td></tr>` +
    `<tr><th>1/Δt</th><td colspan="2">${dt > 0 ? eng(1 / dt, "Hz") : "—"}</td></tr>` +
    `<tr><th class="cc1">C1 A / B</th><td class="cc1">${eng(v("v1", 1), "V")}</td><td class="cc1">${eng(v("v2", 1), "V")}</td></tr>` +
    `<tr><th>ΔV C1</th><td colspan="2" class="cc1">${eng(dv1, "V")}</td></tr>` +
    `<tr><th class="cc2">C2 A / B</th><td class="cc2">${eng(v("v1", 2), "V")}</td><td class="cc2">${eng(v("v2", 2), "V")}</td></tr>` +
    `<tr><th>ΔV C2</th><td colspan="2" class="cc2">${eng(dv2, "V")}</td></tr>`;
  if (lastCurHTML !== html) { lastCurHTML = html; body.innerHTML = html; }
}

// TRLC-LINKS: REQ-SDS-023, REQ-SDS-202
function applyFrame(f) {
  if (sr.showing) { // unfrozen behind our back (freeze button): leave stack view cleanly
    sr.savedWin = { a: view.win.a, b: view.win.b, zoomed: userZoomed };
    sr.showing = false;
    $("srShow").classList.remove("on");
  }
  frame = f; lastSeq = f.seq;
 if (typeof updatePrecisionLimits === "function") updatePrecisionLimits(f);
  const sig = acqSig(f);
  if (sig !== lastSig) { userZoomed = false; lastSig = sig; } // band/depth/run change → re-home
  // Keep the display TRIGGER-LOCKED every frame: the raw record is NOT phase-stable
  // (the trigger lands at a different sample each acquisition), so the edge — not a
  // fixed sample — is the anchor. At home, homeWindow re-centres the edge at posFrac
  // WITHOUT clamping the deep window into the record. When the user has ZOOMED, the
  // window is frozen — but the edge still wanders, so without following it the trace
  // slides ("frame-locked, not trigger-locked"). EDGE-FOLLOW shifts the frozen window
  // by the frame-to-frame edge delta so the trigger stays put at any zoom.
  if (!userZoomed) {
    const h = homeWindow(f); view.win.a = h.a; view.win.b = h.b; view.vwin.a = 0; view.vwin.b = 1;
  } else if (f.edge_frac >= 0 && lastEdgeFrac != null && lastEdgeFrac >= 0) {
    const d = f.edge_frac - lastEdgeFrac;
    view.win.a += d; view.win.b += d;
  }
  lastEdgeFrac = f.edge_frac;
  scheduleRender(); // coalesced paint on the next rAF — never block the poll callback
}

// trigState mirrors the LCD state machine (render.go) so a glance between the
// bench screen and the browser shows the same word.
// TRLC-LINKS: REQ-SDS-204
function trigState() {
  if (!st) return "—";
  if (!st.running) return "STOP";
  if (st.single) return "SNGL";
  if (st.roll) return "ROLL"; // untriggered scrolling view at slow timebases
  if (st.waiting) return "WAIT"; // the held frame's T'D is stale
  if (frame && frame.trigd) return "T'D";
  if (st.norm) return "WAIT";
  return "AUTO";
}

// TRLC-LINKS: REQ-SDS-204
function refreshAria() {
  for (const id of PRESSED) {
    const b = $(id);
    if (!b) continue;
    const v = b.classList.contains("on") ? "true" : "false";
    if (b.getAttribute("aria-pressed") !== v) b.setAttribute("aria-pressed", v);
  }
}

// TRLC-LINKS: REQ-SDS-204
function applyStatus() {
  // The button shows the ACTION you can take: running → press to STOP, stopped →
  // press to RUN (green run / red stop). Redundant glyph + word.
  $("run").textContent = st.running ? "STOP ■" : "RUN ▶";
  $("run").classList.toggle("is-stop", !!st.running);
  $("run").classList.toggle("is-run", !st.running);
  $("stopped").style.display = st.running ? "none" : "block";
  setFromStatus($("mode"), st.norm ? 1 : 0);
  setFromStatus($("slope"), st.trig_rising ? 1 : 0);
  setFromStatus($("source"), st.trig_source === 1 ? 1 : 0);
  $("ets").classList.toggle("on", !!st.ets && st.band !== "sram");
  $("ets").disabled = st.band === "sram";
  $("ets").title = st.band === "sram" ? "Equivalent-time acquisition is unavailable with SRAM capture" : "Equivalent-time sampling (≤50 ns/div)";
  $("decStream").disabled = st.band === "sram";
  $("decStream").title = st.band === "sram" ? "Use Stream + trigger (Trigger on bytes pane) for continuous FPGA decoding and a retained capture" : "Stitch back-to-back captures into a continuous history";
  $("single").classList.toggle("on", !!st.single);
  setFromStatus($("rollStream"), st.roll_stream ? 1 : 0);
  if (document.activeElement !== $("tpos") && Number.isFinite(st.trig_pos_frac) && st.trig_pos_frac >= 0 && st.trig_pos_frac <= 1) {
    $("tpos").value = st.trig_pos_frac;
    $("tposv").textContent = Math.round(st.trig_pos_frac * 100) + " %";
  }
  $("wedged").style.display = st.wedged ? "inline" : "none";
  // Loading indicator (after a short grace so fast frames never flash it).
  const loading = !!st.busy && st.busy_ms >= 150;
  $("loading").hidden = !loading;
  if (loading) $("loading").textContent = "LOADING" + (st.busy_pct >= 0 ? " " + Math.round(st.busy_pct) + "%" : "…");
  setFromStatus($("ttype"), st.trig_type || 0);
  updateTriggerQualifiers(st.trig_qual);
  updateQualRow();
  setFromStatus($("acq"), st.acq_mode || 0);
  updateAcqN();
  if (document.activeElement !== $("memdepth") && st.mem_depth) $("memdepth").value = st.mem_depth;
  $("memdepth").disabled = st.band === "sram";
  $("memdepth").title = st.band === "sram" ? "Full SRAM retained; RUN transfers a preview, STOP recalls the complete record" : "Memory depth";
  if (document.activeElement !== $("holdoff")) $("holdoff").value = st.holdoff_s || 0;
  if (!lvlDragging && st.trig_code) { $("lvl").value = st.trig_volts; $("lvlv").textContent = eng(st.trig_volts, "V"); }
  if ($("tdiv").options.length === 0 && st.tdivs)
    for (const t of st.tdivs) { const o = document.createElement("option"); o.value = t; o.textContent = fmtTdiv(t); $("tdiv").appendChild(o); }
  if ($("vdiv1").options.length === 0 && st.vdivs)
    for (const id of ["vdiv1", "vdiv2"]) for (const v of st.vdivs) { const o = document.createElement("option"); o.value = v; $(id).appendChild(o); }
  // The options carry the range at the BNC (what the scope takes) and read
  // at the probe tip, as the LCD and SCPI do: 10x at the 0.2 V range is 2 V.
  for (const ch of [1, 2]) {
    const el = $("vdiv" + ch), p = probeOf(ch);
    if (el._probe !== p) { el._probe = p; for (const o of el.options) o.textContent = fmtVdiv(+(o.value * p).toPrecision(3)); }
  }
  for (const [id, val] of [["tdiv", st.tdiv_s], ["vdiv1", st.vdiv1], ["vdiv2", st.vdiv2]]) {
    const el = $(id);
    if (val && document.activeElement !== el && !(el._userAt && performance.now() - el._userAt < 1500))
      for (const o of el.options) if (Math.abs(+o.value - val) <= val * 1e-6) { o.selected = true; break; }
  }
  for (const [id, val] of [["probe1", st.probe1], ["probe2", st.probe2]]) setFromStatus($(id), val || 1);
  for (const [id, val] of [["cpl1", st.cpl1], ["cpl2", st.cpl2]]) setFromStatus($(id), val || 0);
  // The strip's trigger caption names the type (and the bytes of an armed serial trigger).
  {
    const types = ["Edge", "Pulse", "Slope", "Video"];
    const serial = !!st.serial_mode && typeof stArmed === "function";
    const pat = serial ? ($("stBytes").value.trim() || "any") : "";
    const cap = "Trigger · " + (serial ? "Serial " + pat : types[st.trig_type || 0] || "Edge");
    if ($("trigCap").textContent !== cap) $("trigCap").textContent = cap;
  }
  // Sliders read/emit tip-referred volts. Their span and step follow each
  // channel's V/div: a fixed ±3.8 V in 50 mV steps was 25 div a step at
  // 2 mV/div and ±0.4 div end to end at 10 V/div.
  for (const ch of [1, 2]) {
    const vd = st["vdiv" + ch] || 1, p = probeOf(ch);
    const span = Math.min(vd <= 0.2 ? 1.6 : 40, 10 * vd) * p; // tier range, at most ±10 div
    $("off" + ch).min = -span; $("off" + ch).max = span; $("off" + ch).step = vd * p / 25;
  }
  {
    const src = (st.trig_source || 0) + 1, vd = st["vdiv" + src] || 1, p = trigProbe();
    const off = Number.isFinite(st["off" + src + "_v"]) ? st["off" + src + "_v"] : 0;
    // ±5 div of screen, its ends on whole steps of one code from 0 V: a range snaps to min + n·step, and an
    // offset-shifted min (-4.56 mV after Default setup) would leave 0 V off the grid (back at the centre the
    // level would read 4.39 mV)
    const step = vd * p / 25;
    $("lvl").step = step;
    $("lvl").min = Math.floor((-5 * vd * p - off) / step) * step; $("lvl").max = Math.ceil((5 * vd * p - off) / step) * step;
  }
  if (!offDragging) {
    $("off1v").textContent = Number.isFinite(st.off1_v) ? eng(st.off1_v, "V") : "—";
    $("off2v").textContent = Number.isFinite(st.off2_v) ? eng(st.off2_v, "V") : "—";
    if (Number.isFinite(st.off1_v)) $("off1").value = st.off1_v;
    if (Number.isFinite(st.off2_v)) $("off2").value = st.off2_v;
  }
  updateStatusLine();
  const state = trigState();
  const chip = $("trigChip");
  chip.textContent = state;
  chip.dataset.state = state;
  refreshAria();
  syncMirrors();
}

// The on-screen time/div — and its scale — FOLLOW the view zoom. The grid is
// always DIVX divisions, so when view.win is narrower/wider than the home 10-div
// screen (win_frac of the served record), the effective time/div scales by
// win_span/win_frac. Rebuilt on every redraw so it tracks zoom/pan live, not just
// on the 1s status poll. (Cursor Δt already scales by win_span.)
// TRLC-LINKS: REQ-SDS-204
function updateStatusLine() {
  if (!st) return;
  // Time/div is ALWAYS the HARDWARE timebase — zoom NEVER changes it. Zoom instead
  // spreads/compresses the on-screen grid dividers (drawGrid), and a "zoom ×N" tag
  // reports the factor.
  let b = frame ? (frame.tdiv_s || frame.displayed_sdiv_s) : (st.tdiv_s || st.displayed_sdiv_s);
  let zoomTxt = "";
  if (frame && view.mode === "YT" && frame.col_span_s > 0) {
    const winSpan = view.win.b - view.win.a;
    const hs = homeSpan(frame), zoom = (hs > 0 && winSpan > 0) ? hs / winSpan : 1;
    if (Math.abs(zoom - 1) > 0.02)
      zoomTxt = zoom >= 1 ? " · zoom ×" + (zoom < 10 ? zoom.toFixed(1) : String(Math.round(zoom)))
                          : " · zoom ÷" + (1 / zoom).toFixed(1);
  }
  const vspanZ = view.vwin.b - view.vwin.a;
  if (vspanZ < 0.999) {
    const vz = 1 / vspanZ;
    zoomTxt += " · vzoom ×" + (vz < 10 ? vz.toFixed(1) : String(Math.round(vz)));
  }
  // Capture-integrity badge: a degraded frame is a TRIGGERED capture that kept
  // its dead tail through the retries (untriggered half-records are published
  // honestly as free-run views and are not degraded); a long run of them means
  // captures are persistently incomplete.
  const degradedTxt = st.stuck_suspect
    ? ' · <b style="color:var(--stale,#e66)">⚠ ACQ DEGRADED — CAPTURES INCOMPLETE</b>'
    : ((frame && frame.degraded) || st.degraded ? ' · <b style="color:var(--stale,#e66)">⚠ DEGRADED</b>' : "");
  // Converters left not converting after the scope's recoveries: the LCD's
  // notice, here too (every capture carries a fake pattern until a restart).
  const adcTxt = st.adc_fault ? ' · <b style="color:var(--stale,#e66)">⚠ ' + escHTML(st.adc_fault) + "</b>" : "";
  const html =
    "<b>" + fmtTdiv(b) + "/div</b>" + zoomTxt + " · " + st.band + " · " + st.fps.toFixed(0) + " fps · seq <b>" + st.seq + "</b>" +
    " · cols " + reqCols + (st.mmap_drain ? "" : " (ioctl)") + (st.dead_runs ? " · DEAD " + st.dead_runs : "") +
    degradedTxt + adcTxt +
    " · cal:" + (st.cal_source || "?") + " · " + st.version;
  if (html !== lastLineHTML) { lastLineHTML = html; $("line").innerHTML = html; }
  const aria = "oscilloscope — trigger " + trigState() + ", " + fmtTdiv(b) + "/div";
  if (aria !== lastAria) { lastAria = aria; $("scope").setAttribute("aria-label", aria); }
}

// awaitFrame resolves when a fresh frame satisfying `pred` arrives (the global
// `frame` is updated by the poll loop), or after `timeout` ms. Used by autoset
// to converge across scale changes instead of relying on one stale capture.
// TRLC-LINKS: REQ-SDS-204
function awaitFrame(pred, timeout = 2500) {
  return new Promise((resolve) => {
    const startSeq = frame ? frame.seq : 0;
    const t0 = Date.now();
    // TRLC-LINKS: REQ-SDS-204
    const tick = () => {
      if (frame && frame.seq !== startSeq && pred(frame)) return resolve(true);
      if (Date.now() - t0 > timeout) return resolve(false);
      setTimeout(tick, 60);
    };
    tick();
  });
}

// updateQualRow shows the qualifier block of the chosen trigger type (Edge has none).
// TRLC-LINKS: REQ-SDS-204
function updateQualRow() {
  const t = +$("ttype").value;
  $("qualrow").style.display = t === 0 ? "none" : "block";
  $("qp-pulse").style.display = t === 1 ? "block" : "none";
  $("qp-slope").style.display = t === 2 ? "block" : "none";
  $("qp-video").style.display = t === 3 ? "block" : "none";
  // The FPGA decides these triggers: say so, or why it cannot yet.
  const b = (st && st.trigger_backend) || "";
  $("qualStat").textContent = b.startsWith("none: ") ? "No hardware trigger yet: " + b.slice(6) : b.startsWith("hardware-") ? "Decided in the FPGA" : "";
  // edge triggers the FPGA fired with no edge at the level, dropped: say so
  const noise = t === 0 && st && st.noise_triggers > 0.25;
  $("trigNoise").hidden = !noise;
  if (noise) $("trigNoise").textContent = `${Math.round(100 * st.noise_triggers)}% of triggers dropped as noise: raise the gain or move the level`;
}

// TRLC-LINKS: REQ-SDS-204
function updateAcqN() {
  const rate=$("precisionRate");
  if (!rate.options.length) for(let log=4;log<=20;log++) {
    const hz=500e6/2**log,o=document.createElement("option");o.value=hz;o.textContent=hz.toLocaleString(undefined,{maximumFractionDigits:6})+" S/s";rate.appendChild(o);
  }
  $("precisionRateLabel").hidden = +$("acq").value !== 4;
  if(document.activeElement!==rate) rate.value=(st && st.precision_rate_hz)||31250000;

  const m = +$("acq").value, row = $("acqnRow");
  if (m === 1 || m === 4) { fillAcqN([1, 4, 16, 32, 64, 128, 256], st ? st.avg_count : 16); row.hidden = false; $("acqnLbl").textContent = "Averages"; }
  else if (m === 2) { fillAcqN([3, 7, 15, 31, 63], st ? st.eres_len : 1); row.hidden = false; $("acqnLbl").textContent = "ERes length"; }
  else row.hidden = true;
  const names = ["normal", "average", "ERes", "peak detect", "precision"];
  $("acqSub").textContent = names[m] + (m === 1 || m === 4 ? " ×" + $("acqn").value : m === 2 ? " " + $("acqn").value : "");
}

// TRLC-LINKS: REQ-SDS-204
function fillAcqN(opts, sel) {
  const n = $("acqn");
  if (n.dataset.opts !== opts.join()) { n.innerHTML = ""; for (const v of opts) { const o = document.createElement("option"); o.value = v; o.textContent = v; n.appendChild(o); } n.dataset.opts = opts.join(); }
  if (document.activeElement !== n && sel) n.value = sel;
}

// ---- view ----
// setMode switches what the scope canvas draws: Y-T, X-Y, FFT, or SPECTRUM's
// spectrogram ("SPG") and Bode ("BODE") views. The workspaces own which view
// is shown (app_ws.js setWsView); this only applies it.
// TRLC-LINKS: REQ-SDS-204, REQ-SDS-219
function setMode(m) {
  view.mode = m;
  // Mode-dependent panels must be synced HERE, not only inside their drawer.
  updateFFTLists();
  // The navigator lives in Y-T (record overview) and FFT (spectrum
  // overview); toggling it changes the scope height, so re-measure
  // (resize() ends with redraw()).
  nav.style.display = (m === "YT" || m === "FFT") ? "" : "none";
  clearPersist(); resize();
}

// TRLC-LINKS: REQ-SDS-071
function recompute() { computeDecode(); redraw(); }

// TRLC-LINKS: REQ-SDS-203
function updateMathHint() {
  const h = $("mathHint");
  if (mathFn === "res1" || mathFn === "res2")
    h.textContent = "pick the carrier peak(s) of C" + (mathFn === "res1" ? 1 : 2) + " in SPECTRUM's peak table; the residual (minor waves) shows in purple";
  else h.textContent = "";
}

// ==== wiring ====

// ---- frame + status polling / transport / send ----


// showSuperseded stops this view and puts up a full-screen "refresh to reclaim"
// overlay. The device serves ONE live client at a time (to keep the engine's
// drain fast); a newer browser's /api/claim supersedes this one. Multiple tabs
// can stay open — refreshing any one reclaims control and supersedes the rest.
// TRLC-LINKS: REQ-SDS-023
function showSuperseded() {
  if (superseded) return;
  superseded = true;
  let o = document.getElementById("supersededOverlay");
  if (!o) {
    o = document.createElement("div");
    o.id = "supersededOverlay";
    o.style.cssText = "position:fixed;inset:0;z-index:99999;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:1.2em;background:rgba(0,0,0,.86);color:#eee;font:16px system-ui,-apple-system,sans-serif;text-align:center;padding:2em;backdrop-filter:blur(2px)";
    o.innerHTML = '<div style="font-size:1.5em;font-weight:600">Disconnected</div>' +
      '<div style="max-width:22em;line-height:1.5;opacity:.85">Another browser took control of the scope. Only one live view runs at a time so the acquisition stays fast.</div>';
    const b = document.createElement("button");
    b.textContent = "Refresh to reclaim";
    b.style.cssText = "font:inherit;padding:.6em 1.3em;cursor:pointer;border-radius:7px;border:1px solid #6a9;background:#183028;color:#cfe;font-weight:600";
    // TRLC-LINKS: REQ-SDS-023
    b.onclick = () => location.reload();
    o.appendChild(b);
    document.body.appendChild(o);
  }
  o.style.display = "flex";
}

let viewLeased = false; // the last poll asked for a view window

// viewParam is the poll's view-window lease: zoomed into a running live
// envelope (whose buckets spread over the whole screen) in Y-T, the scope
// reads the shown stretch again at full detail (view=a,b,cols: screen widths
// from the trigger, a little wider than shown, and the canvas columns);
// view=off once it is not (X-Y draws no zoomed stretch), so the reads, which
// cost frames, stop at once.
// TRLC-LINKS: REQ-SDS-202
function viewParam() {
  const f = frame, w = view.win, n = f && f.c1 ? f.c1.length : 0;
  if (userZoomed && view.mode === "YT" && f && f.peak_detect && f.win_frac > 0 && n > 1 && st && st.running) {
    // value indices: the page draws value i at i/(n-1); edge_frac and
    // win_frac are fractions of n
    const anchor = f.edge_frac >= 0 ? f.edge_frac * n : n / 2, screen = f.win_frac * n;
    const m = (w.b - w.a) * 0.02;
    const a = ((w.a - m) * (n - 1) - anchor) / screen, b = ((w.b + m) * (n - 1) - anchor) / screen;
    viewLeased = true;
    return "&view=" + a.toFixed(6) + "," + b.toFixed(6) + "," + Math.round(CW);
  }
  if (!viewLeased) return "";
  viewLeased = false;
  return "&view=off";
}

// TRLC-LINKS: REQ-SDS-023
async function pollFrameBin() {
  if (superseded) return; // another browser took control; wait for a manual refresh
  if (frozen || document.hidden) { setTimeout(pollFrameBin, 90); return; } // idle tick; hidden tabs stop hitting the device
  try {
    const r = await fetch("/api/frame.bin?since=" + lastSeq + "&cols=" + reqCols + "&full=1&waitms=1000&epoch=" + myEpoch + viewParam());
    if (r.status === 409) { showSuperseded(); return; } // a newer browser claimed the device — stop loading it
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("bad frame"); // never render a reply that failed validation
    binFailures = 0;
    // Re-check frozen AFTER the await: the request parks server-side for up
    // to 1 s, and a freeze/capture-review click mid-flight must not have its
    // snapshot clobbered by the late reply. lastSeq stays put, so unfreezing
    // immediately long-polls the newest frame.
    if (!f.unchanged && !frozen) applyFrame(f);
    // In FFT and spectrogram mode, keep a fresh spectrum frame for the source
    // (the display frame is the live envelope's extremes). Throttled;
    // fire-and-forget so it never paces the display loop.
    const specView = (view.mode === "FFT" || view.mode === "SPG") && !frozen && !(typeof sr !== "undefined" && sr.showing);
    if (specView && performance.now() - fftRawT > 150) fetchFftRaw();
    else if (!specView && fftLeased) {
      // give the lease back at once: its decimated frames are filtered, and
      // MEASURE's statistics would take them for 3 s (the scope's SPECTRUM does too)
      fftLeased = false;
      fetch("/api/frame.bin?lease=spectrum&end=1").catch(() => {});
    }
    setTimeout(pollFrameBin, 10); // the server did the pacing; this only guards a hot loop
  } catch (e) {
    // Bad reply or network error (OTA app restart) — retry the SAME transport
    // with jittered backoff; the endpoint comes back at full speed.
    binFailures++;
    setTimeout(pollFrameBin, Math.min(2000, 250 * binFailures) + 250 * Math.random());
  }
}

// fetchFftRaw pulls one spectrum frame (lease=spectrum, as the LCD's SPECTRUM
// asks: the full-rate record on short screens, else the filtered decimated one,
// which carries its bandwidth) to source the FFT and the spectrogram.
// Fire-and-forget + throttled by the caller; sets fftRaw + fftRawT, then a
// redraw picks it up.
// TRLC-LINKS: REQ-SDS-023
async function fetchFftRaw() {
  if (fftRawBusy) return;
  fftRawBusy = true; fftRawT = performance.now();
  try {
    fftLeased = true;
    const r = await fetch("/api/frame.bin?raw=1&lease=spectrum&waitms=300");
    if (r.ok) { const f = decodeBinFrame(await r.arrayBuffer()); if (f && !f.unchanged && f.c1 && f.sample_s > 0) { fftRaw = f; if (view.mode === "FFT") redraw(); } }
  } catch (e) { /* transient — the next tick retries */ } finally { fftRawBusy = false; }
}

let pollTimer = 0, pollBusy = false, pollKick = false;
// TRLC-LINKS: REQ-SDS-023
async function pollStatus() {
  if (superseded) return; // taken over by another browser; stop loading the device
  clearTimeout(pollTimer);
  pollBusy = true; pollKick = false;
  try { st = await (await fetch("/api/status")).json(); applyStatus(); sheetsFollow(); if (typeof decFollowScope === "function") decFollowScope(); if (typeof cmpRepeat === "function") cmpRepeat(); if (typeof refsFollow === "function") refsFollow(); if (typeof srScopeLine === "function") srScopeLine(); }
  catch (e) { $("line").textContent = "no connection"; lastLineHTML = ""; } // reset the diff guard or a static status keeps "no connection" stuck
  pollBusy = false;
  // While a SINGLE is armed or the device is loading, poll fast so the self-stop
  // and the loading indicator reach the UI promptly. Steady state stays 1 s.
  pollTimer = setTimeout(pollStatus, pollKick ? 150 : st && (st.single || st.busy) ? 250 : 1000);
}

// sheetSet sets one of the scope's sheet rows, as the phone does, and polls
// the status soon after.
// TRLC-LINKS: REQ-SDS-211
async function sheetSet(row, value) {
  try {
    const r = await (await fetch("/api/sheets/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ row, value }) })).json();
    if (r && r.ok === false && r.error) toast(r.error, "warn");
    kickStatus();
    return r;
  } catch (e) { return null; }
}

// sheetsFollow keeps the page's controls that are the scope's own sheet rows
// showing the scope's values, whoever set them (the scope's keys, the phone,
// SCPI): when the scope's sheet changes (its SheetsSeq) the rows are fetched
// once, so Stack master never writes a stale N over the depth set on the scope.
let sheetsSeqSeen = -1, sheetsBusy = false;
const SHEET_FOLLOW = [["bwl1", "ch1.bwl"], ["bwl2", "ch2.bwl"], ["inv1", "ch1.invert"], ["inv2", "ch2.invert"], ["masterN", "cmp.master_depth"], ["streamStops", "bus.stream_trigger"]];
// TRLC-LINKS: REQ-SDS-211
async function sheetsFollow() {
  const seq = st && st.panel ? st.panel.SheetsSeq : undefined;
  if (seq === undefined || seq === sheetsSeqSeen || sheetsBusy) return;
  sheetsBusy = true;
  try {
    const m = await (await fetch("/api/sheets")).json();
    const rows = {};
    for (const t of m.Tabs || []) for (const r of t.Rows || []) rows[r.ID] = r;
    for (const [id, row] of SHEET_FOLLOW) if (rows[row] && rows[row].Value !== undefined) setFromStatus($(id), rows[row].Value);
    const clock = rows["bus.spi_clock"];
    if (clock && typeof stShowClock === "function") stShowClock(+clock.Value || 0, clock.Text); // MHz in the field, Auto's rate as its placeholder
    sheetsSeqSeen = seq;
  } catch (e) { /* the next status poll tries again */ } finally { sheetsBusy = false; }
}

// kickStatus re-polls soon after a control change, so the loading indicator
// for the frame it causes shows without waiting out the 1 s poll.
// TRLC-LINKS: REQ-SDS-023
function kickStatus() {
  if (pollBusy) { pollKick = true; return; }
  clearTimeout(pollTimer);
  pollTimer = setTimeout(pollStatus, 150);
}

// ---- controls ----
// TRLC-LINKS: REQ-SDS-023
async function send(control, value) {
  if (autosetBusy && !await autosetDone) return { ok: false };
  try { const r = await (await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control, value }) })).json(); kickStatus(); return r; }
  catch (e) { return { ok: false }; }
}
// AUTOSET_WAIT_MS bounds the page's wait for the scope's AUTO, as the phone's
// (AUTOSET_MS): AUTO in BUS on a packet-image bus loads two FPGA images and
// runs 11 to 14 s (ARINC 429), close to the 15 s the page waited.
const AUTOSET_WAIT_MS = 30000;
// autoset runs the scope's AUTO for the workspace this page shows, or for
// workspace forWs ("scope": the eye's fit wants SCOPE's edge-triggered
// framing, not BUS's frame-start trigger).
// TRLC-LINKS: REQ-SDS-204, REQ-SDS-226
async function autoset(forWs) {
  if (autosetBusy) return;
  autosetBusy = true;
  let finishAutoset, completed = false;
  autosetDone = new Promise(resolve => { finishAutoset = resolve; });
  const btn = $("autoset"); btn.classList.add("on");
  try {
    // The last sweep that ended before the press: a press that cancels a sweep
    // started elsewhere ends it before a status read may see it busy, and the
    // sweep's end is named by AutosetDone moving on.
    let doneBefore = 0, noticeBefore = "";
    try {
      const p = (await (await fetch("/api/status")).json()).panel || {};
      doneBefore = p.AutosetDone || 0;
      noticeBefore = p.Notice || "";
    } catch (e) { return; }
    // trigger the device autoset (the hard-button path — one implementation),
    // framed for the workspace this page shows, whichever the LCD shows
    if (typeof forWs !== "string") forWs = typeof ws !== "undefined" ? ws.cur : "";
    try {
      const response = await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: "auto", for: forWs }) });
      if (!response.ok || !(await response.json()).ok) return;
    }
    catch (e) { return; }
    // Stable frequency can be a stale frame while the device is still
    // sweeping. Wait for the actual panel operation before releasing controls.
    const t0 = Date.now();
    let sawBusy = false;
    while (Date.now() - t0 < AUTOSET_WAIT_MS) {
      await new Promise(resolve => setTimeout(resolve, 100));
      const status = await (await fetch("/api/status")).json();
      if (status.panel && status.panel.AutosetBusy) sawBusy = true;
      else if (sawBusy || (status.panel && (status.panel.AutosetDone || 0) !== doneBefore)) {
        st = status;
        completed = true;
        break;
      }
    }
    if (!completed) return;
    goHome();
    applyStatus();
    // what the scope's AUTO did, as its notice says ("AUTO: 2 cycles, C1 500mV/div");
    // BUS says what it found itself
    const p = st.panel || {};
    if (p.Notice && p.Notice !== noticeBefore && /^AUTO/.test(p.Notice) && forWs !== "bus") toast(p.Notice);
  } catch (e) {
    // A failed status request must not release queued acquisition commands.
    completed = false;
  } finally {
    autosetBusy = false;
    finishAutoset(completed);
    $("autoset").classList.remove("on");
  }
}
