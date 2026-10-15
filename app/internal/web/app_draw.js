// ENGMODEL-OWNER-UNIT: FU-WEB-APP-DRAW
// app_draw.js — Y-T/X-Y trace, grid, math, refs, cursors drawing (classic script; shares app.js globals).

"use strict";
// Clear the afterglow accumulation buffer (the WebGL persistence framebuffer).
// Called on view/zoom/scale changes so stale trails don't linger.
// TRLC-LINKS: REQ-SDS-071
function clearPersist() { if (GLR) GLR.persistClear(); }

// ---- drawing ----
// DEC_LANE_PX is the decode lane's height (CSS px) under the Y-T plot.
const DEC_LANE_PX = 30;

// decLaneOn reports whether the Y-T plot has the decode lane under it: a
// protocol decoded, in Y-T, as the LCD's decode strip.
// TRLC-LINKS: REQ-SDS-207
function decLaneOn() {
  return view.mode === "YT" && typeof dcfg !== "undefined" && dcfg.proto !== "off";
}

// plotH is the Y-T plot's height in canvas px: the canvas less the decode
// lane while one shows, so the eight divisions end above the decoded bytes
// and no trace, marker or cursor lies under them (the LCD's plot ends above
// its decode strip alike).
// TRLC-LINKS: REQ-SDS-071, REQ-SDS-207
function plotH() {
  return decLaneOn() ? Math.max(CH - DEC_LANE_PX * dpr, CH / 2) : CH;
}

// TRLC-LINKS: REQ-SDS-071
function drawGrid(g) {
  g.fillStyle = "#05080c"; g.fillRect(0, 0, CW, CH);
  g.strokeStyle = "#182430"; g.lineWidth = dpr;
  const PH = plotH();
  // TRLC-LINKS: REQ-SDS-071
  const vline = xf => { const px = Math.round(xf) + .5; g.beginPath(); g.moveTo(px, 0); g.lineTo(px, PH); g.stroke(); };
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
  for (let i = 1; i < DIVY; i++) { // one line per 25 codes, mapped through the voltage window
    const y = Math.round(yFor(128 + (i - DIVY / 2) * 25, 1)) + .5;
    if (y < 0 || y > PH) continue;
    g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke();
  }
  if (PH < CH) { const y = Math.round(PH) - .5; g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke(); } // the plot's bottom edge
  g.strokeStyle = "#26384a";
  vline(CW / 2);                                                    // screen-centre reference
  const yMid = yFor(128, 1);                                        // code-128 (0 V) reference
  if (yMid >= 0 && yMid <= PH) { g.beginPath(); g.moveTo(0, yMid); g.lineTo(CW, yMid); g.stroke(); }
}

// yFor maps a code to a screen y through the VOLTAGE window (vwin) — box
// zoom narrows vwin so a stack's sub-code detail becomes visible.
// TRLC-LINKS: REQ-SDS-202
function yFor(code, zoom) {
  zoom = zoom || 1;
  const v01 = 0.5 + (code - 128) * zoom / 200; // 0=bottom .. 1=top of full scale
  const w = view.vwin;
  return plotH() * (1 - (v01 - w.a) / (w.b - w.a));
}

// codeAtY inverts yFor for interactions (marker drags, shift+click): cy is
// the pointer's screen fraction (0=top), zoom the channel detent zoom.
// TRLC-LINKS: REQ-SDS-202
function codeAtY(cy, zoom) {
  zoom = zoom || 1;
  const w = view.vwin;
  const v01 = w.a + (1 - cy * CH / plotH()) * (w.b - w.a); // cy is of the canvas, the plot ends above a decode lane
  return 128 + (v01 - 0.5) * 200 / zoom;
}

