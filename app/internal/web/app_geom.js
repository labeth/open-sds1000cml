// app_geom.js — viewport/window/nav geometry + marker/zoom math (classic script; shares app.js globals).

"use strict";
function srGateDefaultFromView() {
  const w = view.win, s = w.b - w.a;
  // fallback: inset from the visible edges so both handles are easy to grab
  srGate.a = w.a + 0.1 * s; srGate.b = w.a + 0.9 * s;
  const f = frame;
  if (!f || !f.cols || f.is_env) return;
  // Propose on the channel that will drive the matching: C1/C2 as selected, or
  // (both) the trigger-source channel — the one the user is triggering on.
  const sel = +$("srCh").value || 0;
  const alignIdx = sel === 2 ? 1 : sel === 1 ? 0 : (st && st.trig_source === 1 ? 1 : 0);
  const ch = alignIdx === 1 ? f.c2 : f.c1;
  if (!ch) return;
  // Visible slice, clamped to the record: the deep home window can now run past
  // the record ends (blank margin, no samples), so view.win maps outside [0,1].
  let lo = Math.max(0, Math.round(w.a * (f.cols - 1)));
  let hi = Math.min(f.cols, Math.round(w.b * (f.cols - 1)) + 1);
  if (hi - lo < 32) return;
  const slice = Float32Array.from(ch.subarray(lo, hi));
  // Propose the ACTIVE region of the view (no trigger-edge exclusion — if the
  // edge is the interesting thing on screen, that's what gets marked), narrowed
  // to one period when clearly periodic. srBuildTemplate/srDetectPeriod are the
  // same primitives the engine's auto path uses.
  const act = (typeof srBuildTemplate === "function") ? srBuildTemplate(slice, slice.length, -1, slice.length) : null;
  if (!act) return;
  let gLo = act.lo, gHi = act.hi + 1;
  if (typeof srDetectPeriod === "function") {
    const p = srDetectPeriod(slice, gLo, gHi);
    if (p >= 16 && p < gHi - gLo) gHi = gLo + p;
  }
  if (gHi - gLo < 8) return;
  srGate.a = (lo + gLo) / (f.cols - 1);
  srGate.b = (lo + gHi) / (f.cols - 1);
}

// The ONE column<->pixel mapping: traces, overlays and decode all go through it,
// so they stay aligned at any zoom. At win={0,1} it equals the legacy i/(n-1)*(CW-1).
function xForCol(i, n) { const w = view.win; return (i / (n - 1) - w.a) / (w.b - w.a) * (CW - 1); }

function fracForX(xpx) { const w = view.win; return w.a + (xpx / (CW - 1)) * (w.b - w.a); }

function navFrac(ev) { const r = nav.getBoundingClientRect(); return Math.max(0, Math.min(1, (ev.clientX - r.left) / r.width)); }

function navY(code, zoom) { zoom = zoom || 1; return NH * (1 - (128 + (code - 128) * zoom) / 255); }

function setWin(a, b) { view.win.a = a; view.win.b = b; scheduleRender(); }

// homeSpan: one acquisition screen = DIVX divisions of the HARDWARE time/div, as
// a fraction of the served record — so the home view (zoom 1) shows exactly the
// hardware timebase and the grid reads tdiv_s/div (what the knob/dropdown says).
// Deep memory holds more than one screen, so at fast HW timebases the record's
// WinCols display window is only a zoomed-in slice of it; this uses enough of the
// record to fill a full HW screen. Clamps to the whole record for a short capture.
function homeSpan(f) {
  if (!f) return 1;
  if (f.tdiv_s > 0 && f.col_span_s > 0) return Math.min(1, DIVX * f.tdiv_s / f.col_span_s);
  return (f.win_frac > 0 && f.win_frac <= 1) ? f.win_frac : 1;
}

