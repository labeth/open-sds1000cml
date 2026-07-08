// app_wire.js — top-level event wiring + init, loaded AFTER app.js so every definition exists (classic script; shares app.js globals).

"use strict";
window.addEventListener("resize", resize);




// One delegated handler per channel's (never-replaced) tbody. Toggling by the
// row's own frequency is robust to the list re-sorting between render and click.
for (const ch of [1, 2]) {
  $("fftBody" + ch).addEventListener("click", ev => {
    const tr = ev.target.closest(".pk");
    if (!tr || tr.dataset.freq == null) return;
    togglePeakCh(ch, +tr.dataset.freq);
  });
  $("fftClear" + ch).onclick = () => clearPeaksCh(ch);
  $("fftN" + ch).oninput = () => {
    const v = Math.round(+$("fftN" + ch).value);
    maxPeaks = Number.isFinite(v) && v >= 1 ? Math.min(64, v) : 8;
    $("fftN1").value = maxPeaks; $("fftN2").value = maxPeaks; // shared count
    redraw();
  };
}

scope.addEventListener("pointerdown", ev => {
  if (ev.detail > 1) return; // the 2nd click of a double-click must not grab a cursor/marker (dblclick resets zoom)
  if (ev.shiftKey && view.mode === "YT" && st && frame) { // Shift+click = set trigger level here
    const vpc = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 32);
    const volts = (codeAtY(Math.max(0, Math.min(1, ptToNorm(ev).y)), 1) - 128) * vpc;
    st.trig_volts = volts; $("lvl").value = volts.toFixed(2); $("lvlv").textContent = volts.toFixed(2) + " V";
    send("triglevelcode", Math.round(31434 - 938 * volts / trigProbe())); redraw();
    return;
  }
  if (typeof zmPointerDown === "function" && zmPointerDown(ptToNorm(ev))) { // armed zone drawing
    scope.setPointerCapture(ev.pointerId);
    return;
  }
  if (view.mode === "FFT") { // drag = frequency box zoom; plain click (on up) toggles the nearest peak
    boxZoom.active = true; boxZoom.moved = false;
    boxZoom.sx = ev.clientX; boxZoom.sy = ev.clientY;
    boxZoom.ex = ev.clientX; boxZoom.ey = ev.clientY;
    scope.setPointerCapture(ev.pointerId);
    return;
  }
  const hit = markerHit(ptToNorm(ev));   // markers are draggable regardless of cursor mode
  if (hit) {
    mk = hit;
    if (hit.kind === "level") lvlDragging = true; else offDragging = true;
    scope.setPointerCapture(ev.pointerId);
    moveMarker(ev);
    return;
  }
  if (srGate.on && view.mode === "YT") { // grab a gate marker if the pointer is near one
    const p = ptToNorm(ev), span = view.win.b - view.win.a || 1;
    const af = (srGate.a - view.win.a) / span, bf = (srGate.b - view.win.a) / span;
    const da = Math.abs(p.x - af), db = Math.abs(p.x - bf);
    if (Math.min(da, db) < 0.02) {
      srGate.drag = da <= db ? "a" : "b";
      scope.setPointerCapture(ev.pointerId);
      moveSrGate(ev);
      return;
    }
  }
  if (view.cursors) {
    const p = ptToNorm(ev);
    const near = (a, b) => Math.abs(a - b) < 0.025;
    let drag = null;
    if (near(p.y, cur.v1)) drag = "v1"; else if (near(p.y, cur.v2)) drag = "v2";
    else if (near(p.x, cur.t1)) drag = "t1"; else if (near(p.x, cur.t2)) drag = "t2";
    if (drag) {
      cur.drag = drag;
      scope.setPointerCapture(ev.pointerId);
      moveCursor(ev);
      return;
    }
  }
  if (view.mode === "YT") { // empty area: rubber-band box zoom (time + voltage)
    boxZoom.active = true; boxZoom.moved = false;
    boxZoom.sx = ev.clientX; boxZoom.sy = ev.clientY;
    boxZoom.ex = ev.clientX; boxZoom.ey = ev.clientY;
    scope.setPointerCapture(ev.pointerId);
  }
});
scope.addEventListener("pointermove", ev => {
  if (view.mode === "FFT" && !boxZoom.active) {
    const p = ptToNorm(ev);
    fftHover.on = true; fftHover.x = Math.max(0, Math.min(1, p.x)); fftHover.y = Math.max(0, Math.min(1, p.y));
    if (!fftHoverRaf) fftHoverRaf = requestAnimationFrame(() => { fftHoverRaf = 0; redraw(); });
    return;
  }
  if (boxZoom.active) {
    boxZoom.ex = ev.clientX; boxZoom.ey = ev.clientY;
    if (Math.abs(boxZoom.ex - boxZoom.sx) + Math.abs(boxZoom.ey - boxZoom.sy) > 8) boxZoom.moved = true;
    if (boxZoom.moved) redraw();
    return;
  }
  if (typeof zmPointerMove === "function" && zmPointerMove(ptToNorm(ev))) return;
  if (mk) { moveMarker(ev); return; }
  if (srGate.drag) { moveSrGate(ev); return; }
  if (cur.drag) { moveCursor(ev); return; }
  const h = markerHit(ptToNorm(ev));    // hover affordance
  scope.style.cursor = h ? "ns-resize" : (view.cursors ? "crosshair" : "default");
});
scope.addEventListener("pointerleave", () => {
  if (fftHover.on) { fftHover.on = false; if (view.mode === "FFT") redraw(); }
});
scope.addEventListener("pointerup", ev => {
  if (typeof zmPointerUp === "function" && zmPointerUp()) return;
  if (boxZoom.active) {
    const wasBox = boxZoom.moved;
    boxZoom.active = false; boxZoom.moved = false;
    if (wasBox) {
      applyBoxZoom();
      redraw();
    } else if (view.mode === "FFT") { // plain click: toggle the nearest peak
      const fw = view.fwin;
      const clickedFreq = (fw.a + ptToNorm(ev).x * (fw.b - fw.a)) * displayNyq();
      let best = null;
      for (const ch of [1, 2]) {
        const idx = nearestPeak(fftCh[ch].peaks, clickedFreq);
        if (idx < 0) continue;
        const dx = Math.abs(fftCh[ch].peaks[idx].freq - clickedFreq);
        if (!best || dx < best.dx) best = { ch, freq: fftCh[ch].peaks[idx].freq, dx };
      }
      if (best) togglePeakCh(best.ch, best.freq);
    }
    return;
  }
  if (mk) { commitMarker(); lvlDragging = offDragging = false; mk = null; }
  cur.drag = null;
  srGate.drag = null;
});
nav.addEventListener("pointerdown", ev => {
  if (view.mode !== "YT" && view.mode !== "FFT") return;
  if (view.mode === "YT") userZoomed = true;
  const w = navWin();
  let f = navFrac(ev), s = w.b - w.a;
  if (f < w.a || f > w.b) { const na = Math.max(0, Math.min(1 - s, f - s / 2)); w.a = na; w.b = na + s; redraw(); }
  navDrag.active = true; navDrag.grab = f; navDrag.a0 = w.a; navDrag.b0 = w.b;
  nav.setPointerCapture(ev.pointerId);
});
nav.addEventListener("pointermove", ev => {
  if (!navDrag.active) return;
  const w = navWin();
  const f = navFrac(ev), s = navDrag.b0 - navDrag.a0;
  const na = Math.max(0, Math.min(1 - s, navDrag.a0 + (f - navDrag.grab)));
  w.a = na; w.b = na + s;
  redraw();
});
nav.addEventListener("pointerup", () => navDrag.active = false);
nav.addEventListener("dblclick", () => {
  if (view.mode === "FFT") { view.fwin.a = 0; view.fwin.b = 1; redraw(); }
  else goHome();
});

