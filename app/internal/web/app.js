"use strict";
const $ = id => document.getElementById(id);
const scope = $("scope"), ctx = scope.getContext("2d", { alpha: false });
let CW = 800, CH = 400, dpr = 1;
const DIVX = 10, DIVY = 8;

let st = null;        // last /api/status
let frame = null;     // last frame reply
let lastSeq = 0;
let lvlDragging = false, offDragging = false;
let frozen = false;
// Probe attenuation is a tip-referred display multiplier: every volts the
// client SHOWS is at the probe tip, but code/DAC math is electrical (at the
// scope input), so divide tip volts by the probe factor before sending.
const probeOf = (ch) => (st && (ch === 2 ? st.probe2 : st.probe1)) || 1;
const trigProbe = () => probeOf(st && st.trig_source === 1 ? 2 : 1);
const view = { mode: "YT", persist: false, cursors: false, c1: true, c2: true,
               win: { a: 0, b: 1 },     // visible column range as fractions of 0..cols-1
               vwin: { a: 0, b: 1 },    // visible VOLTAGE range as fractions of full scale (0=bottom)
               fwin: { a: 0, b: 1 } };  // visible FREQUENCY range as fractions of 0..Nyquist (FFT)
let userZoomed = false;   // true once the user pans/zooms → live frames stop re-homing
let lastSig = "";         // acquisition signature; a change re-homes even if zoomed
// Normalized cursor positions (fractions of width/height).
const cur = { t1: 0.33, t2: 0.66, v1: 0.4, v2: 0.6, drag: null };
// Super-res GATE markers: which region to super-res. a/b are fractions of the
// DISPLAY record (0..1), so they stay pinned to the signal through zoom/pan
// (the display is trigger-anchored). Turning the gate on AUTO-PLACES them on the
// best thing in the current view (active region, one period); after that the
// markers are the ONLY truth — arming stacks exactly what they span.
const srGate = { on: false, placed: false, a: 0.4, b: 0.6, drag: null };
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
  // Visible slice, trimmed of the -1 blank margins the deep serve pads with.
  let lo = Math.max(0, Math.round(w.a * (f.cols - 1)));
  let hi = Math.min(f.cols, Math.round(w.b * (f.cols - 1)) + 1);
  while (lo < hi && ch[lo] < 0) lo++;
  while (hi > lo && ch[hi - 1] < 0) hi--;
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
let reqCols = 2048;   // full-resolution both channels (decode + navigator); client-side zoom

// ---- navigator / horizontal zoom ----
const nav = $("nav"), navCtx = nav.getContext("2d", { alpha: false });
let NW = 0, NH = 0;
const NAVH_CSS = 56, MINSPAN = 0.004;
const navDrag = { active: false, grab: 0, a0: 0, b0: 0 };
// The ONE column<->pixel mapping: traces, overlays and decode all go through it,
// so they stay aligned at any zoom. At win={0,1} it equals the legacy i/(n-1)*(CW-1).
function xForCol(i, n) { const w = view.win; return (i / (n - 1) - w.a) / (w.b - w.a) * (CW - 1); }
function fracForX(xpx) { const w = view.win; return w.a + (xpx / (CW - 1)) * (w.b - w.a); }
function navFrac(ev) { const r = nav.getBoundingClientRect(); return Math.max(0, Math.min(1, (ev.clientX - r.left) / r.width)); }
function navY(code, zoom) { zoom = zoom || 1; return NH * (1 - (128 + (code - 128) * zoom) / 255); }
function setWin(a, b) { view.win.a = a; view.win.b = b; redraw(); }
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
  const span = Math.min(1, b - a);
  if (a < 0) { a = 0; b = span; } else if (b > 1) { b = 1; a = 1 - span; }
  return { a, b };
}
function goHome() { if (frame) { const h = homeWindow(frame); view.win.a = h.a; view.win.b = h.b; } view.vwin.a = 0; view.vwin.b = 1; userZoomed = false; clearPersist(); redraw(); }
// Acquisition signature: a change (band/env/depth/run-state) re-homes the window.
function acqSig(f) { return (f.is_env ? "E" : "") + ":" + (f.c1 ? f.c1.length : 0) + ":" + ((st && st.running) ? "R" : "S"); }

// ---- protocol decode ----
const dcfg = {
  proto: "off", scl: 1, sda: 2, clk: 1, data: 2, line: 1,
  threshold: null, auto: true, baud: 115200, bits: 8, parity: "none",
  cpol: 0, cpha: 0, msb: true, fmt: "hex", result: null,
  stream: false, hist: [], lastStreamSeq: 0,   // stitched high-bandwidth decode + packet history
  // Watch/capture: save decoded windows matching a rule to a client buffer for
  // later review. watchErr = on any decode error; watchMatch = substring/regex on
  // the transcript. captures[] holds {seq,t,reason,text,snap}; reviewIdx pins one.
  watch: false, watchErr: true, watchMatch: "", captures: [], reviewIdx: -1, lastCapKey: "",
};
const DECCOL = {
  start: "#3fb950", stop: "#e8604c", addr: "#b98cff", rw: "#8fa6b8",
  ack: "#3fb950", nak: "#e8604c", data: "#35c8e8",
  "frame-error": "#e8604c", "parity-error": "#f5a24c", gap: "#7c8894", idle: "#3a444e",
};

const css = getComputedStyle(document.body);
const C1COL = css.getPropertyValue("--c1").trim();
const C2COL = css.getPropertyValue("--c2").trim();
const CURCOL = css.getPropertyValue("--cursor").trim();
const MATHCOL = css.getPropertyValue("--math").trim();
const TRIGCOL = css.getPropertyValue("--trigger").trim();

// ---- responsive canvas ----
function resize() {
  dpr = window.devicePixelRatio || 1;
  const box = $("scopebox");
  const w = Math.max(240, Math.floor(box.clientWidth));
  const h = Math.max(120, Math.floor(box.clientHeight));
  scope.style.width = w + "px"; scope.style.height = h + "px";
  scope.width = Math.round(w * dpr); scope.height = Math.round(h * dpr);
  CW = scope.width; CH = scope.height;
  // Navigator canvas: full width, fixed CSS height.
  nav.width = Math.round(w * dpr); nav.height = Math.round(NAVH_CSS * dpr);
  NW = nav.width; NH = nav.height;
  // 2048 is 1:1 with real samples on decimated bands (the µs–ms/div range where
  // I2C/UART/slow-SPI live); native-fast bands downsample the window, tolerable
  // for mid-level thresholding. Single source of truth for the fetch width.
  reqCols = 2048;
  clearPersist();
  redraw();
}
window.addEventListener("resize", resize);

// ---- formatting ----
function eng(x, unit, digits) {
  digits = digits || 3;
  const a = Math.abs(x);
  if (a === 0) return "0 " + unit;
  const pfx = [["G",1e9],["M",1e6],["k",1e3],["",1],["m",1e-3],["µ",1e-6],["n",1e-9],["p",1e-12]];
  for (const [p, s] of pfx) if (a >= s) return (x / s).toPrecision(digits) + " " + p + unit;
  return x.toExponential(digits - 1) + " " + unit;
}
function fmtTdiv(s) {
  const strip = x => x.replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "");
  if (s >= 1) return strip(s.toPrecision(3)) + " s";
  if (s >= 1e-3) return strip((s * 1e3).toPrecision(3)) + " ms";
  if (s >= 1e-6) return strip((s * 1e6).toPrecision(3)) + " µs";
  return Math.round(s * 1e9) + " ns";
}
function fmtVdiv(v) { return v >= 1 ? v + " V" : Math.round(v * 1e3) + " mV"; }
function hexA(hex, a) {
  const h = hex.replace("#", "");
  return `rgba(${parseInt(h.substr(0, 2), 16)},${parseInt(h.substr(2, 2), 16)},${parseInt(h.substr(4, 2), 16)},${a})`;
}
function fitLabel(g, text, maxW) {
  if (maxW <= 6 * dpr) return "";
  if (g.measureText(text).width <= maxW) return text;
  let t = text;
  while (t.length > 1 && g.measureText(t).width > maxW) t = t.slice(0, -1);
  return g.measureText(t).width <= maxW ? t : "";
}

// ---- persistence layer ----
let persistCv = null, persistCtx = null;
function persistLayer() {
  if (!persistCv || persistCv.width !== CW || persistCv.height !== CH) {
    persistCv = document.createElement("canvas");
    persistCv.width = CW; persistCv.height = CH;
    persistCtx = persistCv.getContext("2d");
    persistCtx.fillStyle = "#05080c"; persistCtx.fillRect(0, 0, CW, CH);
  }
  return persistCtx;
}
function clearPersist() { if (persistCtx) { persistCtx.fillStyle = "#05080c"; persistCtx.fillRect(0, 0, CW, CH); } }

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

