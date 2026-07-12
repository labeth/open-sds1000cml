// app_core.js — frame/status apply, redraw dispatch, measurement/status UI updates (classic script; shares app.js globals).

"use strict";

// scheduleRender coalesces render work onto ONE requestAnimationFrame tick, so a
// burst of frame arrivals or pointer-move events collapses to a single paint at
// the display rate — and, crucially, the paint runs in a fresh task that yields
// to input between frames instead of blocking inside a network/pointer callback.
// The expensive protocol decode is throttled so it never dominates the loop.
let _renderRaf = 0, _lastDecodeMs = 0;
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
  });
}

function redraw() {
  refreshAria(); // keep aria-pressed in sync with view toggles (which call redraw)
  updateStatusLine(); // time/div follows the zoom → keep it live, not just per status poll
  glBeginFrame();     // clear the scope's WebGL frame; ctx routes every draw to the GPU
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

function updateMeas() {
  const keys = measExpanded ? MEAS_CORE.concat(MEAS_MORE) : MEAS_CORE;
  const clip1 = !!(frame && frame.clip1), clip2 = !!(frame && frame.clip2);
  const sig = keys.length + ":" + clip1 + ":" + clip2;
  if (sig !== measDomSig) {
    measDomSig = sig;
    const clipTag = c => c ? ` <span class="clip" title="signal is clipping — increase V/div or probe; measurements unreliable">⚠ CLIP</span>` : "";
    let html = `<tr><th></th><td class="cc1">C1${clipTag(clip1)}</td>` +
               `<td class="cc2">C2${clipTag(clip2)}</td></tr>`;
    for (const k of keys) {
      html += `<tr><th>${k}</th><td class="cc1"></td><td class="cc2"></td></tr>`;
    }
    html += `<tr><td colspan="3" style="text-align:center;padding-top:3px">` +
            `<button id="measMore" class="btn-mini">${measExpanded ? "less ▲" : "more ▼"}</button></td></tr>`;
    $("measBody").innerHTML = html;
    $("measMore").onclick = () => { measExpanded = !measExpanded; updateMeas(); };
    const rows = $("measBody").querySelectorAll("tr");
    measCells = {};
    keys.forEach((k, i) => { measCells[k] = rows[i + 1].querySelectorAll("td"); });
  }
  for (const k of keys) {
    const [a, b] = measCells[k];
    const t1 = fmtMeas(k, frame && frame.m1), t2 = fmtMeas(k, frame && frame.m2);
    if (a.textContent !== t1) a.textContent = t1;
    if (b.textContent !== t2) b.textContent = t2;
  }
}

function updateCursors() {
  if (!view.cursors || !frame) { $("curCard").style.display = "none"; return; }
  $("curCard").style.display = "";
  // Cursors are screen fractions; the screen now spans (b-a) of the record.
  const dt = Math.abs(cur.t2 - cur.t1) * (frame.col_span_s || 0) * (view.win.b - view.win.a);
  // A full-height drag spans 200 codes ÷ vertical zoom (the 2 mV/5 mV detents
  // render magnified), times volts/code (already probe-scaled). Per channel.
  const z1 = (st && st.zoom1) || 1, z2 = (st && st.zoom2) || 1;
  const vspan = view.vwin.b - view.vwin.a; // a full-height drag spans the ZOOMED voltage range
  const vFull1 = 200 * vspan * (frame.vpc1 || 1 / 25) / z1, vFull2 = 200 * vspan * (frame.vpc2 || 1 / 25) / z2;
  const dv1 = Math.abs(cur.v2 - cur.v1) * vFull1, dv2 = Math.abs(cur.v2 - cur.v1) * vFull2;
  $("curBody").innerHTML =
    `<tr><th>Δt</th><td colspan="2">${eng(dt, "s")}</td></tr>` +
    `<tr><th>1/Δt</th><td colspan="2">${dt > 0 ? eng(1 / dt, "Hz") : "—"}</td></tr>` +
    `<tr><th>ΔV C1</th><td colspan="2" class="cc1">${eng(dv1, "V")}</td></tr>` +
    `<tr><th>ΔV C2</th><td colspan="2" class="cc2">${eng(dv2, "V")}</td></tr>`;
}

function applyFrame(f) {
  if (sr.showing) { // unfrozen behind our back (freeze button): leave stack view cleanly
    sr.savedWin = { a: view.win.a, b: view.win.b, zoomed: userZoomed };
    sr.showing = false;
    $("srShow").classList.remove("on");
  }
  frame = f; lastSeq = f.seq;
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
function trigState() {
  if (!st) return "—";
  if (!st.running) return "STOP";
  if (st.single) return "SNGL";
  if (frame && frame.trigd) return "T'D";
  if (st.norm) return "WAIT";
  return "AUTO";
}

function refreshAria() {
  for (const id of PRESSED) {
    const b = $(id);
    if (!b) continue;
    const v = b.classList.contains("on") ? "true" : "false";
    if (b.getAttribute("aria-pressed") !== v) b.setAttribute("aria-pressed", v);
  }
}

function applyStatus() {
  // The button shows the ACTION you can take: running → press to STOP, stopped →
  // press to RUN (green run / red stop). Redundant glyph + word.
  $("run").textContent = st.running ? "STOP ■" : "RUN ▶";
  $("run").classList.toggle("is-stop", !!st.running);
  $("run").classList.toggle("is-run", !st.running);
  $("stopped").style.display = st.running ? "none" : "block";
  $("mode").textContent = st.norm ? "NORM" : "AUTO";
  $("mode").classList.toggle("on", st.norm);
  $("slope").innerHTML = st.trig_rising ? "&#8599;" : "&#8600;";
  $("source").textContent = st.trig_source === 1 ? "C2" : "C1";
  $("ets").classList.toggle("on", !!st.ets);
  $("single").classList.toggle("on", !!st.single);
  if (document.activeElement !== $("tpos") && st.trig_pos_frac > 0) $("tpos").value = st.trig_pos_frac;
  $("wedged").style.display = st.wedged ? "inline" : "none";
  if (document.activeElement !== $("ttype")) $("ttype").value = st.trig_type || 0;
  updateQualRow();
  if (document.activeElement !== $("acq")) $("acq").value = st.acq_mode || 0;
  updateAcqN();
  if (document.activeElement !== $("memdepth") && st.mem_depth) $("memdepth").value = st.mem_depth;
  if (document.activeElement !== $("holdoff")) $("holdoff").value = st.holdoff_s || 0;
  if (!lvlDragging && st.trig_code) { $("lvl").value = st.trig_volts.toFixed(2); $("lvlv").textContent = st.trig_volts.toFixed(2) + " V"; }
  if ($("tdiv").options.length === 0 && st.tdivs)
    for (const t of st.tdivs) { const o = document.createElement("option"); o.value = t; o.textContent = fmtTdiv(t); $("tdiv").appendChild(o); }
  if ($("vdiv1").options.length === 0 && st.vdivs)
    for (const id of ["vdiv1", "vdiv2"]) for (const v of st.vdivs) { const o = document.createElement("option"); o.value = v; o.textContent = fmtVdiv(v); $(id).appendChild(o); }
  for (const [id, val] of [["tdiv", st.tdiv_s], ["vdiv1", st.vdiv1], ["vdiv2", st.vdiv2]])
    if (val && document.activeElement !== $(id)) for (const o of $(id).options) if (Math.abs(+o.value - val) <= val * 1e-6) { o.selected = true; break; }
  for (const [id, val] of [["probe1", st.probe1], ["probe2", st.probe2]])
    if (document.activeElement !== $(id)) $(id).value = String(val || 1);
  for (const [id, val] of [["cpl1", st.cpl1], ["cpl2", st.cpl2]])
    if (document.activeElement !== $(id)) $(id).value = String(val || 0);
  // Sliders read/emit tip-referred volts; widen their range for high probes so
  // the full electrical span (±3.8 V in, +4.7 V trig) stays reachable.
  for (const [id, lo, hi] of [["off1", -3.8, 3.8], ["off2", -3.8, 3.8], ["lvl", -3.8, 4.7]]) {
    const p = id === "lvl" ? trigProbe() : probeOf(id === "off1" ? 1 : 2);
    $(id).min = (lo * p).toFixed(2); $(id).max = (hi * p).toFixed(2); $(id).step = (0.05 * p).toFixed(3);
  }
  if (!offDragging) {
    $("off1v").textContent = st.off1_v ? st.off1_v.toFixed(2) + " V" : "—";
    $("off2v").textContent = st.off2_v ? st.off2_v.toFixed(2) + " V" : "—";
    if (st.off1_v) $("off1").value = st.off1_v.toFixed(2);
    if (st.off2_v) $("off2").value = st.off2_v.toFixed(2);
  }
  updateStatusLine();
  const state = trigState();
  const chip = $("trigChip");
  chip.textContent = state;
  chip.dataset.state = state;
  refreshAria();
}

// The on-screen time/div — and its scale — FOLLOW the view zoom. The grid is
// always DIVX divisions, so when view.win is narrower/wider than the home 10-div
// screen (win_frac of the served record), the effective time/div scales by
// win_span/win_frac. Rebuilt on every redraw so it tracks zoom/pan live, not just
// on the 1s status poll. (Cursor Δt already scales by win_span.)
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
  const html =
    "<b>" + fmtTdiv(b) + "/div</b>" + zoomTxt + " · " + st.band + " · " + st.fps.toFixed(0) + " fps · seq <b>" + st.seq + "</b>" +
    " · cols " + reqCols + (st.mmap_drain ? "" : " (ioctl)") + (st.dead_runs ? " · DEAD " + st.dead_runs : "") +
    degradedTxt +
    " · cal:" + (st.cal_source || "?") + " · " + st.version;
  if (html !== lastLineHTML) { lastLineHTML = html; $("line").innerHTML = html; }
  const aria = "oscilloscope — trigger " + trigState() + ", " + fmtTdiv(b) + "/div";
  if (aria !== lastAria) { lastAria = aria; $("scope").setAttribute("aria-label", aria); }
}

// awaitFrame resolves when a fresh frame satisfying `pred` arrives (the global
// `frame` is updated by the poll loop), or after `timeout` ms. Used by autoset
// to converge across scale changes instead of relying on one stale capture.
function awaitFrame(pred, timeout = 2500) {
  return new Promise((resolve) => {
    const startSeq = frame ? frame.seq : 0;
    const t0 = Date.now();
    const tick = () => {
      if (frame && frame.seq !== startSeq && pred(frame)) return resolve(true);
      if (Date.now() - t0 > timeout) return resolve(false);
      setTimeout(tick, 60);
    };
    tick();
  });
}

function updateQualRow() {
  const t = +$("ttype").value;
  $("qualrow").style.display = t === 0 ? "none" : "flex";
  $("qp-pulse").style.display = t === 1 ? "flex" : "none";
  $("qp-slope").style.display = t === 2 ? "flex" : "none";
  $("qp-video").style.display = t === 3 ? "flex" : "none";
}

function updateAcqN() {
  const m = +$("acq").value, n = $("acqn");
  if (m === 1) { fillAcqN([4, 16, 32, 64, 128, 256], st ? st.avg_count : 16); n.style.display = ""; }
  else if (m === 2) { fillAcqN([3, 7, 15, 31, 63], st ? st.eres_len : 1); n.style.display = ""; }
  else n.style.display = "none";
}

function fillAcqN(opts, sel) {
  const n = $("acqn");
  if (n.dataset.opts !== opts.join()) { n.innerHTML = ""; for (const v of opts) { const o = document.createElement("option"); o.value = v; o.textContent = v; n.appendChild(o); } n.dataset.opts = opts.join(); }
  if (document.activeElement !== n && sel) n.value = sel;
}

// ---- view toggles ----
function setMode(m) {
  view.mode = m;
  for (const [id, mm] of [["mYT", "YT"], ["mXY", "XY"], ["mFFT", "FFT"]]) $(id).classList.toggle("on", mm === m);
  // Mode-dependent panels must be synced HERE, not only inside their drawer.
  // The peak boxes live in FFT and Y-T; hide them in X-Y. (redraw() re-shows and
  // repopulates them for the active mode.)
  updateFFTLists();
  // The navigator lives in Y-T (record overview) and FFT (spectrum
  // overview); toggling it changes the scope height, so re-measure
  // (resize() ends with redraw()).
  nav.style.display = (m === "YT" || m === "FFT") ? "" : "none";
  clearPersist(); resize();
}

function recompute() { computeDecode(); redraw(); }

function updateMathHint() {
  const h = $("mathHint");
  if (mathFn === "res1" || mathFn === "res2")
    h.textContent = "select the carrier peak(s) in the C" + (mathFn === "res1" ? 1 : 2) + " FFT list; the residual (minor waves) shows in purple";
  else h.textContent = "";
}

// ==== wiring ====

// ---- frame + status polling / transport / send ----


// showSuperseded stops this view and puts up a full-screen "refresh to reclaim"
// overlay. The device serves ONE live client at a time (to keep the engine's
// drain fast); a newer browser's /api/claim supersedes this one. Multiple tabs
// can stay open — refreshing any one reclaims control and supersedes the rest.
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
    b.onclick = () => location.reload();
    o.appendChild(b);
    document.body.appendChild(o);
  }
  o.style.display = "flex";
}