scope.addEventListener("dblclick", () => {
  if (view.mode === "YT") goHome();
  else if (view.mode === "FFT") { view.fwin.a = 0; view.fwin.b = 1; redraw(); }
}); // reset zoom to full
scope.addEventListener("wheel", ev => {
  if (view.mode === "FFT") { // zoom the frequency axis about the pointer; shift = pan
    ev.preventDefault();
    const d = ev.deltaY || ev.deltaX;
    const fw = view.fwin, span = fw.b - fw.a;
    if (ev.shiftKey) {
      const na = Math.max(0, Math.min(1 - span, fw.a + span * 0.15 * (d < 0 ? -1 : 1)));
      view.fwin.a = na; view.fwin.b = na + span;
      redraw();
      return;
    }
    const sx = Math.max(0, Math.min(1, ptToNorm(ev).x));
    const fx = fw.a + sx * span;
    const ns = Math.max(1e-5, Math.min(1, span * Math.exp(d * 0.001)));
    let na = fx - sx * ns, nb = na + ns;
    if (na < 0) { na = 0; nb = ns; }
    if (nb > 1) { nb = 1; na = 1 - ns; }
    view.fwin.a = na; view.fwin.b = nb;
    redraw();
    return;
  }
  if (view.mode !== "YT") return;
  ev.preventDefault();
  // Chromium remaps Shift+vertical-wheel onto the horizontal axis (deltaX, deltaY=0),
  // so read whichever axis carries the notch.
  const d = ev.deltaY || ev.deltaX;
  if (ev.ctrlKey || ev.metaKey) { stepTdiv(d < 0 ? -1 : 1); return; } // Ctrl+wheel = time/div (up = faster)
  if (ev.shiftKey) { panWin(d < 0 ? -1 : 1); return; }                 // Shift+wheel = pan left/right
  userZoomed = true;
  const w = view.win, span = w.b - w.a, sx = Math.max(0, Math.min(1, ptToNorm(ev).x));
  const fx = w.a + sx * span;
  const ns = Math.max(MINSPAN, Math.min(1, span * Math.exp(ev.deltaY * 0.001)));
  let na = fx - sx * ns, nb = na + ns;
  if (na < 0) { na = 0; nb = ns; }
  if (nb > 1) { nb = 1; na = 1 - ns; }
  setWin(na, nb);
}, { passive: false });


async function pollFrameBin() {
  if (transport !== "bin") return;
  if (frozen || document.hidden) { setTimeout(pollFrameBin, 90); return; } // idle tick; hidden tabs stop hitting the device
  try {
    const r = await fetch("/api/frame.bin?since=" + lastSeq + "&cols=" + reqCols + "&full=1&waitms=1000");
    if (!r.ok) { fallbackToJSON(); return; }
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) { fallbackToJSON(); return; } // never render a reply that failed validation
    binFailures = 0;
    // Re-check frozen AFTER the await: the request parks server-side for up
    // to 1 s, and a freeze/capture-review click mid-flight must not have its
    // snapshot clobbered by the late reply. lastSeq stays put, so unfreezing
    // immediately long-polls the newest frame.
    if (!f.unchanged && !frozen) applyFrame(f);
    setTimeout(pollFrameBin, 10); // the server did the pacing; this only guards a hot loop
  } catch (e) {
    binFailures++;
    setTimeout(pollFrameBin, Math.min(2000, 250 * binFailures) + 250 * Math.random());
  }
}


async function probeBin() {
  if (transport === "bin") return;
  try {
    const r = await fetch("/api/frame.bin?since=0&cols=" + reqCols + "&full=1");
    if (r.ok && decodeBinFrame(await r.arrayBuffer()) !== null) {
      transport = "bin"; // pollFrame sees this and stops rescheduling
      pollFrameBin();
      return;
    }
  } catch (e) { /* still down */ }
  setTimeout(probeBin, 30000);
}