// inv (optional): display-level trace invert (SCPI Cn:INVS, st.inv1/st.inv2 —
// the SCPI shadow is the truth). Mirrors the RENDERED code about the display
// centre (127.5): v' = 255 − v, byte-identical to the LCD's invertCodes.
// Deliberately display-only and Y-T-only: measurements, decode, math, X-Y/FFT
// and mask/zone tests keep the true captured polarity (hardware scopes vary
// here; this clone pins the narrow, unsurprising meaning — what you SEE flips,
// what is measured does not). Gap sentinels (-1) pass through untouched.
// TRLC-LINKS: REQ-SDS-071
function drawTrace(g, cols, color, zoom, inv) {
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
        let v = cols[i];
        if (v < 0) continue;
        if (inv) v = 255 - v;
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
    let v = cols[i];
    if (v < 0) { pen = false; continue; }
    if (inv) v = 255 - v;
    const x = xForCol(i, n), y = yFor(v, zoom);
    if (y < -4 || y > plotH() + 4) { pen = false; continue; }
    if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
  }
  g.stroke();
}

// viewCovers is whether frame f's view window (vc1/vc2: the zoom's stretch
// read again at full detail, its first value at view_a of the served array
// and its last at view_b) covers the shown window.
// TRLC-LINKS: REQ-SDS-071
function viewCovers(f) {
  if (!f || !f.vc1 || !f.vc2 || f.vc1.length < 2 || !(f.view_b > f.view_a)) return false;
  const w = view.win, tol = (w.b - w.a) / 100;
  return f.view_a <= w.a + tol && f.view_b >= w.b - tol;
}

// drawViewTrace draws a view window's values (first at a, last at b of the
// served array) with drawTrace, the shown window put in the view's own
// fractions for the call.
// TRLC-LINKS: REQ-SDS-071
function drawViewTrace(g, vals, a, b, color, zoom, inv) {
  const w = view.win, wa = w.a, wb = w.b, s = b - a;
  w.a = (wa - a) / s; w.b = (wb - a) / s;
  try { drawTrace(g, vals, color, zoom, inv); } finally { w.a = wa; w.b = wb; }
}

// drawTraceHeld draws a trace whose held ranges ([a, b) samples a roll view's
// capture did not cover: the last value held between chunks) are no signal:
// the captured samples in color, the held ones dimmed.
// TRLC-LINKS: REQ-SDS-071
function drawTraceHeld(g, cols, held, color, zoom, inv) {
  if (!cols || !held || !held.length) { drawTrace(g, cols, color, zoom, inv); return; }
  const split = heldSplit(cols, held);
  g.globalAlpha = 0.35;
  drawTrace(g, split.held, color, zoom, inv);
  g.globalAlpha = 1;
  drawTrace(g, split.real, color, zoom, inv);
}

// heldSplit is cols as two traces, the captured samples and the held ones,
// each with the other's samples as gaps (-1); kept on the array per ranges.
// TRLC-LINKS: REQ-SDS-071
function heldSplit(cols, held) {
  const c = cols._heldSplit;
  if (c && c.ranges === held) return c;
  const real = new cols.constructor(cols), only = new cols.constructor(cols.length).fill(-1);
  for (const [a, b] of held) {
    for (let i = Math.max(0, a); i < Math.min(b, cols.length); i++) { only[i] = cols[i]; real[i] = -1; }
  }
  return (cols._heldSplit = { ranges: held, real, held: only });
}

// TRLC-LINKS: REQ-SDS-071
function drawEnv(g, mn, mx, color, zoom, inv) {
  if (!mn || !mx) return;
  zoom = zoom || 1; g.fillStyle = color; g.globalAlpha = .8;
  const n = mn.length, [iLo, iHi] = winRange(n);
  const sx = Math.max(1, (CW) / ((view.win.b - view.win.a) * n));
  for (let c = iLo; c <= iHi; c++) {
    // INVS mirrors the band: top code = inv(min), bottom code = inv(max).
    const vt = inv ? 255 - mn[c] : mx[c], vb = inv ? 255 - mx[c] : mn[c];
    const yt = yFor(vt, zoom), yb = yFor(vb, zoom);
    g.fillRect(xForCol(c, n), yt, sx, Math.max(1, yb - yt));
  }
  g.globalAlpha = 1;
}

