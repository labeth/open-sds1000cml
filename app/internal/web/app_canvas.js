// app_canvas.js — scope + navigator pointer/wheel/drag interactions (classic script; loaded after app.js state).

// ---- scope + navigator pointer / wheel / drag interactions ----

"use strict";
scope.addEventListener("pointerdown", ev => {
  if (ev.detail > 1) return; // the 2nd click of a double-click must not grab a cursor/marker (dblclick resets zoom)
  if (ev.shiftKey && view.mode === "YT" && st && frame) { // Shift+click = set trigger level here
    const vpc = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 25);
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
    if (boxZoom.moved) scheduleRender();
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
  scheduleRender();
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
      scheduleRender();
      return;
    }
    const sx = Math.max(0, Math.min(1, ptToNorm(ev).x));
    const fx = fw.a + sx * span;
    const ns = Math.max(1e-5, Math.min(1, span * Math.exp(d * 0.001)));
    let na = fx - sx * ns, nb = na + ns;
    if (na < 0) { na = 0; nb = ns; }
    if (nb > 1) { nb = 1; na = 1 - ns; }
    view.fwin.a = na; view.fwin.b = nb;
    scheduleRender();
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