// Legacy JSON poll — the fallback transport and the ?transport=json debug
// path. The gen token guarantees at most ONE live chain: a transport flap
// (fallback → upgrade → fallback) spawns a new chain with a new token and
// any in-flight older chain exits on its next tick instead of accumulating.
async function pollFrame(gen) {
  if (gen === undefined) gen = jsonGen;
  if (transport !== "json" || gen !== jsonGen) return;
  if (!frozen) {
    try {
      const r = await fetch("/api/frame?since=" + lastSeq + "&cols=" + reqCols + "&full=1");
      const f = await r.json();
      if (!f.unchanged && !frozen) applyFrame(f);
    } catch (e) { /* keep last */ }
  }
  setTimeout(() => pollFrame(gen), 90);
}
async function pollStatus() {
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
$("autoset").onclick = autoset;
$("run").onclick = () => { const on = !(st && st.running); send("run", on ? 1 : 0); if (st) { st.running = on; applyStatus(); } };
$("single").onclick = () => {
  send("single", 1);
  if (st) { st.norm = st.running = st.single = true; applyStatus(); }
  // A one-shot self-stops on capture. Poll fast until it does so the RUN button
  // — which toggles off st.running — sees the stop immediately; otherwise a
  // post-capture RUN click reads a stale "running" and sends STOP (scope would
  // not resume). Bounded; the steady 1 s poll takes over after.
  let n = 0;
  const chk = async () => {
    try { st = await (await fetch("/api/status")).json(); applyStatus(); } catch (e) {}
    if (st && st.running && st.single && n++ < 50) setTimeout(chk, 200);
  };
  setTimeout(chk, 200);
};
$("tpos").oninput = () => send("trigpos", +$("tpos").value);
$("mode").onclick = () => { const on = !(st && st.norm); send("norm", on ? 1 : 0); if (st) { st.norm = on; applyStatus(); } };
$("slope").onclick = () => { const r = !(st && st.trig_rising); send("trigslope", r ? 1 : 0); if (st) { st.trig_rising = r; applyStatus(); } };
$("source").onclick = () => { const c = st && st.trig_source === 1 ? 0 : 1; send("trigsource", c); if (st) { st.trig_source = c; applyStatus(); } };
$("ets").onclick = () => { const on = !(st && st.ets); send("ets", on ? 1 : 0); if (st) { st.ets = on; applyStatus(); } };
$("tdiv").onchange = () => send("tdiv", +$("tdiv").value);
$("vdiv1").onchange = () => send("vdiv1", +$("vdiv1").value);
$("vdiv2").onchange = () => send("vdiv2", +$("vdiv2").value);
$("probe1").onchange = () => send("probe1", +$("probe1").value);
$("probe2").onchange = () => send("probe2", +$("probe2").value);
$("cpl1").onchange = () => send("coupling1", +$("cpl1").value);
$("cpl2").onchange = () => send("coupling2", +$("cpl2").value);
$("refSaveA").onclick = () => saveRef("A");
$("refSaveB").onclick = () => saveRef("B");
$("holdoff").onchange = () => send("holdoff", +$("holdoff").value);
for (const [rng, lbl, ctl, ch] of [["off1", "off1v", "offset1", 1], ["off2", "off2v", "offset2", 2]]) {
  $(rng).oninput = () => { offDragging = true; $(lbl).textContent = (+$(rng).value).toFixed(2) + " V"; };
  $(rng).onchange = () => { offDragging = false; send(ctl, +$(rng).value / probeOf(ch)); };
}
$("lvl").oninput = () => { lvlDragging = true; $("lvlv").textContent = (+$("lvl").value).toFixed(2) + " V"; };
$("lvl").onchange = () => { lvlDragging = false; send("triglevelcode", Math.round(31434 - 938 * (+$("lvl").value) / trigProbe())); };

$("ttype").onchange = () => { send("trigtype", +$("ttype").value); if (st) st.trig_type = +$("ttype").value; updateQualRow(); };
async function sendParams(control, extra) {
  try { await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(Object.assign({ control, value: 0 }, extra)) }); } catch (e) {}
}
for (const id of ["p-lvl", "p-min", "p-max", "p-cond"]) $(id).onchange = sendPulse;
for (const id of ["s-lo", "s-hi", "s-min", "s-max", "s-cond"]) $(id).onchange = sendSlope;
for (const id of ["v-std", "v-line", "v-neg"]) $(id).onchange = sendVideo;

$("acq").onchange = () => { send("acqmode", +$("acq").value); if (st) st.acq_mode = +$("acq").value; updateAcqN(); };
$("acqn").onchange = () => send(+$("acq").value === 1 ? "avgcount" : "eres", +$("acqn").value);
$("memdepth").onchange = () => { send("memdepth", +$("memdepth").value); userZoomed = false; }; // deeper = more to scroll, fewer fps

$("mYT").onclick = () => setMode("YT");
$("mXY").onclick = () => setMode("XY");
$("mFFT").onclick = () => setMode("FFT");
$("tPersist").onclick = () => { view.persist = !view.persist; $("tPersist").classList.toggle("on", view.persist); clearPersist(); redraw(); };
$("tCursors").onclick = () => { view.cursors = !view.cursors; $("tCursors").classList.toggle("on", view.cursors); updateCursors(); redraw(); };
$("tC1").onclick = () => { view.c1 = !view.c1; $("tC1").classList.toggle("on", view.c1); redraw(); };
$("tC2").onclick = () => { view.c2 = !view.c2; $("tC2").classList.toggle("on", view.c2); redraw(); };
$("freeze").onclick = () => { frozen = !frozen; $("freeze").classList.toggle("on", frozen); };

