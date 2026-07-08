// app_draw.js — Y-T/X-Y trace, grid, math, refs, cursors drawing (classic script; shares app.js globals).

"use strict";
// Clear the afterglow accumulation buffer (the WebGL persistence framebuffer).
// Called on view/zoom/scale changes so stale trails don't linger.
function clearPersist() { if (GLR) GLR.persistClear(); }

// ---- drawing ----
function drawGrid(g) {
  g.fillStyle = "#05080c"; g.fillRect(0, 0, CW, CH);
  g.strokeStyle = "#182430"; g.lineWidth = dpr;
  const vline = xf => { const px = Math.round(xf) + .5; g.beginPath(); g.moveTo(px, 0); g.lineTo(px, CH); g.stroke(); };
  // Vertical (TIME) divisions. In Y-T each line marks one tdiv_s of SIGNAL,
  // anchored to the trigger — so the marker SPACING scales with zoom (further
  // apart zoomed in, closer zoomed out) while the time/div LABEL stays the fixed
  // hardware value. FFT/X-Y/no-frame keep the fixed DIVX graticule.
  const w = view.win, span = w.b - w.a;
  let drewTime = false;
  if (view.mode === "YT" && frame && frame.tdiv_s > 0 && frame.col_span_s > 0 && span > 0) {
    const dtFrac = frame.tdiv_s / frame.col_span_s;                 // one division as a record fraction
    const anchor = (frame.edge_frac >= 0) ? frame.edge_frac : 0.5; // the trigger
    const nLo = Math.ceil((w.a - anchor) / dtFrac), nHi = Math.floor((w.b - anchor) / dtFrac);
    if (nHi - nLo <= 400) { for (let n = nLo; n <= nHi; n++) vline((anchor + n * dtFrac - w.a) / span * (CW - 1)); drewTime = true; }
  }
  if (!drewTime) for (let i = 1; i < DIVX; i++) vline(i * CW / DIVX);
  for (let i = 1; i < DIVY; i++) { // one line per 32 codes, mapped through the voltage window
    const y = Math.round(yFor(i * 32, 1)) + .5;
    if (y < 0 || y > CH) continue;
    g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke();
  }
  g.strokeStyle = "#26384a";
  vline(CW / 2);                                                    // screen-centre reference
  const yMid = yFor(128, 1);                                        // code-128 (0 V) reference
  if (yMid >= 0 && yMid <= CH) { g.beginPath(); g.moveTo(0, yMid); g.lineTo(CW, yMid); g.stroke(); }
}

// yFor maps a code to a screen y through the VOLTAGE window (vwin) — box
// zoom narrows vwin so a stack's sub-code detail becomes visible.
function yFor(code, zoom) {
  zoom = zoom || 1;
  const v01 = (128 + (code - 128) * zoom) / 255; // 0=bottom .. 1=top of full scale
  const w = view.vwin;
  return CH * (1 - (v01 - w.a) / (w.b - w.a));
}

// codeAtY inverts yFor for interactions (marker drags, shift+click): cy is
// the pointer's screen fraction (0=top), zoom the channel detent zoom.
function codeAtY(cy, zoom) {
  zoom = zoom || 1;
  const w = view.vwin;
  const v01 = w.a + (1 - cy) * (w.b - w.a);
  return 128 + (v01 * 255 - 128) / zoom;
}