// winRange returns the column index range [iLo,iHi] visible in view.win (a small
// margin so segments enter/exit cleanly). Iterating only these is also faster
// when zoomed in.
function winRange(n) {
  const w = view.win;
  return [Math.max(0, Math.floor(w.a * (n - 1)) - 1), Math.min(n - 1, Math.ceil(w.b * (n - 1)) + 1)];
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

// component() is the per-redraw hot path on big stacks: a full-record tone fit
// (2 trig/sample over up to ~1.3M samples) per selected peak, run by BOTH the
// residual math and the Y-T peak overlay. On a static stack neither the source
// data nor the picked frequencies change as you pan/zoom/hover — only the view
// window — so memoize each fitted tone by (source-array identity, cycles-per-
// record). All the interactive redraws then hit the cache; it self-clears when
// the source array changes (new live frame, re-stack, or leaving stack view),
// and the shared key dedups the overlay and residual calls onto one fit.
let compMemo = { src: null, map: new Map() };
function componentMemo(src, cyclesPerLen) {
  if (compMemo.src !== src) { compMemo.src = src; compMemo.map.clear(); }
  const m = compMemo.map;
  if (m.has(cyclesPerLen)) return m.get(cyclesPerLen);
  const out = component(src, cyclesPerLen);
  if (m.size >= 16) m.delete(m.keys().next().value); // bound memory: evict oldest
  m.set(cyclesPerLen, out);
  return out;
}

// Math trace: arithmetic on C1/C2, or a RESIDUAL — a channel minus its selected
// FFT peaks — so you can null out a carrier and see the minor waves underneath.
let mathFn = "off";
// The math trace rebuilds a full-length array (a copy + a subtraction loop per
// selected tone for the residual) — cheap live, but ~1.3M points on a stack, and
// drawMath() runs it every redraw. It only changes when the source arrays, the
// math mode, or (for the residual) the picked frequencies change, so memoize the
// result and let pan/zoom/hover reuse it. Same self-clearing (by-identity) scheme
// as componentMemo.
let mathMemo = { c1: null, c2: null, fn: null, sel: "", out: null };
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
// Reference waveforms: saved snapshots (codes + their volts/code + offset)
// overlaid for comparison. A ref stays at its ABSOLUTE voltage — it is
// re-mapped to the current V/div/offset when drawn, not pinned to screen codes.
const refs = { A: null, B: null };
const REFCOL = { A: "#b48ead", B: "#a3be8c" };
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

// FFT + peak detection live in peaks.js (shared with the node e2e test).
// SELECTION IS TRACKED BY FREQUENCY, not by list index: peak magnitudes jitter
// with noise, so the strongest-first ranking (and hence any index) reshuffles
// frame-to-frame. The picked frequencies are the source of truth; every frame we
// re-locate each one to its nearest peak with nearestPeak().
// PER-CHANNEL MULTI-SELECT peak model. Each channel keeps its own peak list +
// selection so C1 and C2 spectra are inspected and picked independently (two
// boxes). SELECTION IS TRACKED BY FREQUENCY (magnitudes jitter -> a strongest-
// first index reshuffles frame-to-frame): fftCh[ch].sel holds the picked FREQs,
// re-anchored to the nearest current peak every frame; selIdx maps them to the
// current peak indices for highlighting. Click a peak/row to toggle; per-box
// "clear" empties that channel.
const fftCh = { 1: { peaks: [], sel: [], selIdx: new Set() }, 2: { peaks: [], sel: [], selIdx: new Set() } };
let maxPeaks = 8;
// Distinct palettes so a channel's selected-component overlays are attributable
// on the waveform: C1 warm, C2 cool.
const COMPCOLS = { 1: ["#ff9f1c", "#ffe14f", "#ff5ec4", "#ffb86b", "#ffd24f"], 2: ["#4fd8ff", "#8cff5a", "#b98cff", "#5ad9ff", "#6bffb8"] };
function peaksVisible() { return view.mode === "FFT" || view.mode === "YT"; }
function chOn(ch) { return ch === 1 ? view.c1 : view.c2; }
function chHas(ch) { return !!(frame && (ch === 1 ? frame.c1 : frame.c2)); }
function peakSrcCh(ch) { return frame ? (ch === 2 ? frame.c2 : frame.c1) : null; }
function peakNyq() { return frame && frame.col_span_s > 0 ? frame.c1.length / (2 * frame.col_span_s) : 0; }
// The palette slot a peak index occupies among a channel's (sorted) selection —
// keeps a peak's colour stable across the FFT markers, its list, and the overlay.
function selColorCh(ch, i) {
  const S = fftCh[ch], order = [...S.selIdx].sort((a, b) => a - b), k = order.indexOf(i);
  return k < 0 ? null : COMPCOLS[ch][k % COMPCOLS[ch].length];
}
// Recompute one channel's peaks + re-anchor its selection to the nearest current
// peak (tracks drift, dedupes, keeps a freq whose peak momentarily vanished).
// Returns that channel's spectrum, or null if the channel is off/absent/flat.
// Per-channel spectrum memo keyed by the frame array IDENTITY (every frame
// allocates fresh arrays, so identity is an exact cache key): interaction
// redraws (wheel/drag/cursor) and the peak-list refresh reuse the spectrum
// instead of re-running a full-record FFT on the same data.
const specMemo = { 1: { src: null, spec: null }, 2: { src: null, spec: null } };
// gapFill trims the -1 head/tail margins and linearly interpolates interior
// gaps so the FFT sees a UNIFORM time grid. Filtering the gaps out (the old
// behavior) compacts time and scales every reported frequency by the fill
// factor — badly wrong on a partially-filled superres stack, subtly wrong on
// a deep record's margins. Trimming doesn't change the sample interval, so
// peakNyq() (= 1/(2·dt)) stays correct.
function gapFill(src) {
  let a = 0, b = src.length - 1;
  while (a <= b && src[a] < 0) a++;
  while (b >= a && src[b] < 0) b--;
  if (b - a < 32) return null;
  const out = new Float64Array(b - a + 1);
  let last = -1;
  for (let i = a; i <= b; i++) {
    if (src[i] < 0) continue;
    if (last >= 0 && last < i - 1) {
      const v0 = out[last - a], v1 = src[i];
      for (let j = last + 1; j < i; j++) out[j - a] = v0 + (v1 - v0) * (j - last) / (i - last);
    }
    out[i - a] = src[i];
    last = i;
  }
  return out;
}
// The FFT input is capped to FFT_MAX points. Frequency RESOLUTION is set by
// the record's time span, not the point count, so decimating a huge array
// (a superres stack reaches K× the raw rate = >1M points) only lowers the
// axis' top Nyquist to FFT_MAX/(2·span) ≈ 400 MHz — which is above the raw
// Nyquist, so it keeps every real spectral line while dropping the pure
// interpolation-artifact region above it, at ~40× the FFT speed. Normal
// records (≤20480 samples) are already under the cap → unchanged.
const FFT_MAX = 32768;
function fftStride() {
  const L = frame && frame.c1 ? frame.c1.length : 0;
  return Math.max(1, Math.ceil(L / FFT_MAX));
}
// displayNyq is the effective FFT Nyquist after the decimation cap — used by
// every FFT frequency mapping so the axis stays self-consistent.
function displayNyq() { return peakNyq() / fftStride(); }
function spectrumFor(ch) {
  const src = peakSrcCh(ch), m = specMemo[ch];
  if (m.src === src) return m.spec;
  m.src = src;
  let g = gapFill(src);
  if (g) {
    const stride = fftStride();
    if (stride > 1) {
      // Pure subsample (not box-average) so a tone's magnitude — hence
      // peakVolts — is preserved exactly; the interpolation ripple aliased
      // in is negligible next to the signal.
      const dg = new Float64Array(Math.floor(g.length / stride));
      for (let i = 0; i < dg.length; i++) dg[i] = g[i * stride];
      g = dg;
    }
    m.spec = spectrum(g, displayNyq());
  } else m.spec = null;
  return m.spec;
}
function computePeaksCh(ch) {
  const S = fftCh[ch]; S.peaks = []; S.selIdx = new Set();
  if (!chOn(ch) || !chHas(ch)) return null;
  const spec = spectrumFor(ch);
  if (!spec) return null;
  S.peaks = detectPeaks(spec, { floorDb: -50, maxPeaks });
  const idxs = new Set(), kept = [];
  for (const f of S.sel) {
    const i = nearestPeak(S.peaks, f);
    if (i < 0) { kept.push(f); continue; }
    if (!idxs.has(i)) { idxs.add(i); kept.push(S.peaks[i].freq); }
  }
  S.selIdx = idxs; S.sel = kept;
  return spec;
}
function togglePeakCh(ch, pf) {
  const S = fftCh[ch], i = nearestPeak(S.peaks, pf);
  if (i < 0) return;
  const before = S.sel.length;
  S.sel = S.sel.filter(f => nearestPeak(S.peaks, f) !== i);
  if (S.sel.length === before) S.sel.push(S.peaks[i].freq); // was not selected → add
  peakListLastT = 0; // selection changed: bypass the 1 Hz list throttle this redraw
  redraw();
}
function clearPeaksCh(ch) { fftCh[ch].sel = []; peakListLastT = 0; redraw(); }

// Pointer readout state (FFT mode): frequency at x, dB at y, and each
// visible channel's curve level at that frequency.
const fftHover = { on: false, x: 0, y: 0 };
function drawFFTHover() {
  if (!fftHover.on || boxZoom.moved) return;
  const nyq = displayNyq();
  const fw = view.fwin, fspan = fw.b - fw.a;
  const frac = fw.a + fftHover.x * fspan;
  const freq = frac * nyq;
  const ptrDb = -fftHover.y * 80;
  const px = fftHover.x * CW, py = fftHover.y * CH;
  ctx.save();
  ctx.strokeStyle = "rgba(255,210,63,.4)"; ctx.lineWidth = dpr; ctx.setLineDash([3 * dpr, 3 * dpr]);
  ctx.beginPath(); ctx.moveTo(px + .5, 0); ctx.lineTo(px + .5, CH); ctx.stroke();
  ctx.beginPath(); ctx.moveTo(0, py + .5); ctx.lineTo(CW, py + .5); ctx.stroke();
  ctx.setLineDash([]);
  const lines = ["f " + eng(freq, "Hz", 4) + " · ptr " + ptrDb.toFixed(1) + " dB"];
  for (const ch of [1, 2]) {
    if (!(ch === 1 ? view.c1 : view.c2)) continue;
    const spec = specMemo[ch].spec;
    if (!spec) continue;
    const k = Math.round(frac * (spec.half - 1));
    if (k < 0 || k >= spec.half) continue;
    const db = 20 * Math.log10(spec.mags[k] / spec.peak + 1e-12);
    lines.push("C" + ch + " " + Math.max(-99.9, db).toFixed(1) + " dB");
  }
  ctx.font = (11 * dpr) + "px system-ui";
  const w = Math.max(...lines.map(t => ctx.measureText(t).width)) + 12 * dpr;
  const h = lines.length * 14 * dpr + 8 * dpr;
  const bx = px + 12 * dpr + w > CW ? px - 12 * dpr - w : px + 12 * dpr;
  const by = Math.max(4 * dpr, Math.min(CH - h - 4 * dpr, py - h / 2));
  ctx.fillStyle = "rgba(5,8,12,.88)";
  ctx.fillRect(bx, by, w, h);
  ctx.strokeStyle = "rgba(255,210,63,.5)"; ctx.strokeRect(bx + .5, by + .5, w - 1, h - 1);
  const cols = { 0: "#ffd24f", 1: C1COL, 2: C2COL };
  lines.forEach((t, i) => {
    ctx.fillStyle = i === 0 ? cols[0] : (t.startsWith("C1") ? C1COL : C2COL);
    ctx.fillText(t, bx + 6 * dpr, by + (i + 1) * 14 * dpr - 2 * dpr);
  });
  ctx.restore();
}

function drawFFT() {
  drawGrid(ctx);
  const nyq = displayNyq();
  const fw = view.fwin, fspan = fw.b - fw.a;
  const yAt = db => CH * Math.min(1, -db / 80); // 80 dB span
  for (const ch of [1, 2]) {
    const spec = computePeaksCh(ch);
    updateFFTListCh(ch);
    if (!spec) continue;
    const { mags, half, peak } = spec;
    const dbAt = k => 20 * Math.log10(mags[k] / peak + 1e-12);
    // bin (fractional) → screen x through the frequency window
    const xAt = frac => (frac / (half - 1) - fw.a) / fspan * (CW - 1);
    const base = ch === 1 ? C1COL : C2COL;
    const kLo = Math.max(0, Math.floor(fw.a * (half - 1)) - 1);
    const kHi = Math.min(half - 1, Math.ceil(fw.b * (half - 1)) + 1);
    ctx.strokeStyle = base; ctx.lineWidth = 1.3 * dpr; ctx.globalAlpha = 0.85; ctx.beginPath();
    if (kHi - kLo > 2 * CW) {
      // More bins than pixels: draw the per-pixel max envelope (peaks are
      // what matter in a spectrum) instead of one lineTo per bin.
      let started = false;
      for (let px = 0; px <= CW; px++) {
        const f0 = fw.a + (px / CW) * fspan, f1 = fw.a + ((px + 1) / CW) * fspan;
        let a = Math.floor(f0 * (half - 1)), b = Math.ceil(f1 * (half - 1));
        if (a < 0) a = 0; if (b >= half) b = half - 1;
        if (b < a) continue;
        let mx = 0; for (let k = a; k <= b; k++) if (mags[k] > mx) mx = mags[k];
        const y = yAt(20 * Math.log10(mx / peak + 1e-12));
        if (started) ctx.lineTo(px, y); else { ctx.moveTo(px, y); started = true; }
      }
    } else {
      for (let k = kLo; k <= kHi; k++) { const x = xAt(k), y = yAt(dbAt(k)); if (k === kLo) ctx.moveTo(x, y); else ctx.lineTo(x, y); }
    }
    ctx.stroke(); ctx.globalAlpha = 1;
    // peak markers; selected ones highlighted (palette) + labelled
    ctx.textBaseline = "bottom"; ctx.font = "bold " + (11 * dpr) + "px system-ui";
    for (let i = 0; i < fftCh[ch].peaks.length; i++) {
      const p = fftCh[ch].peaks[i], px = xAt(p.frac), py = yAt(p.db), col = selColorCh(ch, i), r = (col ? 6 : 3) * dpr;
      if (px < -20 || px > CW + 20) continue;
      ctx.fillStyle = col || base;
      ctx.beginPath(); ctx.moveTo(px, py - r * 2); ctx.lineTo(px - r, py - r * 0.5); ctx.lineTo(px + r, py - r * 0.5); ctx.closePath(); ctx.fill();
      if (col) {
        const tx = Math.min(px + 8 * dpr, CW - 96 * dpr);
        ctx.fillText("C" + ch + " " + eng(p.freq, "Hz", 4) + "  " + p.db.toFixed(1) + " dB", tx, Math.max(py - 10 * dpr, 30 * dpr));
      }
    }
    ctx.textBaseline = "alphabetic";
  }
  ctx.fillStyle = "#9fb0c0"; ctx.font = (12 * dpr) + "px system-ui";
  ctx.fillText("FFT  " + eng(fw.a * nyq, "Hz", 3) + " – " + eng(fw.b * nyq, "Hz", 3) +
    (fspan < 0.999 ? "  · drag/wheel to zoom, double-click resets" : "   (dB re each channel's own peak)"), 8 * dpr, 16 * dpr);
  // Physics markers on a superres stack: the fine grid extends the axis K×
  // past the RAW Nyquist, but no real signal exists beyond it — and the
  // analog front end (~100 MHz) bounds trustworthy amplitude. Mark both so
  // the extended axis can't mislead.
  if (sr.showing && sr.st && sr.st.sampleS > 0) {
    const marks = [
      { f: 1 / (2 * sr.st.sampleS), label: "raw Nyquist — no real content beyond", col: "rgba(232,96,76,.8)" },
      { f: 100e6, label: "~analog BW 100 MHz", col: "rgba(245,162,76,.7)" },
    ];
    ctx.font = (10 * dpr) + "px system-ui";
    for (const mk2 of marks) {
      const frac = mk2.f / nyq;
      if (frac <= fw.a || frac >= fw.b) continue;
      const x = (frac - fw.a) / fspan * (CW - 1);
      ctx.strokeStyle = mk2.col; ctx.lineWidth = dpr; ctx.setLineDash([8 * dpr, 5 * dpr]);
      ctx.beginPath(); ctx.moveTo(x + .5, 22 * dpr); ctx.lineTo(x + .5, CH); ctx.stroke();
      ctx.setLineDash([]);
      ctx.fillStyle = mk2.col;
      ctx.save();
      ctx.translate(x - 4 * dpr, CH * 0.45); ctx.rotate(-Math.PI / 2);
      ctx.fillText(mk2.label, 0, 0);
      ctx.restore();
    }
  }
  drawFFTHover();
}

// Y-T overlay: for each channel, reconstruct EACH selected tone from THAT
// channel's trace and draw it over the waveform (one palette-coloured curve per
// selected frequency) so picked sub-frequencies are visible as their own curves.
let peakListLastT = 0;
function drawYTPeaks(g) {
  if (view.mode !== "YT" || !frame || frame.is_env) return;
  // Nothing selected and no residual math → the spectra only feed the pick
  // lists. Refresh those at ~1 Hz instead of paying two full-record FFTs per
  // frame (the dominant avoidable per-frame client cost at 20 fps).
  const needFFT = fftCh[1].sel.length || fftCh[2].sel.length || mathFn === "res1" || mathFn === "res2";
  if (!needFFT) {
    const now = performance.now();
    if (now - peakListLastT > 1000) {
      peakListLastT = now;
      for (const ch of [1, 2]) { computePeaksCh(ch); updateFFTListCh(ch); }
    }
    return;
  }
  for (const ch of [1, 2]) { computePeaksCh(ch); updateFFTListCh(ch); }
  g.save();
  g.font = "bold " + (12 * dpr) + "px system-ui";
  let row = 0;
  for (const ch of [1, 2]) {
    const S = fftCh[ch];
    if (!S.selIdx.size) continue;
    const src = peakSrcCh(ch), zoom = ch === 1 ? (st ? st.zoom1 : 1) : (st ? st.zoom2 : 1), pal = COMPCOLS[ch];
    let k = 0;
    for (const i of [...S.selIdx].sort((a, b) => a - b)) {
      const f = S.peaks[i].freq, comp = componentMemo(src, f * frame.col_span_s);
      if (comp) {
        const col = pal[k % pal.length];
        drawTrace(g, comp, col, zoom);
        g.fillStyle = col;
        g.fillText("C" + ch + " f = " + eng(f, "Hz", 4), CW - 165 * dpr, (18 + row * 15) * dpr);
        row++;
      }
      k++;
    }
  }
  g.restore();
}

// peakVolts: absolute amplitude (volts, peak) of a spectral line. |X| of a
// sine of amplitude A through a Hann window is A·N·cg/2 with coherent gain
// cg = 0.5, so A = 4·|X|/N codes; ×vpc for volts. Parabolic-refined peaks
// under-read by up to ~15% (scalloping) — labelled ≈ for that reason.
function peakVolts(ch, p) {
  const spec = specMemo[ch].spec;
  if (!spec || !frame) return 0;
  const N = spec.half * 2;
  const mag = spec.peak * Math.pow(10, p.db / 20);
  const ampCodes = 4 * mag / N;
  return ampCodes * ((ch === 2 ? frame.vpc2 : frame.vpc1) || 1 / 32);
}

// Noise-floor magnitude of a channel's current spectrum = the MEDIAN AC-bin
// magnitude (the handful of real peaks are sparse, so they don't move the
// median). Memoized on the spectrum object, so the sort runs once per new
// spectrum, not per redraw. Lets each selected peak report its SNR above the
// floor — on a stack that's the improved (crunched) per-frequency figure.
function specFloor(ch) {
  const spec = specMemo[ch] && specMemo[ch].spec;
  if (!spec) return 0;
  if (spec._floor !== undefined) return spec._floor;
  const lo = Math.max(1, Math.floor(spec.half * 0.02)); // skip DC / near-DC
  const tmp = Array.prototype.slice.call(spec.mags, lo, spec.half).sort((a, b) => a - b);
  spec._floor = tmp.length ? tmp[tmp.length >> 1] : 0;
  return spec._floor;
}

// Selected-peak measurement lines under each FFT list: exact frequency,
// level re the channel's strongest line, absolute amplitude, and the tone's SNR
// above the noise floor (with the equivalent bits of resolution, 6.02 dB/bit).
function updateFFTSel(ch) {
  const el = $("fftSel" + ch);
  const S = fftCh[ch];
  if (!peaksVisible() || !S.selIdx.size) { el.textContent = ""; return; }
  const spec = specMemo[ch] && specMemo[ch].spec, floor = specFloor(ch);
  const rows = [];
  for (const i of [...S.selIdx].sort((a, b) => a - b)) {
    const p = S.peaks[i];
    if (!p) continue;
    let snrStr = "";
    if (spec && floor > 0) {
      const snr = 20 * Math.log10((spec.peak * Math.pow(10, p.db / 20)) / floor);
      snrStr = " · " + snr.toFixed(0) + " dB SNR (~" + Math.max(0, snr / 6.02).toFixed(1) + " bit)";
    }
    rows.push(eng(p.freq, "Hz", 4) + " · " + p.db.toFixed(1) + " dBc · ≈" + eng(peakVolts(ch, p), "Vpk", 3) + snrStr);
  }
  // Harmonic/delta line when 2+ peaks are picked: spacing and ratio.
  if (S.selIdx.size >= 2) {
    const fs = [...S.selIdx].map(i => S.peaks[i] && S.peaks[i].freq).filter(Boolean).sort((a, b) => a - b);
    if (fs.length >= 2) rows.push("Δf " + eng(fs[1] - fs[0], "Hz", 3) + " · f2/f1 " + (fs[1] / fs[0]).toFixed(3));
  }
  el.textContent = rows.join("\n");
  el.style.whiteSpace = "pre-line";
}

function updateFFTListCh(ch) {
  const card = $("fftCardC" + ch);
  if (!(peaksVisible() && chOn(ch) && chHas(ch))) { card.style.display = "none"; return; }
  card.style.display = "";
  const S = fftCh[ch], body = $("fftBody" + ch), need = S.peaks.length;
  let rows = body.querySelectorAll("tr.pk");
  // Rebuild row STRUCTURE only when the count changes — otherwise update cells in
  // place so the row elements (and your click) are never dropped.
  if (rows.length !== need) {
    let html = "<tr><th>#</th><th>Freq</th><th>dB</th></tr>";
    for (let i = 0; i < need; i++) html += `<tr class="pk" data-i="${i}" style="cursor:pointer"><th>${i + 1}</th><td class="pf"></td><td class="pd"></td></tr>`;
    if (!need) html += "<tr><td colspan='3' style='color:var(--dim)'>no peaks</td></tr>";
    body.innerHTML = html;
    rows = body.querySelectorAll("tr.pk");
  }
  for (let i = 0; i < need; i++) {
    const p = S.peaks[i], tr = rows[i], col = selColorCh(ch, i);
    tr.dataset.freq = p.freq;                 // selection carries the FREQUENCY
    tr.style.background = col ? "rgba(255,210,63,.14)" : "";
    tr.style.boxShadow = col ? `inset 3px 0 0 ${col}` : "";
    tr.querySelector(".pf").textContent = eng(p.freq, "Hz", 4);
    tr.querySelector(".pd").textContent = p.db.toFixed(1);
  }
  updateFFTSel(ch);
}
function updateFFTLists() { updateFFTListCh(1); updateFFTListCh(2); }
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

// drawSrGate overlays the super-res gate markers (magenta, matching the device)
// with a shaded region between them. Positions are RECORD fractions mapped into
// the current view, so they track the signal through zoom/pan.
function drawSrGate() {
  if (!srGate.on || view.mode !== "YT") return;
  const g = ctx, span = view.win.b - view.win.a || 1;
  const xf = f => (f - view.win.a) / span * CW;
  const xa = xf(Math.min(srGate.a, srGate.b)), xb = xf(Math.max(srGate.a, srGate.b));
  g.save();
  g.fillStyle = "rgba(230,120,240,0.10)"; g.fillRect(xa, 0, xb - xa, CH);
  g.strokeStyle = "rgb(230,120,240)"; g.lineWidth = dpr; g.fillStyle = "rgb(230,120,240)";
  for (const x of [xa, xb]) {
    g.beginPath(); g.moveTo(x, 0); g.lineTo(x, CH); g.stroke();
    g.fillRect(x - 4 * dpr, 0, 8 * dpr, 10 * dpr); g.fillRect(x - 4 * dpr, CH - 10 * dpr, 8 * dpr, 10 * dpr);
  }
  g.font = `${11 * dpr}px sans-serif`; g.fillText("gate", (xa + xb) / 2 - 11 * dpr, 12 * dpr);
  g.restore();
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

// ---- navigator overview strip ----
function drawNavTrace(cols, color) {
  if (!cols || !cols.length) return;
  const n = cols.length;
  navCtx.fillStyle = color; navCtx.globalAlpha = .75;
  for (let px = 0; px < NW; px++) {
    let lo = 256, hi = -1;
    const a = Math.floor(px / NW * n), b = Math.max(a + 1, Math.floor((px + 1) / NW * n));
    for (let i = a; i < b && i < n; i++) { const v = cols[i]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
    if (hi < 0) continue;
    const yt = navY(hi, 1), yb = navY(lo, 1);
    navCtx.fillRect(px, yt, 1, Math.max(1, yb - yt));
  }
  navCtx.globalAlpha = 1;
}
// The nav UNDERLAY (background + traces + decode lane) depends only on the
// frame arrays, channel toggles, decode result and canvas size — not on the
// zoom window. Cache it to an offscreen canvas so pan/zoom redraws blit it
// instead of re-downsampling the whole record per wheel tick.
const navCache = { key: null, cv: null };
// FFT navigator underlay: the FULL spectrum (max-per-pixel dB) per channel,
// cached by spectrum identity so pan/zoom redraws are a blit.
const navFFTCache = { key: null, cv: null };
function drawNavFFT() {
  const key = [specMemo[1].spec, specMemo[2].spec, view.c1, view.c2, NW, NH];
  if (!(navFFTCache.key && navFFTCache.key.every((v, i) => v === key[i]))) {
    navCtx.fillStyle = "#05080c"; navCtx.fillRect(0, 0, NW, NH);
    for (const ch of [1, 2]) {
      if (!(ch === 1 ? view.c1 : view.c2)) continue;
      const spec = specMemo[ch].spec;
      if (!spec) continue;
      const { mags, half, peak } = spec;
      navCtx.fillStyle = ch === 1 ? C1COL : C2COL;
      navCtx.globalAlpha = 0.7;
      for (let px = 0; px < NW; px++) {
        const a = Math.floor(px / NW * half), b = Math.max(a + 1, Math.floor((px + 1) / NW * half));
        let mx = 0;
        for (let k = a; k < b && k < half; k++) if (mags[k] > mx) mx = mags[k];
        const db = 20 * Math.log10(mx / peak + 1e-12);
        const h = Math.max(1, NH * Math.max(0, 1 + db / 80)); // 80 dB floor
        navCtx.fillRect(px, NH - h, 1, h);
      }
      navCtx.globalAlpha = 1;
    }
    if (!navFFTCache.cv || navFFTCache.cv.width !== NW || navFFTCache.cv.height !== NH) {
      navFFTCache.cv = document.createElement("canvas");
      navFFTCache.cv.width = NW; navFFTCache.cv.height = NH;
    }
    navFFTCache.cv.getContext("2d").drawImage(nav, 0, 0);
    navFFTCache.key = key;
  } else {
    navCtx.drawImage(navFFTCache.cv, 0, 0);
  }
  // viewport rectangle = the visible frequency window
  const x0 = view.fwin.a * NW, x1 = view.fwin.b * NW;
  navCtx.fillStyle = "rgba(255,159,46,.13)"; navCtx.fillRect(x0, 0, x1 - x0, NH);
  navCtx.strokeStyle = "rgba(255,159,46,.85)"; navCtx.lineWidth = 1;
  navCtx.strokeRect(x0 + .5, .5, Math.max(1, x1 - x0 - 1), NH - 1);
  navCtx.fillStyle = "rgba(255,159,46,.95)";
  navCtx.fillRect(x0 - 1, 0, 2, NH); navCtx.fillRect(x1 - 1, 0, 2, NH);
}

function drawNav() {
  if (nav.style.display === "none") return;
  if (view.mode === "FFT") { drawNavFFT(); return; }
  const key = [frame && frame.c1, frame && frame.c2, view.c1, view.c2, dcfg.result, NW, NH];
  if (navCache.key && navCache.key.every((v, i) => v === key[i])) {
    navCtx.drawImage(navCache.cv, 0, 0);
  } else {
    navCtx.fillStyle = "#05080c"; navCtx.fillRect(0, 0, NW, NH);
    navCtx.strokeStyle = "#182430"; navCtx.lineWidth = 1;
    navCtx.beginPath(); navCtx.moveTo(0, NH / 2 + .5); navCtx.lineTo(NW, NH / 2 + .5); navCtx.stroke();
    if (frame && !frame.is_env) {
      if (view.c2) drawNavTrace(frame.c2, C2COL);
      if (view.c1) drawNavTrace(frame.c1, C1COL);
    }
    drawNavDecode(); // decode tokens across the WHOLE record (small-window view)
    if (!navCache.cv || navCache.cv.width !== NW || navCache.cv.height !== NH) {
      navCache.cv = document.createElement("canvas");
      navCache.cv.width = NW; navCache.cv.height = NH;
    }
    navCache.cv.getContext("2d").drawImage(nav, 0, 0);
    navCache.key = key;
  }
  // viewport rectangle = the visible [a,b] window.
  const x0 = view.win.a * NW, x1 = view.win.b * NW;
  navCtx.fillStyle = "rgba(255,159,46,.13)"; navCtx.fillRect(x0, 0, x1 - x0, NH);
  navCtx.strokeStyle = "rgba(255,159,46,.85)"; navCtx.lineWidth = 1;
  navCtx.strokeRect(x0 + .5, .5, Math.max(1, x1 - x0 - 1), NH - 1);
  navCtx.fillStyle = "rgba(255,159,46,.95)";
  navCtx.fillRect(x0 - 1, 0, 2, NH); navCtx.fillRect(x1 - 1, 0, 2, NH);
}

// Decode lane in the NAVIGATOR: every token across the whole record, so you see
// what goes where in the small window; the main window shows the detail. Uses
// full-record nav coords (not the zoom window), colour-coded by kind, with the
// byte text drawn wherever a token is wide enough.
function drawNavDecode() {
  if (dcfg.proto === "off" || !frame || frame.is_env || !frame.c1) return;
  const r = dcfg.result;
  if (!r || !r.ok || !r.spans.length) return;
  const n = frame.c1.length, laneH = 13 * dpr, laneY = NH - laneH;
  navCtx.fillStyle = "rgba(5,8,12,0.66)"; navCtx.fillRect(0, laneY, NW, laneH);
  navCtx.textBaseline = "middle"; navCtx.textAlign = "center"; navCtx.font = "bold " + (9 * dpr) + "px system-ui";
  const nx = i => i / (n - 1) * NW, spans = r.spans, cy = laneY + laneH / 2;
  for (let k = 0; k < spans.length; k++) {
    const s = spans[k], col = DECCOL[s.kind] || "#7c8894";
    let x0 = nx(s.i0), x1 = Math.max(nx(s.i1), x0 + 1.5 * dpr);
    if (s.kind === "start" || s.kind === "stop") { navCtx.fillStyle = col; navCtx.fillRect(x0 - dpr, laneY, 1.6 * dpr, laneH); continue; }
    navCtx.fillStyle = hexA(col, 0.85); navCtx.fillRect(x0, laneY + dpr, x1 - x0, laneH - 2 * dpr);
    // Fit inside the token box, else spill right into the gap to the next token.
    if (navCtx.measureText(s.text).width <= x1 - x0 - 2 * dpr) {
      navCtx.fillStyle = "#05080c"; navCtx.textAlign = "center"; navCtx.fillText(s.text, (x0 + x1) / 2, cy);
    } else {
      const nextX0 = k + 1 < spans.length ? nx(spans[k + 1].i0) : NW;
      const label = fitLabel(navCtx, s.text, nextX0 - x0 - 2 * dpr);
      if (label) { navCtx.fillStyle = col; navCtx.textAlign = "left"; navCtx.fillText(label, x0 + 2 * dpr, cy); }
    }
  }
  navCtx.textAlign = "left"; navCtx.textBaseline = "alphabetic";
}

// ---- protocol decode: compute + render ----
function decCodes(role) { return role === 2 ? (frame && frame.c2) : (frame && frame.c1); }
function computeDecode() {
  dcfg.result = null;
  if (dcfg.proto === "off" || !frame || frame.is_env || !frame.c1) { updateDecodeResults(); return; }
  const colTimeS = (frame.col_span_s || 0) / frame.c1.length;
  const cfg = { threshold: dcfg.auto ? null : +$("decThr").value, guard: 4, fmt: dcfg.fmt };
  let r = null;
  if (dcfg.proto === "uart")
    r = decodeUART(decCodes(dcfg.line), colTimeS, Object.assign(cfg, { baud: dcfg.baud > 0 ? dcfg.baud : null, bits: dcfg.bits, parity: dcfg.parity }));
  else if (dcfg.proto === "i2c")
    r = decodeI2C(decCodes(dcfg.scl), decCodes(dcfg.sda), colTimeS, cfg);
  else if (dcfg.proto === "spi")
    r = decodeSPI(decCodes(dcfg.clk), decCodes(dcfg.data), colTimeS, Object.assign(cfg, { cpol: dcfg.cpol, cpha: dcfg.cpha, bitOrder: dcfg.msb ? "msb" : "lsb" }));
  dcfg.result = r;
  if (r && r.ok && dcfg.auto && r.meta && r.meta.threshold != null) {
    $("decThr").value = Math.round(r.meta.threshold); $("decThrV").textContent = Math.round(r.meta.threshold);
  }
  // STREAM history: each new stitch window (stream_seq advances) appends its
  // transcript to a scrolling packet log, so you accumulate everything the bus
  // shows across back-to-back captures — the "complete picture" over time.
  if (dcfg.stream && frame && frame.stream_seq && frame.stream_seq !== dcfg.lastStreamSeq) {
    dcfg.lastStreamSeq = frame.stream_seq;
    if (r && r.ok && r.text) {
      dcfg.hist.push("#" + frame.stream_seq + (frame.gap_ns ? " (gap " + (frame.gap_ns / 1e6).toFixed(1) + "ms)" : "") + ": " + r.text);
      if (dcfg.hist.length > 200) dcfg.hist.shift();
    }
  }
  // WATCH: save any window matching the rule to the review buffer (not while
  // reviewing a frozen capture). Dedup consecutive identical hits.
  if (dcfg.watch && dcfg.reviewIdx < 0 && r && r.ok) {
    const reason = watchReason(r);
    const key = reason + "|" + r.text; // dedup consecutive identical matches
    if (reason && key !== dcfg.lastCapKey) {
      dcfg.lastCapKey = key;
      dcfg.captures.push({ seq: frame.stream_seq || frame.seq || 0, reason, text: r.text, snap: frame, t: Date.now() });
      if (dcfg.captures.length > 80) dcfg.captures.shift();
      updateCaptureList();
    }
  }
  updateDecodeResults();
}
// watchReason returns why a decode matches the watch rule ("" = no match): an
// error kind (frame-error/parity-error, or NAK) and/or a transcript match of the
// user string ("/re/" = regex, else case-insensitive substring).
function watchReason(r) {
  let reason = "";
  if (dcfg.watchErr && r.spans.some(s => s.kind === "frame-error" || s.kind === "parity-error" || s.kind === "nak")) reason = "error";
  const m = (dcfg.watchMatch || "").trim();
  if (m) {
    let hit = false;
    if (m.length > 2 && m[0] === "/" && m[m.length - 1] === "/") { try { hit = new RegExp(m.slice(1, -1), "i").test(r.text); } catch (e) {} }
    else hit = r.text.toLowerCase().includes(m.toLowerCase());
    if (hit) reason = reason ? reason + "+" + m : "“" + m + "”";
  }
  return reason;
}
function updateCaptureList() {
  const card = $("captureCard");
  if (dcfg.proto === "off" || (!dcfg.watch && !dcfg.captures.length)) { card.style.display = "none"; return; }
  card.style.display = "";
  $("captureCount").textContent = dcfg.captures.length + (dcfg.reviewIdx >= 0 ? " · reviewing #" + (dcfg.reviewIdx + 1) : (dcfg.watch ? " · watching…" : ""));
  let html = "";
  for (let i = dcfg.captures.length - 1; i >= 0; i--) {
    const c = dcfg.captures[i], sel = i === dcfg.reviewIdx, err = c.reason.startsWith("error");
    html += `<div class="cap" data-i="${i}" style="cursor:pointer;padding:2px 4px;border-radius:3px;${sel ? "background:rgba(255,210,63,.18)" : ""}">` +
      `<span style="color:var(--dim)">${new Date(c.t).toLocaleTimeString()} </span>` +
      `<span style="color:${err ? "#e8604c" : "#35c8e8"}">${c.reason}</span> ` +
      `<span style="color:var(--text)">${c.text.slice(0, 46)}${c.text.length > 46 ? "…" : ""}</span></div>`;
  }
  if (!dcfg.captures.length) html = `<div style="color:var(--dim);padding:2px 4px">${dcfg.watch ? "watching — no matches yet" : "watch off"}</div>`;
  $("captureList").innerHTML = html;
  $("capLive").style.display = dcfg.reviewIdx >= 0 ? "" : "none";
}
// Review a captured window: freeze on its snapshot so you can zoom/navigate/decode
// it; "live" resumes.
function reviewCapture(i) {
  const c = dcfg.captures[i];
  if (!c) return;
  dcfg.reviewIdx = i; frozen = true; $("freeze").classList.add("on");
  frame = c.snap; view.win.a = 0; view.win.b = 1; userZoomed = false;
  computeDecode(); redraw(); updateMeas(); updateCursors(); updateCaptureList();
}
function reviewLive() {
  dcfg.reviewIdx = -1; frozen = false; $("freeze").classList.remove("on");
  updateCaptureList();
}
function updateDecodeResults() {
  const card = $("decodeResultCard");
  if (dcfg.proto === "off") { card.style.display = "none"; return; }
  card.style.display = "";
  const r = dcfg.result;
  if (frame && frame.is_env) { $("decodeText").value = "(envelope — lower time/div to decode)"; $("decodeCount").textContent = "raise time/div"; return; }
  if (dcfg.stream) { // show the accumulated packet history (newest last), auto-scroll
    const ta = $("decodeText");
    ta.value = dcfg.hist.join("\n");
    ta.scrollTop = ta.scrollHeight;
    $("decodeCount").textContent = dcfg.hist.length + " windows" + (frame && frame.gap_ns ? " · duty " + (frame.window_ns / (frame.window_ns + frame.gap_ns) * 100).toFixed(0) + "%" : "");
    return;
  }
  if (!r) { $("decodeText").value = ""; $("decodeCount").textContent = ""; return; }
  if (!r.ok) { $("decodeText").value = "(no decode)"; $("decodeCount").textContent = r.error || "no signal"; return; }
  $("decodeText").value = r.text;
  $("decodeCount").textContent = r.bytes.length + " bytes" + (r.meta && r.meta.noCS ? " · no CS" : "");
}
// On-trace decode band (YT-only), placed in a reserved bottom lane. Uses the
// windowed xForCol so labels track the trace at any zoom; spans off-window are
// culled.
function drawDecode(g) {
  if (view.mode !== "YT" || dcfg.proto === "off" || !frame || frame.is_env) return;
  const r = dcfg.result;
  if (!r || !r.ok || !r.spans.length || !frame.c1) return;
  const n = frame.c1.length, bandH = 20 * dpr, bandY = CH - bandH - 6 * dpr;
  const spans = r.spans, cy = bandY + bandH / 2;
  g.save();
  g.fillStyle = "rgba(5,8,12,0.6)"; g.fillRect(0, bandY - 2 * dpr, CW, bandH + 4 * dpr);
  g.textBaseline = "middle"; g.font = "bold " + (11 * dpr) + "px system-ui";
  for (let i = 0; i < spans.length; i++) {
    const s = spans[i];
    let x0 = xForCol(s.i0, n), x1 = xForCol(s.i1, n);
    if (x1 < 0 || x0 > CW) continue;
    const col = DECCOL[s.kind] || "#7c8894";
    if (s.kind === "start" || s.kind === "stop") {
      g.fillStyle = col; g.fillRect(x0 - dpr, bandY, 2 * dpr, bandH);
      g.beginPath(); g.moveTo(x0 - 4 * dpr, bandY); g.lineTo(x0 + 4 * dpr, bandY); g.lineTo(x0, bandY + 5 * dpr); g.fill();
      continue;
    }
    x1 = Math.max(x1, x0 + 2 * dpr); const w = x1 - x0;
    g.fillStyle = hexA(col, 0.2); g.fillRect(x0, bandY, w, bandH);
    g.strokeStyle = col; g.lineWidth = dpr; g.strokeRect(x0 + .5, bandY + .5, w - dpr, bandH - dpr);
    // Label: center it when it fits inside the box; when the box is too narrow
    // (zoomed out) let it spill RIGHT into the idle/gap up to the next token, so
    // a byte never loses its value just because its box shrank.
    g.fillStyle = col;
    if (g.measureText(s.text).width <= w - 6 * dpr) {
      g.textAlign = "center"; g.fillText(s.text, (x0 + x1) / 2, cy);
    } else {
      const nextX0 = i + 1 < spans.length ? xForCol(spans[i + 1].i0, n) : CW;
      const label = fitLabel(g, s.text, Math.min(nextX0, CW) - x0 - 4 * dpr);
      if (label) { g.textAlign = "left"; g.fillText(label, x0 + 3 * dpr, cy); }
    }
  }
  g.restore(); g.textAlign = "left"; g.textBaseline = "alphabetic";
}

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

// ---- measurements ----
function fmtMeas(key, m) {
  if (!m) return "—";
  switch (key) {
    case "Vpp": return eng(m.vpp, "V");
    case "Vmax": return eng(m.vmax, "V");
    case "Vmin": return eng(m.vmin, "V");
    case "Vmean": return eng(m.vmean, "V");
    case "Vrms": return eng(m.vrms, "V");
    case "Vtop": return eng(m.vtop, "V");
    case "Vbase": return eng(m.vbase, "V");
    case "Vampl": return eng(m.vampl, "V");
    case "Freq": return m.has_timing ? eng(m.freq, "Hz") : "—";
    case "Period": return m.has_timing ? eng(m.period, "s") : "—";
    case "Duty": return m.has_timing ? m.duty.toFixed(1) + " %" : "—";
    case "Rise": return m.rise_s > 0 ? eng(m.rise_s, "s") : "—";
    case "Fall": return m.fall_s > 0 ? eng(m.fall_s, "s") : "—";
    case "+Width": return m.pos_width_s > 0 ? eng(m.pos_width_s, "s") : "—";
    case "-Width": return m.neg_width_s > 0 ? eng(m.neg_width_s, "s") : "—";
    case "Overshoot": return m.has_timing ? m.overshoot.toFixed(1) + " %" : "—";
  }
  return "—";
}
// Measurement rows. A compact default set plus an expandable "more" group
// (timing/pulse) so the panel stays scannable but the depth is one click away.
const MEAS_CORE = ["Vpp", "Vmax", "Vmin", "Vmean", "Vrms", "Freq", "Period", "Duty"];
const MEAS_MORE = ["Vtop", "Vbase", "Vampl", "Rise", "Fall", "+Width", "-Width", "Overshoot"];
let measExpanded = false;
// The row STRUCTURE only changes on expand/collapse or a clip-flag flip; the
// VALUES change every frame. Rebuild the table on structure changes, update
// cell textContent in place otherwise — an innerHTML re-parse at 20 fps was
// measurable DOM churn for identical markup.
let measDomSig = "", measCells = null;
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

// ---- cursor dragging ----
function ptToNorm(ev) {
  const r = scope.getBoundingClientRect();
  return { x: (ev.clientX - r.left) / r.width, y: (ev.clientY - r.top) / r.height };
}

// ---- DIRECT MANIPULATION of the on-screen markers ----
// You drag the TRIGGER-LEVEL handle (right edge) or a channel's GROUND/offset
// arrow (left edge) right on the display — a vertical quantity moved vertically,
// in the direction you drag. Far better than a horizontal slider for an up/down
// value. (The footer sliders remain for fine keyboard/number entry.)
let mk = null;
function markerHit(p) {
  if (!st || view.mode !== "YT" || !frame) return null;
  const vpcT = (st.trig_source === 1 ? frame.vpc2 : frame.vpc1) || (1 / 32);
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
  redraw();
}
function commitMarker() {
  if (mk.kind === "level") send("triglevelcode", Math.round(31434 - 938 * st.trig_volts / trigProbe()));
  else { const ch = mk.kind === "off1" ? 1 : 2; send("offset" + ch, (mk.kind === "off1" ? st.off1_v : st.off2_v) / probeOf(ch)); }
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

// Rubber-band box zoom: drag a rectangle → zoom into it. In Y-T each axis
// applies only if the box has real extent there (a flat drag zooms time
// only); in FFT the x-extent sets the frequency window. Esc/short drags
// cancel; double-click resets as before.
const boxZoom = { active: false, moved: false, sx: 0, sy: 0, ex: 0, ey: 0 };
function boxRect() {
  const r = scope.getBoundingClientRect();
  const x0 = (Math.min(boxZoom.sx, boxZoom.ex) - r.left) / r.width;
  const x1 = (Math.max(boxZoom.sx, boxZoom.ex) - r.left) / r.width;
  const y0 = (Math.min(boxZoom.sy, boxZoom.ey) - r.top) / r.height;
  const y1 = (Math.max(boxZoom.sy, boxZoom.ey) - r.top) / r.height;
  const wpx = Math.abs(boxZoom.ex - boxZoom.sx), hpx = Math.abs(boxZoom.ey - boxZoom.sy);
  return { x0: Math.max(0, x0), x1: Math.min(1, x1), y0: Math.max(0, y0), y1: Math.min(1, y1), wpx, hpx };
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
let fftHoverRaf = 0;
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
function moveCursor(ev) {
  const p = ptToNorm(ev);
  const clamp = x => Math.max(0, Math.min(1, x));
  if (cur.drag[0] === "t") cur[cur.drag] = clamp(p.x); else cur[cur.drag] = clamp(p.y);
  updateCursors(); redraw();
}
// moveSrGate maps the pointer's canvas-x into a RECORD fraction (so the marker
// stays on the signal through zoom) and updates the dragged gate edge.
function moveSrGate(ev) {
  const px = ptToNorm(ev).x, span = view.win.b - view.win.a;
  srGate[srGate.drag] = Math.max(0, Math.min(1, view.win.a + px * span));
  srGate.placed = true; // user-positioned: gate toggles must not auto-replace it
  redraw();
}

// Navigator: drag to pan the viewport (click outside it first recenters it);
// double-click resets to the trigger-centered "home" slice. Separate from the
// scope's own pointer handlers, so cursor-drag / FFT-pick are untouched.
const navWin = () => view.mode === "FFT" ? view.fwin : view.win; // which window the strip controls
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

// ---- frame transport ----
// Primary: /api/frame.bin long-poll. The server PARKS the request (waitms)
// until a new frame publishes, then replies with a small JSON header + raw
// uint8 payload that binframe.js expands to Int16Array — decode is ~0 vs the
// 50-150 ms the device burned JSON-encoding int16 arrays for the old poll,
// and the 90 ms client-side gap is gone (request-when-ready is the pacing).
// Fallback: the original /api/frame JSON poll below, kept verbatim. Protocol
// mismatches (old server binary, corrupt reply) downgrade to it; a 30 s probe
// upgrades back. Network errors (OTA app restart) retry with jittered backoff
// instead — the endpoint comes back at full speed.
let transport = new URLSearchParams(location.search).get("transport") === "json" ? "json" : "bin";
let binFailures = 0;
let jsonGen = 0; // generation token: bumping it kills any older pollFrame chain

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

function fallbackToJSON() {
  if (transport === "json") return;
  transport = "json";
  pollFrame(++jsonGen); // bump the token so any older chain dies on its next tick
  setTimeout(probeBin, 30000);
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
  setTimeout(pollStatus, 1000);
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
// Buttons that are ON/OFF toggles get an aria-pressed mirror of their .on class.
const PRESSED = ["mYT", "mXY", "mFFT", "tPersist", "tCursors", "tC1", "tC2", "freeze",
  "mode", "ets", "single", "decAuto", "decWatch", "decStream"];
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
let lastLineHTML = "", lastAria = "";

// ---- controls ----
async function send(control, value) {
  try { return await (await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ control, value }) })).json(); }
  catch (e) { return { ok: false }; }
}

// nearest ladder value to a target (time/div, V/div).
function nearestLadder(target, list) {
  let best = list[0], bd = Infinity;
  for (const v of list) { const d = Math.abs(v - target); if (d < bd) { bd = d; best = v; } }
  return best;
}
// AUTOSET — one button to get a stable trace: analyse the live frame and set
// time/div (~3 cycles across the DIVX-division screen), each channel's V/div
// (fill ~6 of 8 divisions) + offset (centred), and the trigger (EDGE at the
// signal midpoint, AUTO, running) on whichever channel carries the stronger signal.
function autoset() {
  if (!frame || !st) return;
  const m1 = frame.m1, m2 = frame.m2, has = m => m && m.vpp > 0.02;
  if (frame.is_env || (!has(m1) && !has(m2))) {
    // envelope/roll (or flat): no per-sample measurements to lock onto. Drop to a
    // safe decimated timebase + AUTO/run so the next frame IS measurable, then
    // autoset again to fine-tune.
    if (st.tdivs && st.tdivs.length) { st.tdiv_s = nearestLadder(500e-6, st.tdivs); send("tdiv", st.tdiv_s); }
    send("norm", 0); send("run", 1); st.norm = false; st.running = true;
    goHome(); applyStatus();
    return;
  }
  const src = (has(m1) && (!has(m2) || m1.vpp >= m2.vpp)) ? 1 : 2;
  const sm = src === 1 ? m1 : m2;
  if (sm.freq > 0 && st.tdivs && st.tdivs.length) {
    st.tdiv_s = nearestLadder((3 / sm.freq) / DIVX, st.tdivs); // 3 cycles across DIVX divisions
    send("tdiv", st.tdiv_s);
  }
  if (st.vdivs && st.vdivs.length) {
    for (const ch of [1, 2]) {
      const m = ch === 1 ? m1 : m2;
      if (!has(m)) continue;
      const p = probeOf(ch); // measurements are tip-referred; the V/div ladder is electrical
      const vdiv = nearestLadder(m.vpp / p / 6, st.vdivs);
      send("vdiv" + ch, vdiv); send("offset" + ch, -m.vmean / p);
      if (ch === 1) { st.vdiv1 = vdiv; st.off1_v = -m.vmean; } else { st.vdiv2 = vdiv; st.off2_v = -m.vmean; }
    }
  }
  const mid = (sm.vmax + sm.vmin) / 2;
  send("triglevelcode", Math.round(31434 - 938 * mid / probeOf(src)));
  send("trigsource", src === 2 ? 1 : 0);
  send("trigtype", 0); // EDGE
  send("norm", 0);     // AUTO
  send("run", 1);      // running
  st.trig_volts = mid; st.trig_source = src === 2 ? 1 : 0; st.trig_type = 0; st.norm = false; st.running = true;
  $("lvl").value = mid.toFixed(2); $("lvlv").textContent = mid.toFixed(2) + " V";
  goHome();
  applyStatus();
}
$("autoset").onclick = autoset;
$("run").onclick = () => { const on = !(st && st.running); send("run", on ? 1 : 0); if (st) { st.running = on; applyStatus(); } };
$("single").onclick = () => { send("single", 1); if (st) { st.norm = st.running = st.single = true; applyStatus(); } };
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

function updateQualRow() {
  const t = +$("ttype").value;
  $("qualrow").style.display = t === 0 ? "none" : "flex";
  $("qp-pulse").style.display = t === 1 ? "flex" : "none";
  $("qp-slope").style.display = t === 2 ? "flex" : "none";
  $("qp-video").style.display = t === 3 ? "flex" : "none";
}
$("ttype").onchange = () => { send("trigtype", +$("ttype").value); if (st) st.trig_type = +$("ttype").value; updateQualRow(); };
async function sendParams(control, extra) {
  try { await fetch("/api/set", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(Object.assign({ control, value: 0 }, extra)) }); } catch (e) {}
}
const sendPulse = () => sendParams("pulseparams", { lvl: +$("p-lvl").value / 100, min: +$("p-min").value * 1000, max: +$("p-max").value * 1000, cond: +$("p-cond").value });
const sendSlope = () => sendParams("slopeparams", { lo: +$("s-lo").value / 100, hi: +$("s-hi").value / 100, min: +$("s-min").value * 1000, max: +$("s-max").value * 1000, cond: +$("s-cond").value });
const sendVideo = () => sendParams("videoparams", { std: +$("v-std").value, line: +$("v-line").value, neg: +$("v-neg").value === 1 });
for (const id of ["p-lvl", "p-min", "p-max", "p-cond"]) $(id).onchange = sendPulse;
for (const id of ["s-lo", "s-hi", "s-min", "s-max", "s-cond"]) $(id).onchange = sendSlope;
for (const id of ["v-std", "v-line", "v-neg"]) $(id).onchange = sendVideo;

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
$("acq").onchange = () => { send("acqmode", +$("acq").value); if (st) st.acq_mode = +$("acq").value; updateAcqN(); };
$("acqn").onchange = () => send(+$("acq").value === 1 ? "avgcount" : "eres", +$("acqn").value);
$("memdepth").onchange = () => { send("memdepth", +$("memdepth").value); userZoomed = false; }; // deeper = more to scroll, fewer fps

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
$("mYT").onclick = () => setMode("YT");
$("mXY").onclick = () => setMode("XY");
$("mFFT").onclick = () => setMode("FFT");
$("tPersist").onclick = () => { view.persist = !view.persist; $("tPersist").classList.toggle("on", view.persist); clearPersist(); redraw(); };
$("tCursors").onclick = () => { view.cursors = !view.cursors; $("tCursors").classList.toggle("on", view.cursors); updateCursors(); redraw(); };
$("tC1").onclick = () => { view.c1 = !view.c1; $("tC1").classList.toggle("on", view.c1); redraw(); };
$("tC2").onclick = () => { view.c2 = !view.c2; $("tC2").classList.toggle("on", view.c2); redraw(); };
$("freeze").onclick = () => { frozen = !frozen; $("freeze").classList.toggle("on", frozen); };

// ---- decode wiring ----
function updateDecodePanel() {
  const p = dcfg.proto;
  $("decRoles").style.display = p === "off" ? "none" : "";
  for (const c of document.querySelectorAll(".dec-uart")) c.style.display = p === "uart" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-i2c")) c.style.display = p === "i2c" ? "flex" : "none";
  for (const c of document.querySelectorAll(".dec-spi")) c.style.display = p === "spi" ? "flex" : "none";
  updateDecodeResults();
  updateCaptureList();
}
function recompute() { computeDecode(); redraw(); }
// syncDecodeControls: write dcfg back into the DOM controls (used after
// autodetect picks settings so the dropdowns/inputs reflect what it chose).
function syncDecodeControls() {
  $("decProto").value = dcfg.proto;
  $("decScl").value = String(dcfg.scl); $("decSda").value = String(dcfg.sda);
  $("decClk").value = String(dcfg.clk); $("decData").value = String(dcfg.data);
  $("decLine").value = String(dcfg.line);
  $("decBaud").value = dcfg.baud; $("decBits").value = dcfg.bits; $("decParity").value = dcfg.parity;
  $("decCpol").value = String(dcfg.cpol); $("decCpha").value = String(dcfg.cpha);
  $("decMsb").value = dcfg.msb ? "1" : "0"; $("decFmt").value = dcfg.fmt;
  $("decAuto").classList.toggle("on", dcfg.auto);
}
function detectLabel(d) {
  const b = d.result && d.result.meta || {};
  if (d.proto === "uart") return "UART · C" + d.roles.line + " · " + (b.baud ? b.baud + " bd" : "auto");
  if (d.proto === "i2c") return "I²C · SCL=C" + d.roles.scl + " SDA=C" + d.roles.sda;
  if (d.proto === "spi") return "SPI · CLK=C" + d.roles.clk + " DATA=C" + d.roles.data + " · mode " + (d.cfg.cpol * 2 + d.cfg.cpha) + " · " + (d.cfg.msb ? "MSB" : "LSB");
  return d.proto;
}
function setDetectMsg(t, err) {
  const el = $("decDetectMsg");
  el.style.display = t ? "" : "none";
  el.textContent = t || "";
  el.style.color = err ? "#e8604c" : "var(--dim)";
}
// runAutodetect: analyse the current frame, pick the best protocol + roles +
// sub-settings, apply them to dcfg + the DOM controls, and re-decode.
function runAutodetect() {
  if (!frame || !frame.c1 || frame.is_env) { setDetectMsg("no live waveform to analyse", true); return; }
  const d = autodetect(frame, { fmt: dcfg.fmt });
  if (d.proto === "off") { setDetectMsg("no protocol matched — " + (d.reason || "check the probes"), true); return; }
  dcfg.proto = d.proto;
  if (d.roles.line) dcfg.line = d.roles.line;
  if (d.roles.scl) { dcfg.scl = d.roles.scl; dcfg.sda = d.roles.sda; }
  if (d.roles.clk) { dcfg.clk = d.roles.clk; dcfg.data = d.roles.data; }
  if (d.cfg.baud != null) dcfg.baud = d.cfg.baud;     // 0 = keep auto-baud
  if (d.cfg.bits) dcfg.bits = d.cfg.bits;
  if (d.cfg.parity) dcfg.parity = d.cfg.parity;
  if (d.cfg.cpol != null) dcfg.cpol = d.cfg.cpol;
  if (d.cfg.cpha != null) dcfg.cpha = d.cfg.cpha;
  if (d.cfg.msb != null) dcfg.msb = d.cfg.msb;
  dcfg.auto = true;                                    // let the threshold auto-track
  syncDecodeControls();
  updateDecodePanel();
  recompute();
  const nb = d.result && d.result.bytes ? d.result.bytes.length : 0;
  setDetectMsg("detected " + detectLabel(d) + " · " + nb + " bytes");
}
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

// ---- superres: stack-and-crunch (align → lucky → drizzle → stack) ----
// A dedicated raw long-poll (?raw=1) feeds superres.js while armed — the
// display transport is untouched. The result is reviewed through the same
// frozen-synthetic-frame path as captures, so zoom/cursors/CSV/PNG all work
// on the stacked waveform, and "fit model" writes the analytic sum-of-
// sinusoids reconstruction into REF B for overlay comparison.
const sr = { st: null, armed: false, gen: 0, lastSeq: 0, meta: null, t0: 0, stopMode: "bits", stopVal: 4, lastBits: 0, lockRef: false, gateDt: null, lastUi: 0, ch: 0, alignCh: 0,
  showing: false, savedWin: null, // stack-view toggle state + remembered zoom
  // Offset dither: the 8-bit quantizer's staircase survives averaging when
  // the front-end noise is sub-LSB. Sweeping the offset DAC by sub-LSB steps
  // across frames (and subtracting the COMMANDED offset back in code space)
  // slides the code thresholds across the signal, so the staircase averages
  // out. dither.pending skips the one frame after each step — the DAC write
  // is staged between captures, so that frame's true offset is ambiguous.
  dither: { on: false, base: 0, steps: 8, idx: 0, pending: 0, framesAtStep: 0 } };

function srStatus(msg) { $("srStats").textContent = msg; }

function srUpdateStats(final) {
  const now = performance.now();
  if (!final && now - sr.lastUi < 500) return;
  sr.lastUi = now;
  if (!sr.st || !sr.st.frames) { srStatus("waiting for frames…"); return; }
  // Strided stats-only reduction: a full one over 1.3M bins each tick janks.
  const res = srResult(sr.st, { statsOnly: true, stride: Math.max(1, Math.ceil(sr.st.nbins / 65536)) });
  if (res.sigmaStack > 0) sr.lastBits = res.bitsGained || 0; // for the +bits stop target
  const el = ((now - sr.t0) / 1000).toFixed(0);
  const rate = res.effRateSa >= 1e9 ? (res.effRateSa / 1e9).toFixed(2) + " GSa/s" : (res.effRateSa / 1e6).toFixed(0) + " MSa/s";
  // A deposit kernel (drizzle) puts only ~frames/K contributors in each fine
  // bin, too sparse to measure per-bin σ — the time-domain bits estimate needs
  // dense (resampled) bins. Rather than report a bogus +0.0, say so and point to
  // the per-tone FFT SNR (which is the right kernel-independent figure there).
  const noise = res.sigmaStack > 0
    ? `σ ${res.sigmaSingle.toFixed(2)}→${res.sigmaStack.toFixed(3)} codes · +${res.bitsGained.toFixed(1)} bits${res.sigmaMeasured ? "" : "~"} (eff ${res.effBits.toFixed(1)})`
    : "σ n/a on this grid — read per-tone SNR in FFT";
  // Gated reference-lock stacks OCCURRENCES (hits) — one frame yields many on a
  // repetitive signal — so report hits · frames; the auto path reports frames.
  const count = res.gated ? `${res.hits} hits · ${res.frames} fr` : `${res.frames} stacked`;
  // Honest no-repeat feedback: everything rejected for a while means the GATED
  // CONTENT does not recur (as a whole) — tell the user instead of spinning.
  const noRepeat = res.gated && res.hits <= 1 && res.rejected >= 12
    ? " · gate content doesn't repeat — move/narrow the markers onto the repeating part" : "";
  srStatus(`${count} · ${res.rejected} rej` + (res.clipped ? ` (${res.clipped} clip)` : "") + (res.reseeds ? ` · reseed ${res.reseeds}` : "") +
    ` · ${el}s · ${noise} · ${rate} grid · fill ${(res.fill * 100).toFixed(1)}%` + noRepeat);
}

// srTargetReached: has the selected stop target been met? bits + stacks are
// acquisition-rate independent (the device gets the same result crunching
// slower than the engine); time is the wall-clock fallback; manual never stops.
function srTargetReached() {
  if (!sr.st || sr.stopVal <= 0) return false;
  switch (sr.stopMode) {
    case "bits":   return sr.lastBits >= sr.stopVal;
    case "stacks": return (sr.st.gated ? sr.st.hits : sr.st.frames) >= sr.stopVal;
    case "time":   return (performance.now() - sr.t0) / 1000 >= sr.stopVal;
  }
  return false;
}

function srIngest(f) {
  if (+$("srCh").value !== sr.ch) { srStop("channel changed — stack kept"); return; }
  const sig = sr.alignCh === 1 ? f.c2 : f.c1;
  if (!sig || f.is_env) { srStop("band became unsupported"); return; }
  if (!sr.st) {
    const K = +$("srK").value || 32;
    sr.st = srNew(f.cols, K);
    sr.st.kernel = $("srKernel").value || "interp"; // resample vs deposit (near-Nyquist)
    sr.st.align = sr.alignCh; // matching/alignment channel; BOTH channels stack
    sr.st.sampleS = f.sample_s || 0;
    sr.st.c[0].vpc = f.vpc1 || 1 / 32;
    sr.st.c[0].offV = f.off1_v || 0;
    sr.st.c[1].vpc = f.vpc2 || 1 / 32;
    sr.st.c[1].offV = f.off2_v || 0;
    sr.meta = { tdiv_s: f.tdiv_s, cols: f.cols, sample_s: f.sample_s, vpc1: f.vpc1, vpc2: f.vpc2 };
    if (sr.lockRef) {
      // Seed THIS frame as the match reference, then run so matching frames stack.
      // When the GATE markers are set, THEY are the region — exactly. sr.gateDt is
      // the marked span in SECONDS relative to the display's trigger edge (set at
      // ARM); anchor it on THIS raw frame's own edge. Otherwise the gate is auto.
      let gate = null;
      if (sr.gateDt) {
        if (!(f.sample_s > 0)) { srStop("no sample interval on the raw feed — can't place the gate"); return; }
        const anchor = f.edge_x != null && f.edge_x >= 0 ? f.edge_x : f.cols / 2;
        const s1 = Math.round(anchor + sr.gateDt.lo / f.sample_s);
        const s2 = Math.round(anchor + sr.gateDt.hi / f.sample_s);
        const lo = Math.max(0, s1), hi = Math.min(f.cols, s2);
        if (hi - lo < 8) { srStop("gate lies outside the captured record — move the markers nearer the trigger"); return; }
        gate = { lo, hi };
      }
      if (!srSeedRef(sr.st, f.c1, f.c2, f.edge_x != null ? f.edge_x : -1, gate)) {
        srStop("reference frame unusable (flat/clipped) — freeze a cleaner one"); return;
      }
      send("run", 1);
      srUpdateStats(false);
      return; // reference seeded; match+stack subsequent frames against it
    }
  } else if (f.cols !== sr.meta.cols || f.sample_s !== sr.meta.sample_s) {
    srStop("acquisition changed (t/div or depth) — stack kept");
    return;
  } else if (f.vpc1 !== sr.meta.vpc1 || f.vpc2 !== sr.meta.vpc2) {
    // NCC is gain-invariant and the drift fit clamps >10×, so a V/div change
    // would silently corrupt code-space stacking — stop instead.
    srStop("vertical scale changed — stack kept");
    return;
  }
  const dz = sr.dither;
  if (dz.on) {
    if (dz.pending > 0) { dz.pending--; return; } // offset still staging — skip
    // No commanded-value correction: iteration 2 of the lab showed the cal
    // volts mapping / DAC granularity mis-corrects (half-code clustering).
    // The ALIGNED drift fit in srFeed measures each frame's ACTUAL vertical
    // shift vs the base-offset reference (b-fit precision ~0.007 codes over
    // the window) and normalizes it out — whatever the DAC really did.
    // Advance the sweep every 2 ingested frames: `steps` phases spanning one
    // ADC LSB, cycling. The offset verb takes ELECTRICAL volts (the sliders
    // divide tip volts by the probe the same way); the next frame is skipped
    // (pending) while the staged DAC write lands.
    if (++dz.framesAtStep >= 2) {
      dz.framesAtStep = 0;
      dz.idx = (dz.idx + 1) % dz.steps;
      const target = dz.base - (dz.idx / dz.steps) * sr.st.c[sr.st.align].vpc; // tip volts, 0..−1 LSB
      const dch = sr.alignCh + 1; // dither sweeps the ALIGN channel's offset DAC
      send("offset" + dch, target / probeOf(dch));
      dz.pending = 1;
    }
  }
  // BOTH channels stack (the align channel drives alignment/lucky-select).
  srFeed(sr.st, f.c1, f.c2, { maxLag: 8, edgeX: f.edge_x });
  srUpdateStats(false);
  // Stop when the target is reached. stacks (exact) and bits are acquisition-rate
  // INDEPENDENT — the device, crunching slower than the engine, reaches the same
  // stack; time is the wall-clock fallback. For bits, recompute EVERY stacked
  // frame with a FIXED stride (not the throttled display cadence) so the stop
  // frame is a function of the stack, not wall-clock.
  if (sr.stopMode === "bits" && sr.stopVal > 0) {
    const r = srResult(sr.st, { statsOnly: true, stride: Math.max(1, Math.ceil(sr.st.nbins / 8192)) });
    if (r.sigmaStack > 0) sr.lastBits = r.bitsGained || 0;
  }
  if (sr.stopVal > 0 && srTargetReached()) { srStop("target reached"); return; }
}

let srFails = 0;
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

function srStop(why) {
  sr.armed = false;
  if (sr.dither.on && sr.dither.idx !== 0) {
    const dch = (sr.alignCh || 0) + 1;
    send("offset" + dch, sr.dither.base / probeOf(dch)); // restore the pre-dither offset
    sr.dither.idx = 0;
  }
  $("srArm").textContent = "ARM";
  $("srArm").classList.remove("on");
  srUpdateStats(true);
  if (why && sr.st) $("srStats").textContent += " · " + why;
  else if (why) srStatus(why);
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

// The stack view is a first-class TOGGLE: everything a single shot can do —
// measurements, FFT, X-Y, math, decode, cursors, CSV/PNG — works on the
// synthetic frame, and you can flip between live and stack freely (the
// stack zoom is remembered across visits).
function srExitView() {
  sr.showing = false;
  $("srShow").classList.remove("on");
  sr.savedWin = { a: view.win.a, b: view.win.b, zoomed: userZoomed };
  frozen = false; $("freeze").classList.remove("on");
  // Live frames resume on the next poll; the poisoned acq signature re-homes.
}
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

// ---- keyboard shortcuts + ? help overlay ----
// One declarative registry drives the keymap AND the help sheet, so adding a
// shortcut is a one-line change (the extensibility pattern from the ADR).
const KEYMAP = [
  { key: " ", label: "Space", desc: "Run / Stop", run: () => $("run").click() },
  { key: "s", label: "S", desc: "Single shot", run: () => $("single").click() },
  { key: "a", label: "A", desc: "AUTO / NORM trigger", run: () => $("mode").click() },
  { key: "t", label: "T", desc: "Trigger source C1/C2", run: () => $("source").click() },
  { key: "1", label: "1", desc: "Toggle channel 1", run: () => $("tC1").click() },
  { key: "2", label: "2", desc: "Toggle channel 2", run: () => $("tC2").click() },
  { key: "c", label: "C", desc: "Cursors", run: () => $("tCursors").click() },
  { key: "p", label: "P", desc: "Persist", run: () => $("tPersist").click() },
  { key: "z", label: "Z", desc: "Freeze", run: () => $("freeze").click() },
  { key: "y", label: "Y", desc: "Y-T view", run: () => setMode("YT") },
  { key: "x", label: "X", desc: "X-Y view", run: () => setMode("XY") },
  { key: "f", label: "F", desc: "FFT view", run: () => setMode("FFT") },
  { key: "?", label: "?", desc: "Show / hide this help", run: () => toggleHelp() },
];
// Mouse gestures — listed in the ? overlay so they're discoverable.
const MOUSEMAP = [
  { label: "Drag (empty area)", desc: "Box zoom — time and voltage (Y-T), frequency (FFT)" },
  { label: "Wheel", desc: "Zoom the time axis about the cursor (FFT: frequency axis)" },
  { label: "Shift+Wheel", desc: "Pan left / right through the record" },
  { label: "Ctrl+Wheel", desc: "Change time/div (zoom the acquisition)" },
  { label: "Double-click", desc: "Reset zoom (time + voltage; FFT: frequency)" },
  { label: "Drag ▸ handle (right)", desc: "Move the trigger level" },
  { label: "Drag ◂ arrow (left)", desc: "Move a channel's offset" },
  { label: "Shift+click", desc: "Set the trigger level where you click" },
];
function editableFocused() { const a = document.activeElement; return a && /^(INPUT|SELECT|TEXTAREA)$/.test(a.tagName); }
function toggleHelp() {
  const el = $("help");
  if (!el.classList.contains("show")) {
    const rows = arr => arr.map(x => `<tr><td><kbd>${x.label}</kbd></td><td>${x.desc}</td></tr>`).join("");
    $("helpBody").innerHTML =
      `<tr><th colspan="2" class="fcap">Keyboard</th></tr>` + rows(KEYMAP) +
      `<tr><th colspan="2" class="fcap">Mouse</th></tr>` + rows(MOUSEMAP);
  }
  el.classList.toggle("show");
}
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape") { $("help").classList.remove("show"); return; }
  if (editableFocused() || e.ctrlKey || e.metaKey || e.altKey) return;
  const k = e.key.length === 1 ? e.key.toLowerCase() : e.key;
  const m = KEYMAP.find(x => x.key === k || x.key === e.key);
  if (m) { e.preventDefault(); m.run(); }
});
$("help").onclick = (e) => { if (e.target.id === "help") $("help").classList.remove("show"); }; // click backdrop closes
function updateMathHint() {
  const h = $("mathHint");
  if (mathFn === "res1" || mathFn === "res2")
    h.textContent = "select the carrier peak(s) in the C" + (mathFn === "res1" ? 1 : 2) + " FFT list; the residual (minor waves) shows in purple";
  else h.textContent = "";
}
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