(function initDecode() {
  for (const sel of document.querySelectorAll(".rolesel")) {
    sel.innerHTML = '<option value="1">C1</option><option value="2">C2</option>';
  }
  $("decSda").value = "2"; $("decData").value = "2"; $("decLine").value = "1";
  $("decDetect").onclick = runAutodetect;
  $("decProto").onchange = () => { dcfg.proto = $("decProto").value; setDetectMsg(""); updateDecodePanel(); recompute(); };
  $("decScl").onchange = () => { dcfg.scl = +$("decScl").value; recompute(); };
  $("decSda").onchange = () => { dcfg.sda = +$("decSda").value; recompute(); };
  $("decClk").onchange = () => { dcfg.clk = +$("decClk").value; recompute(); };
  $("decData").onchange = () => { dcfg.data = +$("decData").value; recompute(); };
  $("decLine").onchange = () => { dcfg.line = +$("decLine").value; recompute(); };
  $("decAuto").onclick = () => { dcfg.auto = !dcfg.auto; $("decAuto").classList.toggle("on", dcfg.auto); recompute(); };
  $("decThr").oninput = () => { dcfg.auto = false; $("decAuto").classList.remove("on"); $("decThrV").textContent = $("decThr").value; recompute(); };
  $("decBaud").onchange = () => { dcfg.baud = Math.max(0, +$("decBaud").value | 0); recompute(); }; // 0 = auto-baud
  $("decBits").onchange = () => { dcfg.bits = Math.max(5, Math.min(9, +$("decBits").value | 0)); recompute(); };
  $("decParity").onchange = () => { dcfg.parity = $("decParity").value; recompute(); };
  $("decCpol").onchange = () => { dcfg.cpol = +$("decCpol").value; recompute(); };
  $("decCpha").onchange = () => { dcfg.cpha = +$("decCpha").value; recompute(); };
  $("decMsb").onchange = () => { dcfg.msb = $("decMsb").value === "1"; recompute(); };
  $("decFmt").onchange = () => { dcfg.fmt = $("decFmt").value; recompute(); }; // hex / ascii / both
  $("decStream").onclick = () => {
    dcfg.stream = !dcfg.stream; $("decStream").classList.toggle("on", dcfg.stream);
    dcfg.hist = []; dcfg.lastStreamSeq = 0;
    fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "stream", value: dcfg.stream ? 1 : 0 }) }).catch(() => {});
    updateDecodeResults();
  };
  $("decHistClear").onclick = () => { dcfg.hist = []; dcfg.lastStreamSeq = 0; updateDecodeResults(); };
  $("decWatch").onclick = () => { dcfg.watch = !dcfg.watch; $("decWatch").classList.toggle("on", dcfg.watch); updateCaptureList(); };
  $("decWatchErr").onchange = () => { dcfg.watchErr = $("decWatchErr").checked; };
  $("decWatchMatch").oninput = () => { dcfg.watchMatch = $("decWatchMatch").value; };
  $("captureList").addEventListener("click", ev => { const d = ev.target.closest(".cap"); if (d) reviewCapture(+d.dataset.i); });
  $("capLive").onclick = reviewLive;
  $("capClear").onclick = () => { dcfg.captures = []; dcfg.reviewIdx = -1; dcfg.lastCapKey = ""; if (frozen) reviewLive(); updateCaptureList(); };
  $("capCopy").onclick = () => {
    const t = dcfg.captures.map(c => new Date(c.t).toISOString() + " #" + c.seq + " [" + c.reason + "] " + c.text).join("\n");
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).catch(() => {});
    else { // plain-HTTP fallback (no navigator.clipboard off https/localhost)
      const ta = document.createElement("textarea");
      ta.value = t; ta.style.position = "fixed"; ta.style.opacity = "0";
      document.body.appendChild(ta); ta.select();
      try { document.execCommand("copy"); } catch (e) {}
      document.body.removeChild(ta);
    }
    $("capCopy").textContent = "copied"; setTimeout(() => $("capCopy").textContent = "copy", 900);
  };
  $("decodeCopy").onclick = () => {
    const t = $("decodeText").value;
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(t).catch(() => {});
    else { $("decodeText").select(); try { document.execCommand("copy"); } catch (e) {} }
    $("decodeCopy").textContent = "copied"; setTimeout(() => $("decodeCopy").textContent = "copy", 900);
  };
  updateDecodePanel();
})();
async function srLoop(gen) {
  if (!sr.armed || gen !== sr.gen) return;
  try {
    const r = await fetch("/api/frame.bin?since=" + sr.lastSeq + "&waitms=1000&raw=1");
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("decode");
    // Re-check AFTER the await: the fetch parks server-side for up to 1 s
    // and a reset/stop click mid-flight must not resurrect the stack.
    if (!sr.armed || gen !== sr.gen) return;
    srFails = 0;
    if (!f.unchanged && f.seq !== sr.lastSeq) {
      sr.lastSeq = f.seq;
      srIngest(f);
    }
    setTimeout(() => srLoop(gen), 10);
  } catch (e) {
    // Failure (outage, protocol) — back off like the display loop instead of
    // hammering a struggling device at the 10 ms tick.
    srFails++;
    setTimeout(() => srLoop(gen), Math.min(2000, 250 * srFails) + 250 * Math.random());
  }
}


$("srArm").onclick = () => {
  if (sr.armed) { srStop("stopped"); return; }
  if (!st || (st.band !== "native-fast" && st.band !== "decimated")) {
    srStatus("unsupported band (" + (st ? st.band : "?") + ") — use a native or decimated t/div");
    return;
  }
  if (typeof decodeBinFrame !== "function" || typeof srNew !== "function") {
    srStatus("superres/binframe scripts missing");
    return;
  }
  if (typeof ej !== "undefined" && ej.armed) ejStop("stopped — superres armed (one raw consumer)");
  sr.st = null; sr.meta = null; sr.lastSeq = 0; sr.savedWin = null;
  sr.stopMode = $("srStopMode").value;
  sr.stopVal = +$("srStopVal").value || 0;
  sr.lastBits = 0;
  // GATE markers set → seed the first frame as the reference with THAT region
  // (works from SINGLE or free-run). Otherwise: frozen → lock the frozen frame;
  // running → auto-adopt.
  sr.lockRef = srGate.on || !!(st && !st.running);
  // Markers live on the DISPLAY record (a windowed/decimated, trigger-anchored
  // serve); the stacker feeds on the RAW record. The trigger edge is the common
  // anchor, so convert markers → SECONDS from the display's edge now, and map
  // onto the raw frame's own edge at seed time. Applying display fractions to
  // the raw record directly is wrong (it stacked ~10 waves for an edge-wide mark).
  sr.gateDt = null;
  if (srGate.on) {
    if (frame && frame.cols > 1 && frame.col_span_s > 0 && !frame.is_env) {
      const cols = frame.cols, spc = frame.col_span_s / cols; // seconds per display column
      const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
      const cLo = Math.min(srGate.a, srGate.b) * (cols - 1);
      const cHi = Math.max(srGate.a, srGate.b) * (cols - 1);
      sr.gateDt = { lo: (cLo - edgeCol) * spc, hi: (cHi - edgeCol) * spc };
    } else {
      srStatus("gate needs a live/frozen trace on screen"); return;
    }
  }
  sr.ch = +$("srCh").value || 0; // latched — a mid-capture change stops the stack
  // "both" (0) aligns on the trigger-source channel; C1/C2 lock the alignment.
  sr.alignCh = sr.ch === 2 ? 1 : sr.ch === 1 ? 0 : (st && st.trig_source === 1 ? 1 : 0);
  sr.dither.on = $("srDither").checked;
  sr.dither.base = (st && (sr.alignCh === 1 ? st.off2_v : st.off1_v)) || 0;
  sr.dither.idx = 0; sr.dither.pending = 0; sr.dither.framesAtStep = 0;
  sr.t0 = performance.now();
  sr.armed = true;
  $("srArm").textContent = "STOP";
  $("srArm").classList.add("on");
  srStatus(srGate.on ? "stacking… (gate = markers)" : "stacking…");
  srLoop(++sr.gen);
};