async function pollFrameBin() {
  if (superseded) return; // another browser took control; wait for a manual refresh
  if (frozen || document.hidden) { setTimeout(pollFrameBin, 90); return; } // idle tick; hidden tabs stop hitting the device
  try {
    const r = await fetch("/api/frame.bin?since=" + lastSeq + "&cols=" + reqCols + "&full=1&waitms=1000&epoch=" + myEpoch);
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
    // In FFT mode, keep a fresh full-record RAW frame for the spectrum source
    // (the display frame is an interpolated window on native-fast). Throttled;
    // fire-and-forget so it never paces the display loop.
    if (view.mode === "FFT" && !frozen && !(typeof sr !== "undefined" && sr.showing) && performance.now() - fftRawT > 150) fetchFftRaw();
    setTimeout(pollFrameBin, 10); // the server did the pacing; this only guards a hot loop
  } catch (e) {
    // Bad reply or network error (OTA app restart) — retry the SAME transport
    // with jittered backoff; the endpoint comes back at full speed.
    binFailures++;
    setTimeout(pollFrameBin, Math.min(2000, 250 * binFailures) + 250 * Math.random());
  }
}

// fetchFftRaw pulls one full-record raw frame (un-windowed, un-interpolated,
// carries sample_s) to source the FFT in FFT mode. Fire-and-forget + throttled
// by the caller; sets fftRaw + fftRawT, then a redraw picks it up.
async function fetchFftRaw() {
  if (fftRawBusy) return;
  fftRawBusy = true; fftRawT = performance.now();
  try {
    const r = await fetch("/api/frame.bin?raw=1&waitms=300");
    if (r.ok) { const f = decodeBinFrame(await r.arrayBuffer()); if (f && !f.unchanged && f.c1 && f.sample_s > 0) { fftRaw = f; if (view.mode === "FFT") redraw(); } }
  } catch (e) { /* transient — the next tick retries */ } finally { fftRawBusy = false; }
}