// ---- eye diagram / jitter analysis (eyejitter.js engine) ----
// The serial-analysis package: software CDR over raw records, persistence eye,
// TIE jitter (histogram, RJ/DJ, spectrum). One raw-feed consumer at a time —
// arming the eye stops superres and vice versa.
const ej = { st: null, armed: false, gen: 0, lastSeq: 0, fails: 0, lastUi: 0, vpc: 1 / 32 };
const ejStatus = m => { $("ejStats").textContent = m; };

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
function ejStop(why) {
  ej.armed = false;
  $("ejArm").textContent = "ARM";
  $("ejArm").classList.remove("on");
  if (why) ejStatus(($("ejStats").textContent || "") + " · " + why);
}

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

function ejIngest(f) {
  const ch = +$("ejCh").value === 2 ? 2 : 1;
  if (ej.ch && ch !== ej.ch) { ejStop("channel changed — re-ARM to analyze the other channel"); return; }
  ej.ch = ch;
  const sig = ch === 2 ? f.c2 : f.c1;
  if (!sig || f.is_env) { ejStop("band became unsupported"); return; }
  if (!(f.sample_s > 0)) return; // no timebase → zero-second TIE would be fabricated
  const vpc = (ch === 2 ? f.vpc2 : f.vpc1) || (1 / 32);
  ej.vpcReal = !!(ch === 2 ? f.vpc2 : f.vpc1); // mV readouts only with a real calibration
  // a V/div change rescales the codes mid-accumulation — the eye/levels would
  // mix scales; stop honestly (same policy as superres)
  if (ej.vpc0 && Math.abs(vpc - ej.vpc0) > 0.02 * ej.vpc0) { ejStop("vertical scale changed — data kept, re-ARM to continue"); return; }
  ej.vpc0 = vpc;
  ej.vpc = vpc;
  const disp = ejFeed(ej.st, sig, f.cols, f.sample_s);
  // a t/div change alters the UI in samples: every record rejects forever while
  // the panel looks live — stop honestly like the V/div and channel guards
  if (disp === "rejected:ui-inconsistent") {
    ej.incons = (ej.incons || 0) + 1;
    if (ej.incons >= 10) { ejStop("signal scale/timebase changed — data kept, re-ARM to continue"); return; }
  } else if (disp.startsWith("locked")) ej.incons = 0;
  ejRender(false);
}