$("srReset").onclick = () => { if (sr.showing) srExitView(); srStop(); sr.st = null; sr.meta = null; sr.savedWin = null; srStatus("idle"); };

// AUTOGATE: always (re-)place the markers on the best feature in the current
// view, then show them. GATE: show/hide toggle — auto-places only the first time;
// after that the markers are wherever you dragged them (the only truth).
$("srAutoGate").onclick = () => {
  srGateDefaultFromView();
  srGate.placed = true;
  srGate.on = true;
  $("srGate").classList.add("on");
  srStatus("gate placed — drag to adjust, then ARM");
  redraw();
};
$("srGate").onclick = () => {
  srGate.on = !srGate.on;
  if (srGate.on && !srGate.placed) { srGateDefaultFromView(); srGate.placed = true; }
  $("srGate").classList.toggle("on", srGate.on);
  if (srGate.on) srStatus("gate on — drag the markers over the feature, then ARM");
  redraw();
};

// Stop-mode selector: adapt the target field's default + step to the units.
$("srStopMode").onchange = () => {
  const m = $("srStopMode").value, v = $("srStopVal");
  const d = { bits: [4, 0.5], stacks: [500, 50], time: [60, 10] }[m];
  v.disabled = !d;
  if (d) { v.value = d[0]; v.step = d[1]; }
};

$("srShow").onclick = () => {
  if (sr.showing) { srExitView(); return; }
  if (!sr.st || !sr.st.frames) { srStatus("nothing stacked yet"); return; }
  const res = srResult(sr.st);
  const n = sr.st.n;
  const dt = sr.st.sampleS / sr.st.K;
  const meas = (mean, ch) => mean ? srMeasure(mean, sr.st.c[ch].vpc, sr.st.c[ch].offV, dt) : null;
  // res.mean is the ALIGN channel's stack, res.mean2 the other — map them back to
  // the PHYSICAL channels so the review honors the selection (align C2 must show
  // as the cyan C2 trace, not swap into C1).
  const c1m = sr.st.align === 0 ? res.mean : res.mean2;
  const c2m = sr.st.align === 0 ? res.mean2 : res.mean;
  // A gated stack's mean spans only the gate (gridL raw samples), not the whole
  // record — size the time axis and edge anchor to the grid actually served.
  const spanCols = sr.st.gated ? sr.st.gridL : n;
  const edgeFrac = sr.st.gated
    ? (sr.st.refEdgeX >= sr.st.gateLo && sr.st.refEdgeX < sr.st.gateHi ? (sr.st.refEdgeX - sr.st.gateLo) / sr.st.gridL : -1)
    : (sr.st.edgeX >= 0 ? sr.st.edgeX / n : -1);
  frame = {
    seq: frame ? frame.seq : 0, unchanged: false,
    c1: c1m, c2: c2m, is_env: false,
    cols: res.mean.length, col_span_s: spanCols * sr.st.sampleS,
    tdiv_s: sr.meta.tdiv_s, displayed_sdiv_s: sr.meta.tdiv_s,
    vpc1: sr.st.c[0].vpc, vpc2: sr.st.c[1].vpc,
    off1_v: sr.st.c[0].offV, off2_v: sr.st.c[1].offV,
    edge_frac: edgeFrac,
    win_frac: 1, depth: 0,
    m1: meas(c1m, 0), m2: meas(c2m, 1),
    clip1: false, clip2: false,
    trigd: true, interp: false, coherent: true, ptp: 0,
  };
  sr.showing = true;
  $("srShow").classList.add("on");
  frozen = true; $("freeze").classList.add("on");
  if (sr.savedWin) {
    view.win.a = sr.savedWin.a; view.win.b = sr.savedWin.b; userZoomed = sr.savedWin.zoomed;
  } else {
    view.win.a = 0; view.win.b = 1; userZoomed = true; // whole stack; wheel-zoom into the detail
  }
  lastSig = "superres"; // poison the acq signature so returning to live re-homes
  computeDecode(); redraw(); updateMeas(); updateCursors();
  srUpdateStats(true);
};

$("srFit").onclick = () => {
  if (!sr.st || !sr.st.frames) { srStatus("nothing stacked yet"); return; }
  const res = srResult(sr.st);
  const am = res.mean; // res.mean IS the align channel's stack
  const ac = sr.st.c[sr.st.align];
  const fit = srModelFit(am, sr.st.K, sr.st.sampleS, { spectrum, detectPeaks }, 6);
  if (!fit) { srStatus("model fit failed (need a fuller stack)"); return; }
  refs.B = {
    c1: Array.from(fit.synth(Math.min(am.length, 16384))),
    c2: null, vpc1: ac.vpc, vpc2: 1 / 32, off1: ac.offV, off2: 0, show: true,
    // only overlay on a matching time base; a gated stack spans just the gate
    srSpanS: (sr.st.gated ? sr.st.gridL : sr.st.n) * sr.st.sampleS,
  };
  updateRefRows(); redraw();
  srStatus("model → REF B: " + fit.freqs.map(f => eng(f, "Hz", 3)).join(", "));
};