function drawTrace(g, cols, color, zoom) {
  if (!cols || !cols.length) return;
  g.strokeStyle = color; g.lineWidth = 1.4 * dpr; g.lineJoin = "round";
  const n = cols.length, [iLo, iHi] = winRange(n);
  if (iHi - iLo > 4 * CW) {
    // DENSE view (superres stacks reach 1.3M points; deep records zoomed
    // out): a lineTo per sample janks the canvas, so draw the min/max
    // envelope per pixel column instead — what a scope raster really shows.
    g.beginPath();
    let pen = false;
    for (let px = 0; px < CW; px++) {
      const a = Math.max(iLo, Math.ceil(fracForX(px) * (n - 1)));
      const b = Math.min(iHi, Math.ceil(fracForX(px + 1) * (n - 1)) - 1);
      let lo = Infinity, hi = -Infinity;
      for (let i = a; i <= b; i++) {
        const v = cols[i];
        if (v < 0) continue;
        if (v < lo) lo = v;
        if (v > hi) hi = v;
      }
      if (hi < lo) { pen = false; continue; }
      const y1 = yFor(hi, zoom), y2 = yFor(lo, zoom);
      if (pen) g.lineTo(px, y1); else g.moveTo(px, y1);
      g.lineTo(px, y2);
      pen = true;
    }
    g.stroke();
    return;
  }
  g.beginPath(); let pen = false;
  for (let i = iLo; i <= iHi; i++) {
    const v = cols[i];
    if (v < 0) { pen = false; continue; }
    const x = xForCol(i, n), y = yFor(v, zoom);
    if (y < -4 || y > CH + 4) { pen = false; continue; }
    if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
  }
  g.stroke();
}

function drawEnv(g, mn, mx, color, zoom) {
  if (!mn || !mx) return;
  zoom = zoom || 1; g.fillStyle = color; g.globalAlpha = .8;
  const n = mn.length, [iLo, iHi] = winRange(n);
  const sx = Math.max(1, (CW) / ((view.win.b - view.win.a) * n));
  for (let c = iLo; c <= iHi; c++) {
    const yt = yFor(mx[c], zoom), yb = yFor(mn[c], zoom);
    g.fillRect(xForCol(c, n), yt, sx, Math.max(1, yb - yt));
  }
  g.globalAlpha = 1;
}

function drawTrigMarkers(g) {
  if (!st) return;
  // Horizontal trigger-LEVEL line (where the level sits on the trace = the
  // point the display now anchors on).
  if (st.trig_code) {
    const vpc = (frame && (st.trig_source === 1 ? frame.vpc2 : frame.vpc1)) || (1 / 32);
    const y = yFor(128 + st.trig_volts / (vpc * 32) * 32, 1);
    if (y >= 0 && y <= CH) {
      g.strokeStyle = TRIGCOL; g.globalAlpha = 0.7; g.setLineDash([6 * dpr, 5 * dpr]); g.lineWidth = dpr;
      g.beginPath(); g.moveTo(0, y + .5); g.lineTo(CW, y + .5); g.stroke(); g.setLineDash([]); g.globalAlpha = 1;
      g.fillStyle = TRIGCOL; // level handle on the right edge (drag it to move the level)
      g.beginPath(); g.moveTo(CW, y - 6 * dpr); g.lineTo(CW - 10 * dpr, y); g.lineTo(CW, y + 6 * dpr); g.fill();
    }
  }
  // Vertical trigger-POSITION marker (where in time the trigger sits), mapped
  // through the zoom window; hidden when it falls outside the visible range.
  // Prefer the real edge position in the served record (deep memory); fall back
  // to the HW trigger-position fraction when there's no software edge.
  const frac = (frame && frame.edge_frac >= 0) ? frame.edge_frac
             : (st.trig_pos_frac > 0 ? st.trig_pos_frac : 0.5);
  if (frac >= view.win.a && frac <= view.win.b) {
    const tx = (frac - view.win.a) / (view.win.b - view.win.a) * CW;
    g.strokeStyle = "rgba(255,159,46,.35)"; g.lineWidth = dpr;
    g.beginPath(); g.moveTo(tx + .5, 0); g.lineTo(tx + .5, CH); g.stroke();
    g.fillStyle = CURCOL; g.font = "bold " + (10 * dpr) + "px system-ui"; g.textBaseline = "top";
    g.beginPath(); g.moveTo(tx - 6 * dpr, 0); g.lineTo(tx + 6 * dpr, 0); g.lineTo(tx, 10 * dpr); g.fill();
    g.fillText("T", tx + 7 * dpr, 1 * dpr);
  }
}