// The "home" window: the trigger-centered HARDWARE-timebase screen (grid =
// tdiv_s/div), EXCEPT for a single/stopped capture where we show the WHOLE record
// so you see the full shot and can zoom in.
function homeWindow(f) {
  if (!f) return { a: 0, b: 1 };
  const stopped = st && !st.running;         // single shot / stopped: show it all
  let wf = homeSpan(f);
  if (stopped || f.is_env) wf = 1;
  const pf = (st && st.trig_pos_frac > 0 && st.trig_pos_frac < 1) ? st.trig_pos_frac : 0.5;
  const c = (f.edge_frac >= 0) ? f.edge_frac : 0.5; // matches server window() when EdgeX<0
  let a = c - wf * pf, b = c + wf * (1 - pf);
  // DEEP record with a real edge: keep the edge EXACTLY at posFrac and let the
  // window run past the record ends (blank margin, like the LCD) — do NOT clamp
  // it back into the record. The raw record is not phase-stable, so the edge is
  // the anchor; clamping would slide the edge off-centre when it sits near a
  // record end and make the trace jitter as the (multi-period) edge wanders.
  const deep = f.win_frac > 0 && f.win_frac < 1 && !f.is_env && f.edge_frac >= 0 && !(st && !st.running);
  if (!deep) {
    const span = Math.min(1, b - a);
    if (a < 0) { a = 0; b = span; } else if (b > 1) { b = 1; a = 1 - span; }
  }
  return { a, b };
}

function goHome() { if (frame) { const h = homeWindow(frame); view.win.a = h.a; view.win.b = h.b; } view.vwin.a = 0; view.vwin.b = 1; userZoomed = false; clearPersist(); redraw(); }

// Acquisition signature: a change (band/env/depth/run-state) re-homes the window.
function acqSig(f) { return (f.is_env ? "E" : "") + ":" + (f.c1 ? f.c1.length : 0) + ":" + ((st && st.running) ? "R" : "S"); }

// winRange returns the column index range [iLo,iHi] visible in view.win (a small
// margin so segments enter/exit cleanly). Iterating only these is also faster
// when zoomed in.
function winRange(n) {
  const w = view.win;
  return [Math.max(0, Math.floor(w.a * (n - 1)) - 1), Math.min(n - 1, Math.ceil(w.b * (n - 1)) + 1)];
}

// ---- cursor dragging ----
function ptToNorm(ev) {
  const r = scope.getBoundingClientRect();
  return { x: (ev.clientX - r.left) / r.width, y: (ev.clientY - r.top) / r.height };
}

function markerHit(p) {
  if (!st || view.mode !== "YT" || !frame) return null;
  const vpcT = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 25);
  if (st.trig_code && p.x > 0.85) {                 // trigger LEVEL handle (right edge)
    const ly = yFor(128 + st.trig_volts / vpcT, 1) / CH;
    if (Math.abs(p.y - ly) < 0.06) return { kind: "level", vpc: vpcT };
  }
  if (p.x < 0.13) {                                 // channel GROUND / offset arrows (left edge)
    for (const [ch, en, vpc, offV, zoom] of [[1, view.c1, frame.vpc1, frame.off1_v, st.zoom1 || 1], [2, view.c2, frame.vpc2, frame.off2_v, st.zoom2 || 1]]) {
      if (!en || !vpc) continue;
      const gy = yFor(128 + (offV || 0) / vpc, zoom) / CH;
      if (Math.abs(p.y - gy) < 0.06) return { kind: "off" + ch, vpc, zoom };
    }
  }
  return null;
}

function moveMarker(ev) {
  const cy = Math.max(0, Math.min(1, ptToNorm(ev).y));
  if (mk.kind === "level") {
    const volts = (codeAtY(cy, 1) - 128) * mk.vpc; // top of screen = higher level = up
    st.trig_volts = volts; $("lvl").value = volts.toFixed(2); $("lvlv").textContent = volts.toFixed(2) + " V";
  } else {
    const ch = mk.kind === "off1" ? 1 : 2, offV = (codeAtY(cy, mk.zoom) - 128) * mk.vpc;
    if (ch === 1) { st.off1_v = offV; $("off1").value = offV.toFixed(2); $("off1v").textContent = offV.toFixed(2) + " V"; }
    else { st.off2_v = offV; $("off2").value = offV.toFixed(2); $("off2v").textContent = offV.toFixed(2) + " V"; }
  }
  scheduleRender();
}