// ---- rendering (throttled) ----
let ejLastUi = 0, ejEyeCv = null;
function ejRender(force) {
  const now = performance.now();
  if (!force && now - ejLastUi < 500) return;
  ejLastUi = now;
  const st2 = ej.st;
  if (!st2) return;
  const res = ejResult(st2);
  // status line
  if (res.records === 0) {
    ejStatus("no lock yet · " + st2.rejected + " rejected (" + (res.lastErr || "…") + ") — needs a clean NRZ stream");
  } else {
    ejStatus(res.records + " records · " + res.edges + " edges · " + st2.rejected + " rej" +
      (st2.rejected > 0 && res.lastErr ? " (" + res.lastErr + ")" : "") + " · " +
      eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3));
  }
  ejDrawEye(st2);
  ejDrawHist(st2, res);
  ejDrawSpec(res);
  ejMetricsTable(res);
  if (typeof ejBigVisible === "function" && ejBigVisible()) ejDrawBig();
}

// log-density heatmap: dark well -> blue -> cyan -> yellow -> white
function ejHeatColor(t) {
  const r = Math.min(255, Math.max(0, Math.round(t < 0.5 ? 0 : (t - 0.5) * 2 * 255)));
  const g = Math.min(255, Math.max(0, Math.round(t < 0.25 ? t * 4 * 130 : 130 + (t - 0.25) * 167)));
  const b = Math.min(255, Math.max(0, Math.round(t < 0.5 ? 120 + t * 270 : 255 - (t - 0.5) * 2 * 200)));
  return [r, g, b];
}
function ejDrawEye(st2) {
  const cv = $("ejEye"), g = cv.getContext("2d");
  const W = st2.eyeW, H = st2.eyeH;
  if (!ejEyeCv) { ejEyeCv = document.createElement("canvas"); ejEyeCv.width = W; ejEyeCv.height = H; }
  const og = ejEyeCv.getContext("2d");
  const img = og.createImageData(W, H);
  let mx = 0;
  for (let i = 0; i < st2.eye.length; i++) if (st2.eye[i] > mx) mx = st2.eye[i];
  const lmax = Math.log1p(mx) || 1;
  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      const d = st2.eye[y * W + x];
      const o = ((H - 1 - y) * W + x) * 4; // row 0 = lowest code -> bottom of canvas
      if (d <= 0) { img.data[o + 3] = 0; continue; }
      const t = Math.log1p(d) / lmax;
      const [r, gg, b] = ejHeatColor(t);
      img.data[o] = r; img.data[o + 1] = gg; img.data[o + 2] = b; img.data[o + 3] = 255;
    }
  }
  og.putImageData(img, 0, 0);
  g.fillStyle = "#05080c";
  g.fillRect(0, 0, cv.width, cv.height);
  g.imageSmoothingEnabled = false;
  g.drawImage(ejEyeCv, 0, 0, cv.width, cv.height);
  // UI grid: the fold spans exactly 2 UI; mark the two bit boundaries
  g.strokeStyle = "rgba(255,255,255,0.25)";
  g.setLineDash([3, 4]);
  for (const fx of [0.25, 0.5, 0.75]) {
    g.beginPath(); g.moveTo(fx * cv.width, 0); g.lineTo(fx * cv.width, cv.height); g.stroke();
  }
  g.setLineDash([]);
}
function ejDrawHist(st2, res) { ejDrawHistTo($("ejHist"), st2, res, false); }
function ejDrawHistTo(cv, st2, res, detailed) {
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c"; g.fillRect(0, 0, cv.width, cv.height);
  const tie = st2.tie;
  if (tie.length < 50) return;
  let mn = Infinity, mx = -Infinity;
  for (const v of tie) { if (v < mn) mn = v; if (v > mx) mx = v; }
  if (!(mx > mn)) return;
  const NB = detailed ? 160 : 64, hist = new Float64Array(NB);
  for (const v of tie) hist[Math.min(NB - 1, Math.floor((v - mn) / (mx - mn) * NB))]++;
  let hmax = 0;
  for (const h of hist) if (h > hmax) hmax = h;
  const pad = detailed ? 26 : 0;
  g.fillStyle = "#35c8e8";
  const bw = (cv.width - pad) / NB;
  for (let i = 0; i < NB; i++) {
    const h = hist[i] / hmax * (cv.height - 14 - pad);
    g.fillRect(pad + i * bw, cv.height - pad - h, Math.max(1, bw - 1), h);
  }
  const fs = detailed ? 14 : 10;
  g.fillStyle = "#8899aa"; g.font = fs + "px sans-serif";
  g.fillText("TIE histogram — " + eng(mx - mn, "s", 2) + " pp · " + tie.length + " edges", 6 + pad, fs + 2);
  if (detailed) {
    // x-axis ticks (TIE in engineering units) + zero marker when in range
    g.strokeStyle = "rgba(255,255,255,0.2)";
    for (const t of [0, 0.25, 0.5, 0.75, 1]) {
      const x = pad + t * (cv.width - pad);
      g.beginPath(); g.moveTo(x, cv.height - pad); g.lineTo(x, cv.height - pad + 6); g.stroke();
      const val = mn + t * (mx - mn);
      g.fillText(eng(val, "s", 2), Math.min(x + 2, cv.width - 70), cv.height - 6);
    }
    if (mn < 0 && mx > 0) {
      const x0 = pad + (0 - mn) / (mx - mn) * (cv.width - pad);
      g.strokeStyle = "rgba(255,255,255,0.35)";
      g.setLineDash([4, 5]);
      g.beginPath(); g.moveTo(x0, 0); g.lineTo(x0, cv.height - pad); g.stroke();
      g.setLineDash([]);
    }
  }
}
function ejDrawSpec(res) { ejDrawSpecTo($("ejSpec"), res, false); }
function ejDrawSpecTo(cv, res, detailed) {
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c"; g.fillRect(0, 0, cv.width, cv.height);
  const sp = res.spectrum;
  if (!sp || !res.specDf) return;
  let mx = 0;
  for (let k = 2; k < sp.length; k++) if (sp[k] > mx) mx = sp[k];
  if (!(mx > 0)) return;
  const pad = detailed ? 26 : 0;
  g.strokeStyle = "#f5d90a";
  g.beginPath();
  for (let k = 2; k < sp.length; k++) {
    const x = pad + (k - 2) / (sp.length - 2) * (cv.width - pad);
    const y = cv.height - pad - sp[k] / mx * (cv.height - 14 - pad);
    if (k === 2) g.moveTo(x, y); else g.lineTo(x, y);
  }
  g.stroke();
  const fs = detailed ? 14 : 10;
  g.fillStyle = "#8899aa"; g.font = fs + "px sans-serif";
  g.fillText("TIE spectrum — pk " + eng(res.specPeakHz, "Hz", 3) + " / " + eng(res.specPeakAmp, "s", 2), 6 + pad, fs + 2);
  if (detailed) {
    // frequency axis ticks + the measured CDR corner (below it the linear-fit
    // clock absorbs jitter — the honest measurement floor)
    const fMax = res.specDf * (sp.length - 2);
    g.strokeStyle = "rgba(255,255,255,0.2)";
    for (const t of [0, 0.25, 0.5, 0.75, 1]) {
      const x = pad + t * (cv.width - pad);
      g.beginPath(); g.moveTo(x, cv.height - pad); g.lineTo(x, cv.height - pad + 6); g.stroke();
      g.fillText(eng(t * fMax, "Hz", 2), Math.min(x + 2, cv.width - 80), cv.height - 6);
    }
    if (res.tieHpHz && res.tieHpHz < fMax) {
      const xc = pad + res.tieHpHz / fMax * (cv.width - pad);
      g.strokeStyle = "rgba(242,166,59,0.5)";
      g.setLineDash([4, 5]);
      g.beginPath(); g.moveTo(xc, 0); g.lineTo(xc, cv.height - pad); g.stroke();
      g.setLineDash([]);
      g.fillText("CDR corner", xc + 4, 30);
    }
    if (res.specPeakHz) {
      const xp = pad + res.specPeakHz / fMax * (cv.width - pad);
      g.fillStyle = "#f5d90a";
      g.fillText("▼", xp - 5, 16);
    }
  }
}
function ejMetricsTable(res) {
  const rows = [];
  const push = (k, v) => rows.push("<tr><th>" + k + "</th><td>" + v + "</td></tr>");
  if (res.bitRate) push("bit rate", eng(res.bitRate, "b/s", 5) + " (UI " + eng(res.uiSeconds, "s", 4) + ")");
  if (res.tieRms !== undefined) {
    push("TIE", eng(res.tieRms, "s", 3) + " rms · " + eng(res.tiePp, "s", 3) + " pp");
    // dual-Dirac-lite caveat: unimodal TIE (incl. periodic jitter) counts as RJ.
    // Flag when the spectrum's dominant tone explains most of the TIE power.
    const pjDominated = res.specPeakAmp && res.tieRms > 0 && (res.specPeakAmp / Math.SQRT2) > 0.6 * res.tieRms;
    push("RJ / DJ(δδ)", (res.rj !== undefined ? eng(res.rj, "s", 3) : "— (needs ≥200 edges)") + " / " +
      (res.dj ? eng(res.dj, "s", 3) : "—") + (pjDominated ? " · PJ-dominated" : ""));
    push("period / c2c", eng(res.periodJRms, "s", 3) + " / " + eng(res.c2cJRms, "s", 3) + " rms");
    // metrology honesty: the per-record linear-fit clock high-passes TIE — the
    // scope-world analogue of a golden PLL's loop bandwidth. Say so.
    if (res.tieHpHz) push("CDR corner", "&gt; " + eng(res.tieHpHz, "Hz", 2) + " measured");
  }
  const em = res.eyeMetrics;
  if (em && em.eyeHeightCodes > 0) {
    push("eye height", ej.vpcReal
      ? (em.eyeHeightCodes * ej.vpc * 1000).toFixed(1) + " mV (" + em.eyeHeightCodes.toFixed(0) + " codes)"
      : em.eyeHeightCodes.toFixed(0) + " codes");
    if (em.eyeWidthUI > 0) push("eye width", em.eyeWidthUI.toFixed(2) + " UI");
  }
  $("ejBody").innerHTML = rows.join("");
}