// Per-channel GROUND (0 V) markers on the left edge: where each channel's zero
// sits after its offset. Ground code = 128 + offV/vpc (V = (code-128)·vpc - offV).
function drawChannelMarkers(g) {
  if (!frame) return;
  g.textBaseline = "middle"; g.font = "bold " + (10 * dpr) + "px system-ui";
  const chans = [[view.c1, frame.vpc1, frame.off1_v, st ? st.zoom1 : 1, C1COL, "1"],
                 [view.c2, frame.vpc2, frame.off2_v, st ? st.zoom2 : 1, C2COL, "2"]];
  for (const [en, vpc, offV, zoom, col, lbl] of chans) {
    if (!en || !vpc) continue;
    const y = yFor(128 + (offV || 0) / vpc, zoom || 1);
    const yc = Math.max(6 * dpr, Math.min(CH - 6 * dpr, y));
    g.fillStyle = col;
    g.beginPath(); g.moveTo(0, yc - 5 * dpr); g.lineTo(9 * dpr, yc); g.lineTo(0, yc + 5 * dpr); g.fill();
    g.fillText(lbl, 12 * dpr, yc);
  }
  g.textBaseline = "alphabetic";
}

function componentMemo(src, cyclesPerLen) {
  if (compMemo.src !== src) { compMemo.src = src; compMemo.map.clear(); }
  const m = compMemo.map;
  if (m.has(cyclesPerLen)) return m.get(cyclesPerLen);
  const out = component(src, cyclesPerLen);
  if (m.size >= 16) m.delete(m.keys().next().value); // bound memory: evict oldest
  m.set(cyclesPerLen, out);
  return out;
}

function computeMath() {
  if (mathFn === "off" || !frame || frame.is_env || !frame.c1) return null;
  const selSig = (mathFn === "res1")
    ? fftCh[1].sel.join(",") : (mathFn === "res2") ? fftCh[2].sel.join(",") : "";
  if (mathMemo.c1 === frame.c1 && mathMemo.c2 === frame.c2 &&
      mathMemo.fn === mathFn && mathMemo.sel === selSig) return mathMemo.out;
  const out = computeMathRaw();
  mathMemo = { c1: frame.c1, c2: frame.c2, fn: mathFn, sel: selSig, out };
  return out;
}

function computeMathRaw() {
  const a = frame.c1, b = frame.c2, n = a.length, out = new Array(n);
  const clip = v => v < 0 ? 0 : v > 255 ? 255 : v;
  if (mathFn === "res1" || mathFn === "res2") {
    const ch = mathFn === "res1" ? 1 : 2, src = ch === 1 ? a : b, S = fftCh[ch];
    if (!src || !S || !S.sel.length) return null;   // nothing selected → nothing to remove
    let mean = 0, cnt = 0; for (const v of src) if (v >= 0) { mean += v; cnt++; }
    mean = cnt ? mean / cnt : 128;
    const res = Float64Array.from(src); // NOT src.slice(): an Int16Array copy would truncate the fractional accumulation below
    for (const f of S.sel) {
      const comp = componentMemo(src, f * (frame.col_span_s || 0)); // fitted tone at that freq
      if (!comp) continue;
      for (let i = 0; i < n; i++) if (res[i] >= 0) res[i] -= (comp[i] - mean); // remove its AC part
    }
    for (let i = 0; i < n; i++) out[i] = (src[i] < 0) ? -1 : clip(res[i]);
    return out;
  }
  if (!b) return null;
  for (let i = 0; i < n; i++) {
    if (a[i] < 0 || b[i] < 0) { out[i] = -1; continue; }
    const x = a[i] - 128, y = b[i] - 128;
    if (mathFn === "c1-c2") out[i] = clip(128 + (x - y));
    else if (mathFn === "c2-c1") out[i] = clip(128 + (y - x));
    else if (mathFn === "c1+c2") out[i] = clip(128 + (x + y));
    else out[i] = clip(128 + (x * y) / 96); // c1*c2, scaled to stay on-screen
  }
  return out;
}