$("ePNG").onclick = () => {
  const a = document.createElement("a");
  a.download = "scope-" + (frame ? frame.seq : 0) + ".png";
  a.href = scope.toDataURL("image/png"); a.click();
};
$("eCSV").onclick = () => {
  if (!frame || !frame.c1) return;
  const dt = (frame.col_span_s || 0) / frame.c1.length;
  const vpc1 = frame.vpc1 || (1 / 32), vpc2 = frame.vpc2 || (1 / 32);
  const o1 = frame.off1_v || 0, o2 = frame.off2_v || 0;
  const toV = (code, vpc, off) => (code === undefined || code < 0 ? "" : ((code - 128) * vpc - off).toExponential(6));
  const c2 = frame.c2;
  // Decimate huge arrays: a superres stack's K× fine grid is interpolation,
  // so exporting every point (>1M) means ~0.5 s of number-formatting for
  // sub-sample rows that carry no new data. Cap at ~131072 rows — beyond the
  // raw record's real content — and note it in the header.
  const N = frame.c1.length, CAP = 131072;
  const step = N > CAP ? Math.ceil(N / CAP) : 1;
  const rowsN = Math.ceil(N / step);
  const rows = new Array(rowsN + 1);
  rows[0] = "# open-sds1000cml capture seq=" + frame.seq +
    " tdiv_s=" + (frame.tdiv_s || 0) + " probe_c1=" + (st ? st.probe1 || 1 : 1) +
    " probe_c2=" + (st ? st.probe2 || 1 : 1) +
    (step > 1 ? " decimated=" + step + "x_of_" + N + "_pts" : "") + "\nt_s,c1_v,c2_v";
  let r = 1;
  for (let i = 0; i < N; i += step)
    rows[r++] = (i * dt).toExponential(6) + "," + toV(frame.c1[i], vpc1, o1) + "," +
      toV(c2 ? c2[i] : undefined, vpc2, o2);
  const a = document.createElement("a");
  a.download = "scope-" + frame.seq + ".csv";
  a.href = URL.createObjectURL(new Blob([rows.join("\n") + "\n"], { type: "text/csv" })); a.click();
};
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { $("help").classList.remove("show"); return; }
  if (editableFocused() || e.ctrlKey || e.metaKey || e.altKey) return;
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  const m = KEYMAP.find(x => x.key === k || x.key === e.key);
  if (m) { e.preventDefault(); m.run(); }
});
$("help").onclick = (e) => { if (e.target.id === "help") $("help").classList.remove("show"); }; // click backdrop closes
$("mathFn").onchange = () => { mathFn = $("mathFn").value; updateMathHint(); redraw(); };
$("panelToggle").onclick = () => { document.body.classList.toggle("dock-toggled"); resize(); }; // collapse/expand dock
// Minimise/expand an individual card by clicking its title (ignore header controls).
$("dock").addEventListener("click", ev => {
  // ignore header controls, the action cluster, and hint text (e.g. "click to toggle")
  if (ev.target.closest("button, input, select, label, textarea, a, .subtle, .card-actions")) return;
  const h3 = ev.target.closest("h3");
  if (h3 && h3.parentElement.classList.contains("card")) h3.parentElement.classList.toggle("collapsed");
});

resize();
// If binframe.js failed to load (dropped subresource fetch, OTA restart
// mid-page-load), decodeBinFrame is undefined — pollFrameBin would throw and
// retry forever mistaking it for a network error. Start on JSON instead.
if (typeof decodeBinFrame !== "function") transport = "json";
if (transport === "bin") pollFrameBin(); else pollFrame();
pollStatus();

$("ejArm").onclick = () => {
  if (ej.armed) { ejStop("stopped"); return; }
  if (!st || (st.band !== "native-fast" && st.band !== "decimated")) {
    ejStatus("unsupported band (" + (st ? st.band : "?") + ") — use a native/decimated t/div");
    return;
  }
  if (typeof ejNew !== "function" || typeof decodeBinFrame !== "function") { ejStatus("eyejitter/binframe scripts missing"); return; }
  if (sr.armed) srStop("stopped — eye/jitter armed (one raw consumer)");
  ej.st = ejNew({});
  ej.lastSeq = 0; ej.gen++; ej.fails = 0;
  ej.ch = 0; ej.vpc0 = 0; ej.incons = 0;
  ej.armed = true;
  $("ejArm").textContent = "STOP";
  $("ejArm").classList.add("on");
  ejStatus("locking…");
  ejLoop(ej.gen);
};
$("ejReset").onclick = () => { ej.st = ejNew({}); ej.lastUi = 0; ejRender(true); ejStatus(ej.armed ? "reset — locking…" : "idle"); };

async function ejLoop(gen) {
  if (!ej.armed || gen !== ej.gen) return;
  try {
    const r = await fetch("/api/frame.bin?since=" + ej.lastSeq + "&waitms=1000&raw=1");
    if (!r.ok) throw new Error("http " + r.status);
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f === null) throw new Error("decode");
    if (!ej.armed || gen !== ej.gen) return; // stop clicked mid-flight
    ej.fails = 0;
    if (!f.unchanged && f.seq !== ej.lastSeq) {
      ej.lastSeq = f.seq;
      ejIngest(f);
    }
    setTimeout(() => ejLoop(gen), 10);
  } catch (e) {
    ej.fails++;
    setTimeout(() => ejLoop(gen), Math.min(2000, 250 * ej.fails) + 250 * Math.random());
  }
}
$("ejEye").onclick = () => ejOpenBig("eye");
$("ejHist").onclick = () => ejOpenBig("hist");
$("ejSpec").onclick = () => ejOpenBig("spec");
$("ejBigWrap").onclick = () => $("ejBigWrap").classList.add("hidden");


// ---- zone trigger + mask testing (engine-side; this is the config surface) ----
// window property (not const): the scope draw path runs during load BEFORE
// this glue executes — a lexical binding would be a TDZ landmine there.
window.zm = {
  zones: [],      // client copy for rendering + editing: engine-format objects
  mask: null,     // {lo, hi, win} client copy for rendering
  drawArmed: false, drawA: null, drawB: null,
  failMark: null, // {frac, code} violation marker on a gallery frame
  lastRing: -1,
  lastSkip: -1, lastZoneSkip: -1, // skip-delta trackers (stale-test warnings)
};