async function pollStatus() {
  if (superseded) return; // taken over by another browser; stop loading the device
  try { st = await (await fetch("/api/status")).json(); applyStatus(); }
  catch (e) { $("line").textContent = "no connection"; lastLineHTML = ""; } // reset the diff guard or a static status keeps "no connection" stuck
  // While a SINGLE is armed, poll fast so the self-stop on capture reaches the
  // UI promptly: the RUN/STOP button toggles off st.running, and a 1 s-stale
  // "running" shadow made a post-capture RUN click send STOP instead (the scope
  // would not resume). 250 ms closes that window; steady state stays 1 s.
  setTimeout(pollStatus, st && st.single ? 250 : 1000);
}

// ---- controls ----
async function send(control, value) {
  try { return await (await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control, value }) })).json(); }
  catch (e) { return { ok: false }; }
}
async function autoset() {
  if (autosetBusy) return;
  autosetBusy = true;
  const btn = $("autoset"); btn.classList.add("on");
  try {
    // trigger the device autoset (the hard-button path — one implementation)
    try { await fetch("/api/panel", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ button: "auto" }) }); }
    catch (e) { return; }
    // wait for it to converge: a measurable, non-envelope frame whose measured
    // frequency is stable across two reads (the sweep has settled on the native
    // band). Bounded so the button always releases.
    const has = m => m && m.vpp > 0.02;
    const meas = () => { const m = frame && (has(frame.m1) ? frame.m1 : (has(frame.m2) ? frame.m2 : null)); return m && !frame.is_env ? m.freq : null; };
    let prev = null, t0 = Date.now();
    while (Date.now() - t0 < 9000) {
      await awaitFrame(() => true, 700);
      const f = meas();
      if (f != null && f > 0 && prev != null && Math.abs(f - prev) <= prev * 0.03) break; // stable
      prev = f;
    }
    goHome();
    applyStatus();
  } finally {
    autosetBusy = false;
    $("autoset").classList.remove("on");
  }
}