// Draw the math trace (if any) at its SOURCE channel's vertical zoom, so it lines
// up with the trace it's derived from. Used by both the plain and persist paths.
function drawMath(g) {
  const m = computeMath();
  if (m) drawTrace(g, m, MATHCOL, (mathFn === "res2") ? (st ? st.zoom2 : 1) : (st ? st.zoom1 : 1));
}

function drawRefTrace(g, cols, refVpc, refOff, curVpc, curOff, color, zoom) {
  if (!cols || !cols.length || !curVpc) return;
  g.strokeStyle = color; g.lineWidth = 1.2 * dpr; g.lineJoin = "round";
  g.globalAlpha = 0.7; g.setLineDash([4 * dpr, 3 * dpr]);
  g.beginPath(); let pen = false;
  const n = cols.length, [iLo, iHi] = winRange(n);
  for (let i = iLo; i <= iHi; i++) {
    const rc = cols[i];
    if (rc < 0) { pen = false; continue; }
    const code = 128 + ((rc - 128) * refVpc - refOff + curOff) / curVpc;
    const x = xForCol(i, n), y = yFor(code, zoom);
    if (y < -4 || y > CH + 4) { pen = false; continue; }
    if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
  }
  g.stroke(); g.globalAlpha = 1; g.setLineDash([]);
}

function drawRefs(g) {
  for (const slot of ["A", "B"]) {
    const r = refs[slot];
    if (!r || !r.show) continue;
    // A superres model fit spans the raw record it was fit on; drawing it
    // index-proportionally over a different time base would silently show a
    // time-compressed curve. Hide it unless the spans match (~1%).
    if (r.srSpanS && frame && frame.col_span_s &&
        Math.abs(r.srSpanS - frame.col_span_s) > 0.01 * r.srSpanS) continue;
    if (r.c1) drawRefTrace(g, r.c1, r.vpc1, r.off1, frame ? frame.vpc1 : r.vpc1, frame ? frame.off1_v || 0 : 0, REFCOL[slot], st ? st.zoom1 : 1);
    if (r.c2) drawRefTrace(g, r.c2, r.vpc2, r.off2, frame ? frame.vpc2 : r.vpc2, frame ? frame.off2_v || 0 : 0, REFCOL[slot], st ? st.zoom2 : 1);
  }
}

function saveRef(slot) {
  if (!frame) return;
  // Cap stored refs (a superres stack reaches 1.3M points; drawRefTrace
  // strokes per sample, so an uncapped ref would jank every redraw).
  const cap = arr => {
    if (!arr) return null;
    const stride = Math.ceil(arr.length / 65536);
    if (stride <= 1) return Array.from(arr);
    const out = [];
    for (let i = 0; i < arr.length; i += stride) out.push(arr[i]);
    return out;
  };
  refs[slot] = {
    c1: view.c1 ? cap(frame.c1) : null,
    c2: view.c2 ? cap(frame.c2) : null,
    vpc1: frame.vpc1 || 1 / 32, vpc2: frame.vpc2 || 1 / 32,
    off1: frame.off1_v || 0, off2: frame.off2_v || 0,
    show: true,
  };
  updateRefRows(); redraw();
}