// --- zone drawing (armed drag; wired into the scope pointer handlers) ---
$("zmDraw").onclick = () => {
  if (!zm.drawArmed && !zmCplOK(+$("zmCh").value || 0)) return;
  zm.drawArmed = !zm.drawArmed;
  zm.drawA = zm.drawB = null;
  $("zmDraw").classList.toggle("on", zm.drawArmed);
  zmStatus(zm.drawArmed ? "drag a rectangle on the scope to add a zone" : "");
};
$("zmClearZones").onclick = () => { zm.zones = []; zmPushZones(); redraw(); };
$("zmTrig").onclick = () => {
  const on = !$("zmTrig").classList.contains("on");
  $("zmTrig").classList.toggle("on", on);
  send("zonemode", on ? 1 : 0);
  zmStatus(on ? "zone trigger ON — only qualifying frames publish" : "zone trigger off");
};

// --- mask build from N raw frames (dilated client-side, uploaded) ---
$("zmBuild").onclick = async () => {
  if (!st || !st.win_cols) { zmStatus("no window info yet"); return; }
  const N = Math.max(4, Math.min(200, +$("zmN").value || 32));
  const tolT = Math.max(0, +$("zmTolT").value || 0), tolV = Math.max(0, +$("zmTolV").value || 0);
  const ch = +$("zmCh").value || 0;
  if (!zmCplOK(ch)) return;
  const win = st.win_cols, posFrac = st.trig_pos_frac > 0 ? st.trig_pos_frac : 0.5;
  const lo = new Array(win).fill(255), hi = new Array(win).fill(0);
  let got = 0, lastSeq = 0, tries = 0;
  zmStatus("building mask 0/" + N + "…");
  while (got < N && tries < N * 6) {
    tries++;
    try {
      const r = await fetch("/api/frame.bin?since=" + lastSeq + "&waitms=1000&raw=1");
      const f = decodeBinFrame(await r.arrayBuffer());
      if (!f || f.unchanged || f.seq === lastSeq) continue;
      lastSeq = f.seq;
      if (!(f.edge_x >= 0) || !(f.sample_s > 0)) continue;
      const sig = ch === 1 ? f.c2 : f.c1;
      if (!sig) continue;
      const left = Math.round(f.edge_x - posFrac * win);
      for (let j = 0; j < win; j++) {
        const s = left + j;
        if (s < 0 || s >= f.cols) continue;
        const v = sig[s];
        if (v < lo[j]) lo[j] = v;
        if (v > hi[j]) hi[j] = v;
      }
      got++;
      if (got % 8 === 0) zmStatus("building mask " + got + "/" + N + "…");
    } catch (e) { break; }
  }
  if (got < 4) { zmStatus("mask build failed — no locked frames (trigger on-signal?)"); return; }
  // unobserved columns (lo>hi) are UNTESTABLE — normalize to always-pass before
  // dilating, or they become inverted-garbage bounds that fail every sample
  for (let j = 0; j < win; j++) if (lo[j] > hi[j]) { lo[j] = 0; hi[j] = 255; }
  // dilation (same morphology as engine.BuildMaskFromEnvelope)
  const dLo = new Array(win), dHi = new Array(win);
  for (let j = 0; j < win; j++) {
    let mn = 255, mx = 0;
    for (let k = Math.max(0, j - tolT); k <= Math.min(win - 1, j + tolT); k++) {
      if (lo[k] < mn) mn = lo[k];
      if (hi[k] > mx) mx = hi[k];
    }
    dLo[j] = Math.max(0, mn - tolV);
    dHi[j] = Math.min(255, mx + tolV);
  }
  zm.mask = { lo: dLo, hi: dHi, win, ch };
  const c = zmVctx(ch);
  if (c) { // frozen source for exact re-mapping on later V/div / offset changes
    zm.mask.srcLo = dLo.slice(); zm.mask.srcHi = dHi.slice();
    zm.mask.svpc = c.vpc; zm.mask.soff = c.off; zm.mask.avpc = c.vpc; zm.mask.aoff = c.off;
  }
  await fetch("/api/mask", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lo: dLo, hi: dHi, win, ch }) }).catch(() => {});
  zmStatus("mask built from " + got + " frames (±" + tolT + " samp, ±" + tolV + " codes) — set test mode");
  redraw();
};
$("zmMode").onchange = () => send("maskmode", +$("zmMode").value);
$("zmClearStats").onclick = () => { send("maskclear", 0); zm.failMark = null; redraw(); };

window.zmRescale = zmRescale; // exercised directly by the browser e2e