// ---- large views: click any diagram (eye / histogram / spectrum) to open a
// full-screen live-refreshing render with proper axes ----
let ejBigKind = "eye";
function ejOpenBig(kind) {
  if (!ej.st || ej.st.records === 0) return;
  ejBigKind = kind;
  $("ejBigWrap").classList.remove("hidden");
  ejDrawBig();
}
$("ejEye").onclick = () => ejOpenBig("eye");
$("ejHist").onclick = () => ejOpenBig("hist");
$("ejSpec").onclick = () => ejOpenBig("spec");
$("ejBigWrap").onclick = () => $("ejBigWrap").classList.add("hidden");
function ejBigVisible() { return !$("ejBigWrap").classList.contains("hidden"); }
function ejDrawBig() {
  const st2 = ej.st;
  if (!st2) return;
  const cv = $("ejBig");
  // render at the element's CSS size × devicePixelRatio for a crisp upscale
  const r = cv.getBoundingClientRect();
  const dpr2 = window.devicePixelRatio || 1;
  if (cv.width !== Math.round(r.width * dpr2)) {
    cv.width = Math.round(r.width * dpr2);
    cv.height = Math.round(r.height * dpr2);
  }
  const res = ejResult(st2);
  if (ejBigKind === "hist") ejDrawHistTo(cv, st2, res, true);
  else if (ejBigKind === "spec") ejDrawSpecTo(cv, res, true);
  else ejDrawEyeTo(cv, st2, true);
  const em = res.eyeMetrics;
  $("ejBigInfo").textContent =
    (res.bitRate ? eng(res.bitRate, "b/s", 4) + " · UI " + eng(res.uiSeconds, "s", 3) : "") +
    (res.tieRms !== undefined ? " · TIE " + eng(res.tieRms, "s", 3) + " rms · RJ " + eng(res.rj, "s", 3) + " · DJ " + (res.dj ? eng(res.dj, "s", 3) : "—") : "") +
    (em && em.eyeHeightCodes > 0 ? " · eye " + (em.eyeHeightCodes * ej.vpc * 1000).toFixed(0) + " mV / " + em.eyeWidthUI.toFixed(2) + " UI" : "") +
    " · " + res.records + " records — click to close";
}
function ejDrawEyeTo(cv, st2, detailed) {
  if (!ejEyeCv) return;
  const g = cv.getContext("2d");
  g.fillStyle = "#05080c";
  g.fillRect(0, 0, cv.width, cv.height);
  g.imageSmoothingEnabled = detailed; // large view: smooth the density upscale
  g.drawImage(ejEyeCv, 0, 0, cv.width, cv.height);
  g.strokeStyle = "rgba(255,255,255,0.25)";
  g.setLineDash([4, 6]);
  for (const fx of [0.25, 0.5, 0.75]) {
    g.beginPath(); g.moveTo(fx * cv.width, 0); g.lineTo(fx * cv.width, cv.height); g.stroke();
  }
  g.setLineDash([]);
}