function updateRefRows() {
  let html = "";
  for (const slot of ["A", "B"]) {
    const r = refs[slot];
    if (!r) continue;
    html += `<div class="decrow" style="align-items:center">` +
      `<button class="btn-mini reftog" data-slot="${slot}" style="color:${REFCOL[slot]}">REF ${slot} ${r.show ? "●" : "○"}</button>` +
      `<button class="btn-mini refclr" data-slot="${slot}" title="clear REF ${slot}">✕</button></div>`;
  }
  const el = $("refRows"); el.innerHTML = html;
  el.querySelectorAll(".reftog").forEach(b => b.onclick = () => { refs[b.dataset.slot].show = !refs[b.dataset.slot].show; updateRefRows(); redraw(); });
  el.querySelectorAll(".refclr").forEach(b => b.onclick = () => { refs[b.dataset.slot] = null; updateRefRows(); redraw(); });
}

function drawYT(g) {
  drawGrid(g);
  drawRefs(g); // references sit UNDER the live traces
  if (frame) {
    if (frame.is_env) {
      if (view.c2) drawEnv(g, frame.e2min, frame.e2max, C2COL, st ? st.zoom2 : 1);
      if (view.c1) drawEnv(g, frame.e1min, frame.e1max, C1COL, st ? st.zoom1 : 1);
    } else {
      if (view.c2) drawTrace(g, frame.c2, C2COL, st ? st.zoom2 : 1);
      if (view.c1) drawTrace(g, frame.c1, C1COL, st ? st.zoom1 : 1);
      drawMath(g);
    }
  }
  drawChannelMarkers(g);
  drawTrigMarkers(g);
}

function drawXY() {
  drawGrid(ctx);
  if (!frame || !frame.c1 || !frame.c2) return;
  ctx.strokeStyle = MATHCOL; ctx.lineWidth = 1.2 * dpr; ctx.lineJoin = "round";
  ctx.beginPath(); let pen = false;
  const n = Math.min(frame.c1.length, frame.c2.length);
  const step = Math.max(1, Math.floor(n / 20000)); // dense stacks: stride the Lissajous
  for (let i = 0; i < n; i += step) {
    const a = frame.c1[i], b = frame.c2[i];
    if (a < 0 || b < 0) { pen = false; continue; }
    const x = a / 255 * CW, y = CH * (1 - b / 255);
    if (pen) ctx.lineTo(x, y); else { ctx.moveTo(x, y); pen = true; }
  }
  ctx.stroke();
  ctx.fillStyle = "var(--dim)"; ctx.font = (12 * dpr) + "px system-ui";
  ctx.fillText("X: C1   Y: C2", 8 * dpr, 16 * dpr);
}

function drawCursors() {
  if (!view.cursors) return;
  const g = ctx;
  g.lineWidth = dpr; g.strokeStyle = CURCOL; g.setLineDash([4 * dpr, 4 * dpr]);
  for (const t of [cur.t1, cur.t2]) { const x = t * CW; g.beginPath(); g.moveTo(x, 0); g.lineTo(x, CH); g.stroke(); }
  for (const v of [cur.v1, cur.v2]) { const y = v * CH; g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke(); }
  g.setLineDash([]);
  // handles
  g.fillStyle = CURCOL;
  for (const [i, t] of [[1, cur.t1], [2, cur.t2]]) { g.fillRect(t * CW - 4 * dpr, 0, 8 * dpr, 8 * dpr); }
  for (const [i, v] of [[1, cur.v1], [2, cur.v2]]) { g.fillRect(0, v * CH - 4 * dpr, 8 * dpr, 8 * dpr); }
}

function drawBoxZoom(g) {
  if (!boxZoom.active || !boxZoom.moved) return;
  const b = boxRect();
  g.save();
  g.strokeStyle = "rgba(255,159,46,.9)"; g.lineWidth = dpr; g.setLineDash([5 * dpr, 4 * dpr]);
  g.fillStyle = "rgba(255,159,46,.08)";
  const x = b.x0 * CW, y = b.y0 * CH, w = (b.x1 - b.x0) * CW, h = (b.y1 - b.y0) * CH;
  g.fillRect(x, y, w, h); g.strokeRect(x, y, w, h);
  g.restore();
}