// TRLC-LINKS: REQ-SDS-071
function drawTrigMarkers(g) {
  if (!st) return;
  // Horizontal trigger-LEVEL line (where the level sits on the trace = the
  // point the display now anchors on).
  if (st.trig_code) {
    const vpc = (frame && (st.trig_source === 1 ? frame.vpc2 : frame.vpc1)) || (1 / 25);
    // Include the source channel's offset so the level line sits where the
    // trigger actually is on the offset-shifted trace (matches the engine's
    // centering anchor and the LCD marker).
    const off = (frame && (st.trig_source === 1 ? frame.off2_v : frame.off1_v)) || 0;
    const y = yFor(128 + (st.trig_volts + off) / vpc, 1);
    if (y >= 0 && y <= plotH()) {
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
             : (Number.isFinite(st.trig_pos_frac) && st.trig_pos_frac >= 0 && st.trig_pos_frac <= 1 ? st.trig_pos_frac : 0.5);
  if (frac >= view.win.a && frac <= view.win.b) {
    const tx = (frac - view.win.a) / (view.win.b - view.win.a) * CW;
    g.strokeStyle = "rgba(255,159,46,.35)"; g.lineWidth = dpr;
    g.beginPath(); g.moveTo(tx + .5, 0); g.lineTo(tx + .5, plotH()); g.stroke();
    g.fillStyle = CURCOL; g.font = "bold " + (10 * dpr) + "px system-ui"; g.textBaseline = "top";
    g.beginPath(); g.moveTo(tx - 6 * dpr, 0); g.lineTo(tx + 6 * dpr, 0); g.lineTo(tx, 10 * dpr); g.fill();
    g.fillText("T", tx + 7 * dpr, 1 * dpr);
  }
}

// Per-channel GROUND (0 V) markers on the left edge: where each channel's zero
// sits after its offset. Ground code = 128 + offV/vpc (V = (code-128)·vpc - offV).
// TRLC-LINKS: REQ-SDS-071
function drawChannelMarkers(g) {
  if (!frame) return;
  g.textBaseline = "middle"; g.font = "bold " + (10 * dpr) + "px system-ui";
  const chans = [[view.c1, frame.vpc1, frame.off1_v, st ? st.zoom1 : 1, C1COL, "1"],
                 [view.c2, frame.vpc2, frame.off2_v, st ? st.zoom2 : 1, C2COL, "2"]];
  for (const [en, vpc, offV, zoom, col, lbl] of chans) {
    if (!en || !vpc) continue;
    const y = yFor(128 + (offV || 0) / vpc, zoom || 1);
    const yc = Math.max(6 * dpr, Math.min(plotH() - 6 * dpr, y));
    g.fillStyle = col;
    g.beginPath(); g.moveTo(0, yc - 5 * dpr); g.lineTo(9 * dpr, yc); g.lineTo(0, yc + 5 * dpr); g.fill();
    g.fillText(lbl, 12 * dpr, yc);
  }
  g.textBaseline = "alphabetic";
}

// TRLC-LINKS: REQ-SDS-203
function componentMemo(src, cyclesPerLen) {
  if (compMemo.src !== src) { compMemo.src = src; compMemo.map.clear(); }
  const m = compMemo.map;
  if (m.has(cyclesPerLen)) return m.get(cyclesPerLen);
  const out = component(src, cyclesPerLen);
  if (m.size >= 16) m.delete(m.keys().next().value); // bound memory: evict oldest
  m.set(cyclesPerLen, out);
  return out;
}

// TRLC-LINKS: REQ-SDS-203
function computeMath() {
  if (mathFn === "off" || !frame || frame.is_env || !frame.c1) return null;
  const selSig = (mathFn === "res1")
    ? fftCh[1].sel.join(",") : (mathFn === "res2") ? fftCh[2].sel.join(",") : "";
  if (mathMemo.c1 === frame.c1 && mathMemo.c2 === frame.c2 &&
      mathMemo.scale === mathScale() && mathMemo.frame === frame && mathMemo.fn === mathFn && mathMemo.sel === selSig) return mathMemo.out;
  const out = computeMathRaw();
  mathMemo = { scale: mathScale(), frame, c1: frame.c1, c2: frame.c2, fn: mathFn, sel: selSig, out };
  return out;
}

// TRLC-LINKS: REQ-SDS-203
function computeMathRaw() {
  const a = frame.c1, b = frame.c2, n = a.length, out = new Array(n);
  // TRLC-LINKS: REQ-SDS-203
  const clip = v => v < 0 ? 0 : v > 255 ? 255 : v;
  if (mathFn === "res1" || mathFn === "res2") {
    const ch = mathFn === "res1" ? 1 : 2, src = ch === 1 ? a : b, S = fftCh[ch];
    if (!src || !S || !S.sel.length) return null;   // nothing selected → nothing to remove
    let mean = 0, cnt = 0; for (const v of src) if (v >= 0) { mean += v; cnt++; }
    mean = cnt ? mean / cnt : 128;
    const res = Float64Array.from(src); // NOT src.slice(): an Int16Array copy would truncate the fractional accumulation below
    for (const f of S.sel) {
      const comp = componentMemo(src, f * frameSpanS(frame, n)); // fitted tone at that freq (true span: peak freqs are dt_s/raw-true)
      if (!comp) continue;
      for (let i = 0; i < n; i++) if (res[i] >= 0) res[i] -= (comp[i] - mean); // remove its AC part
    }
    for (let i = 0; i < n; i++) out[i] = (src[i] < 0) ? -1 : clip(res[i]);
    return out;
  }
  if (!b) return null;
  const v1 = frame.vpc1 || 1 / 25, v2 = frame.vpc2 || 1 / 25;
  const scale = mathScale() / 25;
  for (let i = 0; i < n; i++) {
    if (!(a[i] >= 0) || !(b[i] >= 0)) { out[i] = -1; continue; }
    const x = (a[i]-128)*v1-(frame.off1_v || 0);
    const y = (b[i]-128)*v2-(frame.off2_v || 0);
    let volts;
    if (mathFn === "c1-c2") volts = x-y;
    else if (mathFn === "c2-c1") volts = y-x;
    else if (mathFn === "c1+c2") volts = x+y;
    else volts = x*y;
    out[i] = clip(128 + volts/scale);
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-203
function mathScale() {
  const a = (frame.vpc1 || 1/25)*25/((st && st.zoom1) || 1);
  const b = (frame.vpc2 || 1/25)*25/((st && st.zoom2) || 1);
  return mathFn === "c1*c2" ? 4*a*b : a;
}

// TRLC-LINKS: REQ-SDS-203
function drawMath(g) {
  const m = computeMath();
  const residual = mathFn === "res1" || mathFn === "res2";
  const label = $("mathScale");
  if (label) label.textContent = m && !residual ? `${eng(mathScale(), mathFn === "c1*c2" ? "V²" : "V")}/div · zero at centre` : "";
  if (m) drawTrace(g, m, MATHCOL, residual ? ((st && st[mathFn === "res2" ? "zoom2" : "zoom1"]) || 1) : 1);
}

// drawRefTrace overlays a reference. With `tm` ({edge, dt} of the reference and
// the live record's) the reference is placed by time from the trigger, so one
// taken from a record of another length (an averaged master) lines up; without
// it, index-proportionally over the record.
// TRLC-LINKS: REQ-SDS-203
function drawRefTrace(g, cols, refVpc, refOff, curVpc, curOff, color, zoom, tm) {
  if (!cols || !cols.length || !curVpc) return;
  g.strokeStyle = color; g.lineWidth = 1.2 * dpr; g.lineJoin = "round";
  g.globalAlpha = 0.7; g.setLineDash([4 * dpr, 3 * dpr]);
  g.beginPath(); let pen = false;
  const n = cols.length;
  let iLo, iHi, xOf;
  if (tm) {
    // live record index of reference index i, then its screen x
    // TRLC-LINKS: REQ-SDS-203
    const liveIdx = (i) => tm.edgeL + (i - tm.edge) * tm.dt / tm.dtL;
    // TRLC-LINKS: REQ-SDS-203
    const refIdx = (frac) => tm.edge + (frac * (tm.nL - 1) - tm.edgeL) * tm.dtL / tm.dt;
    iLo = Math.max(0, Math.floor(refIdx(view.win.a)) - 1); iHi = Math.min(n - 1, Math.ceil(refIdx(view.win.b)) + 1);
    // TRLC-LINKS: REQ-SDS-203
    xOf = (i) => xForCol(liveIdx(i), tm.nL);
    if (iHi - iLo > 4 * CW) tm.step = Math.ceil((iHi - iLo) / (4 * CW)); // dense: a few points per pixel
  } else {
    [iLo, iHi] = winRange(n);
    // TRLC-LINKS: REQ-SDS-203
    xOf = (i) => xForCol(i, n);
  }
  const step = tm && tm.step ? tm.step : 1;
  for (let i = iLo; i <= iHi; i += step) {
    const rc = cols[i];
    if (rc < 0) { pen = false; continue; }
    const code = 128 + ((rc - 128) * refVpc - refOff + curOff) / curVpc;
    const x = xOf(i), y = yFor(code, zoom);
    if (y < -4 || y > plotH() + 4) { pen = false; continue; }
    if (pen) g.lineTo(x, y); else { g.moveTo(x, y); pen = true; }
  }
  g.stroke(); g.globalAlpha = 1; g.setLineDash([]);
}

// TRLC-LINKS: REQ-SDS-203
function drawRefs(g) {
  for (const [slot, r] of [["A", refs.A], ["B", refs.B], ["B", srModel]]) {
    if (!r || !r.show) continue;
    // A superres model fit spans the raw record it was fit on; drawing it
    // index-proportionally over a different time base would silently show a
    // time-compressed curve. Hide it unless the spans match (~1%).
    if (r.srSpanS && frame && frame.col_span_s &&
        Math.abs(r.srSpanS - frame.col_span_s) > 0.01 * r.srSpanS) continue;
    // time placement when both the reference and the live record know their trigger
    let tm = null;
    const live = frame && (frame.c1 || frame.c2);
    if (live && !frame.is_env && r.dt > 0 && r.edge >= 0 && frame.edge_frac >= 0) {
      const nL = live.length, dtL = frameDtS(frame, nL);
      if (dtL > 0) tm = { edge: r.edge, dt: r.dt, edgeL: frame.edge_frac * (nL - 1), dtL, nL };
    }
    if (r.c1) drawRefTrace(g, r.c1, r.vpc1, r.off1, frame ? frame.vpc1 : r.vpc1, frame ? frame.off1_v || 0 : 0, REFCOL[slot], st ? st.zoom1 : 1, tm && Object.assign({}, tm));
    if (r.c2) drawRefTrace(g, r.c2, r.vpc2, r.off2, frame ? frame.vpc2 : r.vpc2, frame ? frame.off2_v || 0 : 0, REFCOL[slot], st ? st.zoom2 : 1, tm && Object.assign({}, tm));
  }
}

// saveRef has the scope store its screen as reference slot ("A"/"B"): the
// page shows the scope's references, so the LCD, the phone and this page
// hold the same REF A and B.
// TRLC-LINKS: REQ-SDS-203, REQ-SDS-227
function saveRef(slot) { return refAct(slot, "store"); }

// refAct stores, shows, hides or clears a reference on the scope and takes
// both of the scope's slots from its answer.
// TRLC-LINKS: REQ-SDS-227
async function refAct(slot, op) {
  let j = null;
  try {
    j = await (await fetch("/api/refs", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ slot, op }) })).json();
  } catch (e) { j = null; }
  if (!j) { toast("REF " + slot + ": no answer from the scope", "warn"); return false; }
  if (j.refs) { refsTaken++; takeScopeRefs(j.refs); } // newer than any read still on its way
  if (!j.ok) toast("REF " + slot + ": " + (j.error || "the scope refused it"), "warn");
  return !!j.ok;
}

// updateRefRows syncs COMPARE's REF A/B controls with the stored references.
// TRLC-LINKS: REQ-SDS-203
function updateRefRows() {
  for (const slot of ["A", "B"]) {
    const r = refs[slot];
    const show = $("refShow" + slot), clr = $("refClear" + slot), info = $("refInfo" + slot);
    if (!show) continue;
    show.disabled = !r; clr.disabled = !r;
    show.classList.toggle("on", !!(r && r.show));
    show.setAttribute("aria-pressed", r && r.show ? "true" : "false");
    const chans = r ? [r.c1 ? "C1" : "", r.c2 ? "C2" : ""].filter(Boolean).join("+") : "";
    // each channel's V/div at the tip as stored, with its probe when not 1x
    // TRLC-LINKS: REQ-SDS-227
    const scale = (ch) => {
      const v = ch === 1 ? r.vdiv1 : r.vdiv2, p = (r.probe || [1, 1])[ch - 1] || 1;
      return v > 0 && (ch === 1 ? r.c1 : r.c2) ? "C" + ch + " " + fmtVdiv(+v.toPrecision(3)) + (p !== 1 ? " ×" + p : "") : "";
    };
    const vdivs = r ? [scale(1), scale(2)].filter(Boolean).join(", ") : "";
    const text = !r ? "empty" : (r.label ? r.label + " · " : "") + (chans || "no trace") +
      (r.tdiv ? " · " + fmtTdiv(r.tdiv) + "/div" : "") + (vdivs ? " · " + vdivs : "") + " · " + new Date(r.at || Date.now()).toLocaleTimeString();
    if (info.textContent !== text) info.textContent = text;
    // the setup it was stored at, which AUTO in COMPARE puts the scope back on
    const su = r && r.setup;
    const tip = su ? "Stored at " + fmtTdiv(su.tdiv_s) + "/div, trigger C" + (su.trig_src + 1) + (su.rising ? " rising" : " falling") +
      " at " + Math.round(su.pos_frac * 100) + " %, C1 " + fmtVdiv(+su.vdiv[0].toPrecision(3)) + ", C2 " + fmtVdiv(+su.vdiv[1].toPrecision(3)) +
      " (AUTO in COMPARE restores it)" : "";
    if (info.title !== tip) info.title = tip;
  }
  if (typeof wsDots === "function") wsDots();
  if (typeof cmpRender === "function" && typeof ws !== "undefined" && ws.cur === "compare") cmpRender();
}
for (const slot of ["A", "B"]) {
  // TRLC-LINKS: REQ-SDS-203
  $("refShow" + slot).onclick = () => { const r = refs[slot]; if (r) refAct(slot, r.show ? "hide" : "show"); };
  // TRLC-LINKS: REQ-SDS-203
  $("refClear" + slot).onclick = () => { if (refs[slot]) refAct(slot, "clear"); };
}

// TRLC-LINKS: REQ-SDS-071
function drawYT(g) {
  drawGrid(g);
  drawRefs(g); // references sit UNDER the live traces
  if (frame) {
    if (frame.is_env) {
      if (view.c2) drawEnv(g, frame.e2min, frame.e2max, C2COL, st ? st.zoom2 : 1, !!(st && st.inv2));
      if (view.c1) drawEnv(g, frame.e1min, frame.e1max, C1COL, st ? st.zoom1 : 1, !!(st && st.inv1));
    } else if (viewCovers(frame)) {
      // zoomed into a live envelope: the stretch read again at full detail
      if (view.c2) drawViewTrace(g, frame.vc2, frame.view_a, frame.view_b, C2COL, st ? st.zoom2 : 1, !!(st && st.inv2));
      if (view.c1) drawViewTrace(g, frame.vc1, frame.view_a, frame.view_b, C1COL, st ? st.zoom1 : 1, !!(st && st.inv1));
      drawMath(g);
    } else {
      if (view.c2) drawTraceHeld(g, frame.c2, frame.held, C2COL, st ? st.zoom2 : 1, !!(st && st.inv2));
      if (view.c1) drawTraceHeld(g, frame.c1, frame.held, C1COL, st ? st.zoom1 : 1, !!(st && st.inv1));
      drawMath(g);
    }
  }
  drawChannelMarkers(g);
  drawTrigMarkers(g);
}

// TRLC-LINKS: REQ-SDS-071
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
  ctx.fillStyle = DIMCOL; ctx.font = (12 * dpr) + "px system-ui";
  ctx.fillText("X: C1   Y: C2", 8 * dpr, 16 * dpr);
}

