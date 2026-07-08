// app_core.js — frame/status apply, redraw dispatch, measurement/status UI updates (classic script; shares app.js globals).

function redraw() {
  refreshAria(); // keep aria-pressed in sync with view toggles (which call redraw)
  updateStatusLine(); // time/div follows the zoom → keep it live, not just per status poll
  if (view.mode === "XY") { clearPersist(); drawXY(); drawCursors(); drawBoxZoom(ctx); return; }
  if (view.mode === "FFT") { clearPersist(); drawFFT(); drawCursors(); drawNav(); drawBoxZoom(ctx); return; }
  if (view.persist && !frame?.is_env) {
    const pg = persistLayer();
    pg.fillStyle = "rgba(5,8,12,0.14)"; pg.fillRect(0, 0, CW, CH); // fade
    // draw new trace onto the persistence layer (no grid)
    if (frame) {
      if (view.c2) drawTrace(pg, frame.c2, C2COL, st ? st.zoom2 : 1);
      if (view.c1) drawTrace(pg, frame.c1, C1COL, st ? st.zoom1 : 1);
      drawMath(pg);
    }
    drawGrid(ctx);
    ctx.drawImage(persistCv, 0, 0);
    drawChannelMarkers(ctx);
    drawTrigMarkers(ctx);
  } else {
    drawYT(ctx);
  }
  drawYTPeaks(ctx);
  drawDecode(ctx);
  drawCursors();
  drawSrGate();
  if (window.zm) drawZones(ctx);
  drawNav();
  drawBoxZoom(ctx);
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
  // A full-height drag spans 255 codes ÷ vertical zoom (the 2 mV/5 mV detents
  // render magnified), times volts/code (already probe-scaled). Per channel.
  const z1 = (st && st.zoom1) || 1, z2 = (st && st.zoom2) || 1;
  const vspan = view.vwin.b - view.vwin.a; // a full-height drag spans the ZOOMED voltage range
  const vFull1 = 255 * vspan * (frame.vpc1 || 1 / 32) / z1, vFull2 = 255 * vspan * (frame.vpc2 || 1 / 32) / z2;
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
  if (!userZoomed) { const h = homeWindow(f); view.win.a = h.a; view.win.b = h.b; view.vwin.a = 0; view.vwin.b = 1; }
  computeDecode(); redraw(); updateMeas(); updateCursors();
}

function fallbackToJSON() {
  if (transport === "json") return;
  transport = "json";
  pollFrame(++jsonGen); // bump the token so any older chain dies on its next tick
  setTimeout(probeBin, 30000);
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
  const html =
    "<b>" + fmtTdiv(b) + "/div</b>" + zoomTxt + " · " + st.band + " · " + st.fps.toFixed(0) + " fps · seq <b>" + st.seq + "</b>" +
    " · cols " + reqCols + (st.mmap_drain ? "" : " (ioctl)") + (st.dead_runs ? " · DEAD " + st.dead_runs : "") +
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