function commitMarker() {
  if (mk.kind === "level") send("triglevelcode", trigCodeFor(st.trig_volts));
  else { const ch = mk.kind === "off1" ? 1 : 2; send("offset" + ch, (mk.kind === "off1" ? st.off1_v : st.off2_v) / probeOf(ch)); }
}

function boxRect() {
  const r = scope.getBoundingClientRect();
  const x0 = (Math.min(boxZoom.sx, boxZoom.ex) - r.left) / r.width;
  const x1 = (Math.max(boxZoom.sx, boxZoom.ex) - r.left) / r.width;
  const y0 = (Math.min(boxZoom.sy, boxZoom.ey) - r.top) / r.height;
  const y1 = (Math.max(boxZoom.sy, boxZoom.ey) - r.top) / r.height;
  const wpx = Math.abs(boxZoom.ex - boxZoom.sx), hpx = Math.abs(boxZoom.ey - boxZoom.sy);
  return { x0: Math.max(0, x0), x1: Math.min(1, x1), y0: Math.max(0, y0), y1: Math.min(1, y1), wpx, hpx };
}

function applyBoxZoom() {
  const b = boxRect();
  if (view.mode === "FFT") {
    if (b.wpx < 12) return;
    const fw = view.fwin, span = fw.b - fw.a;
    const na = fw.a + b.x0 * span, nb = fw.a + b.x1 * span;
    if (nb - na > 1e-6) { view.fwin.a = na; view.fwin.b = nb; }
    return;
  }
  const w = view.win, hspan = w.b - w.a;
  if (b.wpx >= 12) {
    const na = w.a + b.x0 * hspan, nb = w.a + b.x1 * hspan;
    if (nb - na >= MINSPAN * 0.01) { view.win.a = na; view.win.b = nb; }
  }
  if (b.hpx >= 12) {
    const vw = view.vwin, vspan = vw.b - vw.a;
    // screen y grows downward; vwin is bottom→top
    const na = vw.a + (1 - b.y1) * vspan, nb = vw.a + (1 - b.y0) * vspan;
    if (nb - na > 1e-4) { view.vwin.a = na; view.vwin.b = nb; }
  }
  userZoomed = true;
  clearPersist();
}

function moveCursor(ev) {
  const p = ptToNorm(ev);
  const clamp = x => Math.max(0, Math.min(1, x));
  if (cur.drag[0] === "t") cur[cur.drag] = clamp(p.x); else cur[cur.drag] = clamp(p.y);
  scheduleRender();
}

// moveSrGate maps the pointer's canvas-x into a RECORD fraction (so the marker
// stays on the signal through zoom) and updates the dragged gate edge.
function moveSrGate(ev) {
  const px = ptToNorm(ev).x, span = view.win.b - view.win.a;
  srGate[srGate.drag] = Math.max(0, Math.min(1, view.win.a + px * span));
  srGate.placed = true; // user-positioned: gate toggles must not auto-replace it
  scheduleRender();
}

// Wheel over the scope: zoom about the cursor (Y-T only). Anchors the record
// fraction under the pointer so it stays put as the span grows/shrinks.
function panWin(dir) {
  userZoomed = true;
  const w = view.win, span = w.b - w.a;
  const na = Math.max(0, Math.min(1 - span, w.a + span * 0.15 * dir));
  setWin(na, na + span);
}

function stepTdiv(dir) {
  const sel = $("tdiv");
  if (!sel.options.length) return;
  const i = Math.max(0, Math.min(sel.options.length - 1, sel.selectedIndex + dir));
  if (i !== sel.selectedIndex) { sel.selectedIndex = i; send("tdiv", +sel.value); }
}

// nearest ladder value to a target (time/div, V/div).
function nearestLadder(target, list) {
  let best = list[0], bd = Infinity;
  for (const v of list) { const d = Math.abs(v - target); if (d < bd) { bd = d; best = v; } }
  return best;
}