// --- live meter + failure gallery (driven off the 1 Hz status poll) ---
setInterval(() => {
  if (!st) return;
  zmRescale();
  if (st.mask_mode > 0 || st.mask_fail > 0 || st.mask_pass > 0) {
    const total = (st.mask_pass || 0) + (st.mask_fail || 0);
    let line = `pass ${st.mask_pass || 0} · FAIL ${st.mask_fail || 0}` +
      (total ? ` · ${((st.mask_fail || 0) / total * 100).toFixed(3)}%` : "") +
      (st.mask_stopped ? " · STOPPED ON FAIL" : "");
    if (st.mask_skip > 0) line += ` · skip ${st.mask_skip}`;
    // a test that only skips is DEAD — say so instead of looking happy
    if (st.mask_mode > 0 && (st.mask_skip || 0) > zm.lastSkip && zm.lastSkip >= 0)
      line += " · MASK STALE (scale/timebase changed) — rebuild";
    zm.lastSkip = st.mask_skip || 0;
    $("zmMeter").textContent = line;
  } else { $("zmMeter").textContent = ""; zm.lastSkip = st.mask_skip || 0; }
  if (st.zone_mode > 0 && (st.zone_skip || 0) > zm.lastZoneSkip && zm.lastZoneSkip >= 0)
    zmStatus("zone trigger inactive at this timebase (env/roll frames can't qualify)");
  zm.lastZoneSkip = st.zone_skip || 0;
  if ((st.mask_ring || 0) !== zm.lastRing) {
    zm.lastRing = st.mask_ring || 0;
    let html = "";
    for (let i = 0; i < zm.lastRing; i++) html += `<button class="btn-mini zmf" data-i="${i}">fail ${i + 1}</button> `;
    $("zmGallery").innerHTML = html;
    for (const b of document.querySelectorAll("#zmGallery .zmf")) {
      b.onclick = async () => {
        try {
          const r = await (await fetch("/api/maskfail?i=" + b.dataset.i)).json();
          if (!r.ok) return;
          frame = {
            seq: r.seq, unchanged: false,
            c1: Int16Array.from(r.c1), c2: r.c2 ? Int16Array.from(r.c2) : null,
            is_env: false, cols: r.valid, col_span_s: r.valid * r.sample_s,
            tdiv_s: st.tdiv_s, displayed_sdiv_s: st.displayed_sdiv_s,
            vpc1: 1 / 32, vpc2: 1 / 32, off1_v: 0, off2_v: 0,
            edge_frac: r.edge_x >= 0 ? r.edge_x / r.valid : -1,
            win_frac: Math.min(1, r.win_cols / r.valid), depth: r.valid,
            trigd: true, interp: false, coherent: true, ptp: 0,
          };
          zm.failMark = { frac: r.fail_sample / (r.valid - 1), code: r.fail_code };
          frozen = true; $("freeze").classList.add("on");
          userZoomed = false; lastSig = "maskfail";
          const h = homeWindow(frame); view.win.a = h.a; view.win.b = h.b;
          redraw();
          zmStatus("failure " + (+b.dataset.i + 1) + " @ capture #" + r.seq + " — violation circled (unfreeze to resume)");
        } catch (e) { }
      };
    }
  }
}, 1000);


async function bodeRenderNow() {
  if (typeof bodeDraw !== "function") return;
  const cv = $("bodeCv"); if (!cv) return;
  let pts = { freq: [], gain_db: [], phase_deg: [] };
  try { const r = await (await fetch("/api/bode")).json(); if (r.ok) pts = r; } catch (e) { }
  // crisp render at the element's CSS box × dpr
  const rect = cv.getBoundingClientRect();
  const W = Math.max(200, Math.round(rect.width || cv.width)), H = cv.height;
  if (cv.width !== W) cv.width = W;
  const g = cv.getContext("2d");
  bodeDraw(g, cv.width, H, pts, bodeColors());
  return pts.n || 0;
}

$("bodeArm").onclick = () => {
  bode.armed = !bode.armed;
  $("bodeArm").classList.toggle("on", bode.armed);
  $("bodeArm").textContent = bode.armed ? "STOP" : "ARM";
  const ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
  if (ref === dut) { bodeStatus("REF and DUT must be different channels"); }
  // /api/set bodemode carries ref/dut in lo/hi
  fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: bode.armed ? 1 : 0, lo: ref, hi: dut }) }).catch(() => {});
  bodeStatus(bode.armed ? "armed — sweep the source frequency to add points" : "stopped");
};
$("bodeClear").onclick = () => {
  send("bodeclear", 0);
  bode.lastN = -1;
  bodeRenderNow();
  bodeStatus(bode.armed ? "cleared — sweeping" : "cleared");
};
for (const id of ["bodeRef", "bodeDut"]) $(id).onchange = () => {
  if (bode.armed) {
    const ref = +$("bodeRef").value || 0, dut = +$("bodeDut").value || 0;
    fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control: "bodemode", value: 1, lo: ref, hi: dut }) }).catch(() => {});
  }
};
// full-screen enlarge on click, reusing the eye big-view dialog shell
$("bodeCv").onclick = () => {
  if (typeof ejBigVisible !== "function") return;
  ejBigKind = "bode";
  $("ejBigWrap").classList.remove("hidden");
  bodeDrawBig();
};

// 1 Hz: refresh the curve + the live-point readout while armed (or non-empty).
setInterval(async () => {
  if (!st) return;
  const active = st.bode_mode > 0 || (st.bode_points || 0) > 0;
  if (!active) return;
  const n = await bodeRenderNow();
  if (typeof ejBigVisible === "function" && ejBigVisible() && ejBigKind === "bode") bodeDrawBig();
  let line = `${n || 0} points`;
  if (st.bode_mode > 0) {
    line = st.bode_valid
      ? `live: ${eng(st.bode_freq_hz, "Hz", 3)} · ${st.bode_gain_db.toFixed(2)} dB · ${st.bode_phase_deg.toFixed(1)}° — ${n || 0} pts`
      : `armed — no single-frequency lock yet (${n || 0} pts)`;
  }
  bodeStatus(line);
}, 1000);

$("spgArm").onclick = () => {
  spg.armed = !spg.armed;
  $("spgArm").classList.toggle("on", spg.armed);
  $("spgArm").textContent = spg.armed ? "STOP" : "ARM";
  if (spg.armed) spgEnsure();
  spgStatus(spg.armed ? "armed — building the waterfall from each capture" : "stopped");
};
$("spgClear").onclick = () => { if (spg.sg) sgClear(spg.sg); spgRender(); spgStatus(spg.armed ? "cleared — building" : "cleared"); };
$("spgCh").onchange = () => { spg.ch = +$("spgCh").value === 2 ? 2 : 1; };
$("spgFloor").onchange = () => { if (spg.sg) spg.sg.floorDb = +$("spgFloor").value || -60; };
$("spgCv").onclick = () => {
  if (typeof ejBigVisible !== "function") return;
  ejBigKind = "spg"; $("ejBigWrap").classList.remove("hidden"); spgDrawBig();
};
// push + render on a fast tick so the waterfall scrolls at ~frame rate
setInterval(() => {
  if (!spg.armed && (!spg.sg || spg.sg.rows === 0)) return;
  spgPushCurrent();
  spgRender();
  if (typeof ejBigVisible === "function" && ejBigVisible() && ejBigKind === "spg") spgDrawBig();
  if (spg.armed) spgStatus(`waterfall live · ${spg.sg ? spg.sg.rows : 0} rows · Nyquist ${eng(peakNyq(), "Hz", 3)}`);
}, 80);