// TRLC-LINKS: REQ-SDS-071
function drawCursors() {
  if (!view.cursors) return;
  curSync(); // Y-T: on their signal times and levels
  const g = ctx, PH = view.mode === "YT" ? plotH() : CH;
  // TRLC-LINKS: REQ-SDS-217
  const onX = (t) => t >= 0 && t <= 1;
  // TRLC-LINKS: REQ-SDS-217
  const onY = (v) => v >= 0 && v * CH <= PH;
  g.lineWidth = dpr; g.strokeStyle = CURCOL; g.setLineDash([4 * dpr, 4 * dpr]);
  for (const t of [cur.t1, cur.t2]) { if (!onX(t)) continue; const x = t * CW; g.beginPath(); g.moveTo(x, 0); g.lineTo(x, PH); g.stroke(); }
  for (const v of [cur.v1, cur.v2]) { if (!onY(v)) continue; const y = v * CH; g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke(); }
  g.setLineDash([]);
  // handles; a cursor beyond the screen keeps its value and shows an arrow
  // at the side it is beyond
  g.fillStyle = CURCOL; g.font = "bold " + (10 * dpr) + "px system-ui";
  for (const [i, t] of [[1, cur.t1], [2, cur.t2]]) {
    if (onX(t)) { g.fillRect(t * CW - 4 * dpr, 0, 8 * dpr, 8 * dpr); continue; }
    const x = t < 0 ? 0 : CW, d = t < 0 ? 1 : -1, y = (14 + 12 * i) * dpr;
    g.beginPath(); g.moveTo(x, y); g.lineTo(x + d * 10 * dpr, y - 6 * dpr); g.lineTo(x + d * 10 * dpr, y + 6 * dpr); g.fill();
    g.fillText(i === 1 ? "A" : "B", x + d * 12 * dpr - (d < 0 ? 7 * dpr : 0), y + 4 * dpr);
  }
  for (const [i, v] of [[1, cur.v1], [2, cur.v2]]) {
    if (onY(v)) { g.fillRect(0, v * CH - 4 * dpr, 8 * dpr, 8 * dpr); continue; }
    const y = v < 0 ? 0 : PH, d = v < 0 ? 1 : -1, x = (20 + 16 * i) * dpr;
    g.beginPath(); g.moveTo(x, y); g.lineTo(x - 6 * dpr, y + d * 10 * dpr); g.lineTo(x + 6 * dpr, y + d * 10 * dpr); g.fill();
  }
}

// TRLC-LINKS: REQ-SDS-071
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