// ---- zone trigger + mask testing (engine-side; this is the config surface) ----
// window property (not const): the scope draw path runs during load BEFORE
// this glue executes — a lexical binding would be a TDZ landmine there.
window.zm = {
  zones: [],      // client copy for rendering + editing: engine-format objects
  mask: null,     // {lo, hi, win} client copy for rendering
  drawArmed: false, drawA: null, drawB: null,
  failMark: null, // {frac, code} violation marker on a gallery frame
  lastRing: -1,
};

// --- coordinate transforms (display point <-> edge-anchored zone coords) ---
function zmPointToZone(p) { // p = ptToNorm point -> {dtS, code}
  if (!frame || !frame.cols || !(frame.col_span_s > 0)) return null;
  const cols = frame.cols;
  const frac = view.win.a + p.x * (view.win.b - view.win.a);
  const c = frac * (cols - 1);
  const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
  return { dtS: (c - edgeCol) * (frame.col_span_s / cols), code: codeAtY(p.y, 1) };
}
function zmZoneToRect(z) { // zone -> on-screen rect {x0,x1,y0,y1} in px, or null
  if (!frame || !frame.cols || !(frame.col_span_s > 0)) return null;
  const cols = frame.cols;
  const edgeCol = frame.edge_frac >= 0 ? frame.edge_frac * cols : cols / 2;
  const spc = frame.col_span_s / cols;
  const span = view.win.b - view.win.a || 1;
  const xOf = dt => ((edgeCol + dt / spc) / (cols - 1) - view.win.a) / span * CW;
  return {
    x0: xOf(Math.min(z.dt_lo_s, z.dt_hi_s)), x1: xOf(Math.max(z.dt_lo_s, z.dt_hi_s)),
    y0: yFor(z.code_hi, 1), y1: yFor(z.code_lo, 1),
  };
}

// --- overlay rendering (called from redraw) ---
function drawZones(g) {
  if (view.mode !== "YT") return;
  for (const z of zm.zones) {
    const r = zmZoneToRect(z);
    if (!r) continue;
    g.fillStyle = z.avoid ? "rgba(240,80,80,0.13)" : "rgba(80,220,120,0.13)";
    g.strokeStyle = z.avoid ? "rgba(240,80,80,0.6)" : "rgba(80,220,120,0.6)";
    g.fillRect(r.x0, r.y0, r.x1 - r.x0, r.y1 - r.y0);
    g.strokeRect(r.x0, r.y0, r.x1 - r.x0, r.y1 - r.y0);
  }
  if (zm.drawArmed && zm.drawA && zm.drawB) { // live preview while dragging
    const x0 = Math.min(zm.drawA.x, zm.drawB.x) * CW, x1 = Math.max(zm.drawA.x, zm.drawB.x) * CW;
    const y0 = Math.min(zm.drawA.y, zm.drawB.y) * CH, y1 = Math.max(zm.drawA.y, zm.drawB.y) * CH;
    g.setLineDash([4, 4]);
    g.strokeStyle = "rgba(80,220,120,0.9)";
    g.strokeRect(x0, y0, x1 - x0, y1 - y0);
    g.setLineDash([]);
  }
  // mask envelope (standard windowed display only; deep serves skip the render)
  if (zm.mask && frame && frame.win_frac === 1 && st && st.win_cols === zm.mask.win) {
    const win = zm.mask.win, span = view.win.b - view.win.a || 1;
    g.strokeStyle = "rgba(242,166,59,0.55)";
    for (const env of [zm.mask.lo, zm.mask.hi]) {
      g.beginPath();
      let started = false;
      for (let x = 0; x < CW; x += 2) {
        const fr = view.win.a + (x / CW) * span;
        const j = Math.round(fr * (win - 1));
        if (j < 0 || j >= win) { started = false; continue; }
        const y = yFor(env[j], 1);
        if (!started) { g.moveTo(x, y); started = true; } else g.lineTo(x, y);
      }
      g.stroke();
    }
  }
  if (zm.failMark && frame) { // violation marker on a gallery frame
    const span = view.win.b - view.win.a || 1;
    const x = (zm.failMark.frac - view.win.a) / span * CW;
    const y = yFor(zm.failMark.code, 1);
    g.strokeStyle = "#f05050";
    g.lineWidth = 2 * dpr;
    g.beginPath(); g.arc(x, y, 9 * dpr, 0, 2 * Math.PI); g.stroke();
    g.lineWidth = dpr;
  }
}

// Zone/mask test RAW capture codes; AC/GND coupling is a display-only
// transform here, so what the user sees would not be what is tested.
// Refuse to create misaligned artifacts (review finding).
function zmCplOK(ch) {
  const cpl = ch === 1 ? (st && st.cpl2) : (st && st.cpl1);
  if (cpl) {
    zmStatus("zone/mask test the RAW capture — set C" + (ch + 1) + " coupling to DC first");
    return false;
  }
  return true;
}

// --- zone drawing (armed drag; wired into the scope pointer handlers) ---
$("zmDraw").onclick = () => {
  if (!zm.drawArmed && !zmCplOK(+$("zmCh").value || 0)) return;
  zm.drawArmed = !zm.drawArmed;
  zm.drawA = zm.drawB = null;
  $("zmDraw").classList.toggle("on", zm.drawArmed);
  zmStatus(zm.drawArmed ? "drag a rectangle on the scope to add a zone" : "");
};
function zmPointerDown(p) { // true = consumed
  if (!zm.drawArmed || view.mode !== "YT") return false;
  zm.drawA = p; zm.drawB = p;
  return true;
}
function zmPointerMove(p) {
  if (!zm.drawArmed || !zm.drawA) return false;
  zm.drawB = p;
  redraw();
  return true;
}
function zmPointerUp() {
  if (!zm.drawArmed || !zm.drawA || !zm.drawB) return false;
  const a = zmPointToZone(zm.drawA), b = zmPointToZone(zm.drawB);
  zm.drawArmed = false;
  $("zmDraw").classList.remove("on");
  zm.drawA = zm.drawB = null;
  if (a && b && Math.abs(a.dtS - b.dtS) > 0) {
    zm.zones.push({
      dt_lo_s: Math.min(a.dtS, b.dtS), dt_hi_s: Math.max(a.dtS, b.dtS),
      code_lo: Math.round(Math.min(a.code, b.code)), code_hi: Math.round(Math.max(a.code, b.code)),
      avoid: false, ch: +$("zmCh").value || 0,
    });
    zmPushZones();
  }
  redraw();
  return true;
}
function zmPushZones() {
  fetch("/api/zones", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(zm.zones) }).catch(() => {});
  zmZoneList();
}
function zmZoneList() {
  const rows = zm.zones.map((z, i) =>
    `<div>z${i + 1} <button class="btn-mini zmm" data-i="${i}">${z.avoid ? "avoid" : "hit"}</button> ` +
    `${eng(z.dt_lo_s, "s", 2)}..${eng(z.dt_hi_s, "s", 2)} · ${z.code_lo}-${z.code_hi} ` +
    `<button class="btn-mini zmx" data-i="${i}">✕</button></div>`).join("");
  $("zmZoneList").innerHTML = rows;
  for (const b of document.querySelectorAll("#zmZoneList .zmm")) {
    b.onclick = () => { zm.zones[+b.dataset.i].avoid = !zm.zones[+b.dataset.i].avoid; zmPushZones(); redraw(); };
  }
  for (const b of document.querySelectorAll("#zmZoneList .zmx")) {
    b.onclick = () => { zm.zones.splice(+b.dataset.i, 1); zmPushZones(); redraw(); };
  }
}
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
  zm.mask = { lo: dLo, hi: dHi, win };
  await fetch("/api/mask", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lo: dLo, hi: dHi, win, ch }) }).catch(() => {});
  zmStatus("mask built from " + got + " frames (±" + tolT + " samp, ±" + tolV + " codes) — set test mode");
  redraw();
};
$("zmMode").onchange = () => send("maskmode", +$("zmMode").value);
$("zmClearStats").onclick = () => { send("maskclear", 0); zm.failMark = null; redraw(); };
function zmStatus(m) { if (m !== undefined) $("zmStats").textContent = m || "—"; }

// --- live meter + failure gallery (driven off the 1 Hz status poll) ---
setInterval(() => {
  if (!st) return;
  if (st.mask_mode > 0 || st.mask_fail > 0 || st.mask_pass > 0) {
    const total = (st.mask_pass || 0) + (st.mask_fail || 0);
    $("zmMeter").textContent = `pass ${st.mask_pass || 0} · FAIL ${st.mask_fail || 0}` +
      (total ? ` · ${((st.mask_fail || 0) / total * 100).toFixed(3)}%` : "") +
      (st.mask_stopped ? " · STOPPED ON FAIL" : "");
  } else $("zmMeter").textContent = "";
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
          zmStatus("failure " + (+b.dataset.i + 1) + " @ seq " + r.seq + " — violation circled (unfreeze to resume)");
        } catch (e) { }
      };
    }
  }
}, 1000);
