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
               win: { a: 0, b: 1 } };   // visible column range as fractions of 0..cols-1
let userZoomed = false;   // true once the user pans/zooms → live frames stop re-homing
let lastSig = "";         // acquisition signature; a change re-homes even if zoomed
// Normalized cursor positions (fractions of width/height).
const cur = { t1: 0.33, t2: 0.66, v1: 0.4, v2: 0.6, drag: null };
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
function goHome() { if (frame) { const h = homeWindow(frame); view.win.a = h.a; view.win.b = h.b; } userZoomed = false; redraw(); }
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
  for (let i = 1; i < DIVY; i++) { const y = Math.round(i * CH / DIVY) + .5; g.beginPath(); g.moveTo(0, y); g.lineTo(CW, y); g.stroke(); }
  g.strokeStyle = "#26384a";
  vline(CW / 2);                                                    // screen-centre reference
  g.beginPath(); g.moveTo(0, CH / 2); g.lineTo(CW, CH / 2); g.stroke();
}
function yFor(code, zoom) { zoom = zoom || 1; return CH * (1 - (128 + (code - 128) * zoom) / 255); }

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
  g.beginPath(); let pen = false;
  const n = cols.length, [iLo, iHi] = winRange(n);
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

// Math trace: arithmetic on C1/C2, or a RESIDUAL — a channel minus its selected
// FFT peaks — so you can null out a carrier and see the minor waves underneath.
let mathFn = "off";
function computeMath() {
  if (mathFn === "off" || !frame || frame.is_env || !frame.c1) return null;
  const a = frame.c1, b = frame.c2, n = a.length, out = new Array(n);
  const clip = v => v < 0 ? 0 : v > 255 ? 255 : v;
  if (mathFn === "res1" || mathFn === "res2") {
    const ch = mathFn === "res1" ? 1 : 2, src = ch === 1 ? a : b, S = fftCh[ch];
    if (!src || !S || !S.sel.length) return null;   // nothing selected → nothing to remove
    let mean = 0, cnt = 0; for (const v of src) if (v >= 0) { mean += v; cnt++; }
    mean = cnt ? mean / cnt : 128;
    const res = src.slice();
    for (const f of S.sel) {
      const comp = component(src, f * (frame.col_span_s || 0)); // fitted tone at that freq
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
function drawYT(g) {
  drawGrid(g);
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
  for (let i = 0; i < n; i++) {
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
function computePeaksCh(ch) {
  const S = fftCh[ch]; S.peaks = []; S.selIdx = new Set();
  if (!chOn(ch) || !chHas(ch)) return null;
  const spec = spectrum(peakSrcCh(ch).filter(v => v >= 0), peakNyq());
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
  redraw();
}
function clearPeaksCh(ch) { fftCh[ch].sel = []; redraw(); }

function drawFFT() {
  drawGrid(ctx);
  const nyq = peakNyq();
  const yAt = db => CH * Math.min(1, -db / 80); // 80 dB span
  for (const ch of [1, 2]) {
    const spec = computePeaksCh(ch);
    updateFFTListCh(ch);
    if (!spec) continue;
    const { mags, half, peak } = spec;
    const dbAt = k => 20 * Math.log10(mags[k] / peak + 1e-12);
    const xAt = frac => frac / (half - 1) * (CW - 1);
    const base = ch === 1 ? C1COL : C2COL;
    // spectrum curve in the channel's colour
    ctx.strokeStyle = base; ctx.lineWidth = 1.3 * dpr; ctx.globalAlpha = 0.85; ctx.beginPath();
    for (let k = 0; k < half; k++) { const x = xAt(k), y = yAt(dbAt(k)); if (k === 0) ctx.moveTo(x, y); else ctx.lineTo(x, y); }
    ctx.stroke(); ctx.globalAlpha = 1;
    // peak markers; selected ones highlighted (palette) + labelled
    ctx.textBaseline = "bottom"; ctx.font = "bold " + (11 * dpr) + "px system-ui";
    for (let i = 0; i < fftCh[ch].peaks.length; i++) {
      const p = fftCh[ch].peaks[i], px = xAt(p.frac), py = yAt(p.db), col = selColorCh(ch, i), r = (col ? 6 : 3) * dpr;
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
  ctx.fillText("FFT  0 – " + eng(nyq, "Hz", 3) + "   (dB re each channel's own peak)", 8 * dpr, 16 * dpr);
}

// Y-T overlay: for each channel, reconstruct EACH selected tone from THAT
// channel's trace and draw it over the waveform (one palette-coloured curve per
// selected frequency) so picked sub-frequencies are visible as their own curves.
function drawYTPeaks(g) {
  if (view.mode !== "YT" || !frame || frame.is_env) return;
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
      const f = S.peaks[i].freq, comp = component(src, f * frame.col_span_s);
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
function drawNav() {
  if (nav.style.display === "none") return;
  navCtx.fillStyle = "#05080c"; navCtx.fillRect(0, 0, NW, NH);
  navCtx.strokeStyle = "#182430"; navCtx.lineWidth = 1;
  navCtx.beginPath(); navCtx.moveTo(0, NH / 2 + .5); navCtx.lineTo(NW, NH / 2 + .5); navCtx.stroke();
  if (frame && !frame.is_env) {
    if (view.c2) drawNavTrace(frame.c2, C2COL);
    if (view.c1) drawNavTrace(frame.c1, C1COL);
  }
  drawNavDecode(); // decode tokens across the WHOLE record (small-window view)
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
  const nx = i => i / (n - 1) * NW;
  for (const s of r.spans) {
    const col = DECCOL[s.kind] || "#7c8894";
    let x0 = nx(s.i0), x1 = Math.max(nx(s.i1), x0 + 1.5 * dpr);
    if (s.kind === "start" || s.kind === "stop") { navCtx.fillStyle = col; navCtx.fillRect(x0 - dpr, laneY, 1.6 * dpr, laneH); continue; }
    navCtx.fillStyle = hexA(col, 0.85); navCtx.fillRect(x0, laneY + dpr, x1 - x0, laneH - 2 * dpr);
    const label = fitLabel(navCtx, s.text, x1 - x0 - 2 * dpr);
    if (label) { navCtx.fillStyle = "#05080c"; navCtx.fillText(label, (x0 + x1) / 2, laneY + laneH / 2); }
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
  g.save();
  g.fillStyle = "rgba(5,8,12,0.6)"; g.fillRect(0, bandY - 2 * dpr, CW, bandH + 4 * dpr);
  g.textBaseline = "middle"; g.textAlign = "center"; g.font = "bold " + (11 * dpr) + "px system-ui";
  for (const s of r.spans) {
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
    const label = fitLabel(g, s.text, w - 6 * dpr);
    if (label) { g.fillStyle = col; g.fillText(label, (x0 + x1) / 2, bandY + bandH / 2); }
  }
  g.restore(); g.textAlign = "left"; g.textBaseline = "alphabetic";
}

function redraw() {
  refreshAria(); // keep aria-pressed in sync with view toggles (which call redraw)
  updateStatusLine(); // time/div follows the zoom → keep it live, not just per status poll
  if (view.mode === "XY") { clearPersist(); drawXY(); drawCursors(); return; }
  if (view.mode === "FFT") { clearPersist(); drawFFT(); drawCursors(); return; }
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
  drawNav();
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
    case "Freq": return m.freq > 0 ? eng(m.freq, "Hz") : "—";
    case "Period": return m.period > 0 ? eng(m.period, "s") : "—";
    case "Duty": return m.freq > 0 ? m.duty.toFixed(1) + " %" : "—";
  }
  return "—";
}
function updateMeas() {
  const keys = ["Vpp", "Vmax", "Vmin", "Vmean", "Vrms", "Freq", "Period", "Duty"];
  const clipTag = c => c ? ` <span class="clip" title="signal is clipping — increase V/div or probe; measurements unreliable">⚠ CLIP</span>` : "";
  let html = `<tr><th></th><td class="cc1">C1${clipTag(frame && frame.clip1)}</td>` +
             `<td class="cc2">C2${clipTag(frame && frame.clip2)}</td></tr>`;
  for (const k of keys) {
    html += `<tr><th>${k}</th><td class="cc1">${fmtMeas(k, frame && frame.m1)}</td>` +
            `<td class="cc2">${fmtMeas(k, frame && frame.m2)}</td></tr>`;
  }
  $("measBody").innerHTML = html;
}

function updateCursors() {
  if (!view.cursors || !frame) { $("curCard").style.display = "none"; return; }
  $("curCard").style.display = "";
  // Cursors are screen fractions; the screen now spans (b-a) of the record.
  const dt = Math.abs(cur.t2 - cur.t1) * (frame.col_span_s || 0) * (view.win.b - view.win.a);
  // A full-height drag spans 255 codes ÷ vertical zoom (the 2 mV/5 mV detents
  // render magnified), times volts/code (already probe-scaled). Per channel.
  const z1 = (st && st.zoom1) || 1, z2 = (st && st.zoom2) || 1;
  const vFull1 = 255 * (frame.vpc1 || 1 / 32) / z1, vFull2 = 255 * (frame.vpc2 || 1 / 32) / z2;
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
    const volts = (255 * (1 - cy) - 128) * mk.vpc; // top of screen = higher level = up
    st.trig_volts = volts; $("lvl").value = volts.toFixed(2); $("lvlv").textContent = volts.toFixed(2) + " V";
  } else {
    const ch = mk.kind === "off1" ? 1 : 2, offV = (255 * (1 - cy) - 128) / mk.zoom * mk.vpc;
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
    const volts = (255 * (1 - Math.max(0, Math.min(1, ptToNorm(ev).y))) - 128) * vpc;
    st.trig_volts = volts; $("lvl").value = volts.toFixed(2); $("lvlv").textContent = volts.toFixed(2) + " V";
    send("triglevelcode", Math.round(31434 - 938 * volts / trigProbe())); redraw();
    return;
  }
  if (view.mode === "FFT") { // toggle the nearest peak, across both channels
    const clickedFreq = ptToNorm(ev).x * peakNyq(); // x maps ~linearly to 0..Nyquist
    let best = null;
    for (const ch of [1, 2]) {
      const idx = nearestPeak(fftCh[ch].peaks, clickedFreq);
      if (idx < 0) continue;
      const dx = Math.abs(fftCh[ch].peaks[idx].freq - clickedFreq);
      if (!best || dx < best.dx) best = { ch, freq: fftCh[ch].peaks[idx].freq, dx };
    }
    if (best) togglePeakCh(best.ch, best.freq);
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
  if (!view.cursors) return;
  const p = ptToNorm(ev);
  const near = (a, b) => Math.abs(a - b) < 0.025;
  if (near(p.y, cur.v1)) cur.drag = "v1"; else if (near(p.y, cur.v2)) cur.drag = "v2";
  else if (near(p.x, cur.t1)) cur.drag = "t1"; else if (near(p.x, cur.t2)) cur.drag = "t2";
  else return; // click not near any cursor → do nothing (don't yank one; keeps dbl-click reset clean)
  scope.setPointerCapture(ev.pointerId);
  moveCursor(ev);
});
scope.addEventListener("pointermove", ev => {
  if (mk) { moveMarker(ev); return; }
  if (cur.drag) { moveCursor(ev); return; }
  const h = markerHit(ptToNorm(ev));    // hover affordance
  scope.style.cursor = h ? "ns-resize" : (view.cursors ? "crosshair" : "default");
});
scope.addEventListener("pointerup", () => {
  if (mk) { commitMarker(); lvlDragging = offDragging = false; mk = null; }
  cur.drag = null;
});
function moveCursor(ev) {
  const p = ptToNorm(ev);
  const clamp = x => Math.max(0, Math.min(1, x));
  if (cur.drag[0] === "t") cur[cur.drag] = clamp(p.x); else cur[cur.drag] = clamp(p.y);
  updateCursors(); redraw();
}

// Navigator: drag to pan the viewport (click outside it first recenters it);
// double-click resets to the trigger-centered "home" slice. Separate from the
// scope's own pointer handlers, so cursor-drag / FFT-pick are untouched.
nav.addEventListener("pointerdown", ev => {
  if (view.mode !== "YT") return;
  userZoomed = true;
  let f = navFrac(ev), w = view.win, s = w.b - w.a;
  if (f < w.a || f > w.b) { const na = Math.max(0, Math.min(1 - s, f - s / 2)); setWin(na, na + s); }
  navDrag.active = true; navDrag.grab = f; navDrag.a0 = view.win.a; navDrag.b0 = view.win.b;
  nav.setPointerCapture(ev.pointerId);
});
nav.addEventListener("pointermove", ev => {
  if (!navDrag.active) return;
  const f = navFrac(ev), s = navDrag.b0 - navDrag.a0;
  const na = Math.max(0, Math.min(1 - s, navDrag.a0 + (f - navDrag.grab)));
  setWin(na, na + s);
});
nav.addEventListener("pointerup", () => navDrag.active = false);
nav.addEventListener("dblclick", () => goHome());

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
scope.addEventListener("dblclick", () => { if (view.mode === "YT") goHome(); }); // reset zoom to full
scope.addEventListener("wheel", ev => {
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

// ---- polling ----
async function pollFrame() {
  if (!frozen) {
    try {
      const r = await fetch("/api/frame?since=" + lastSeq + "&cols=" + reqCols + "&full=1");
      const f = await r.json();
      if (!f.unchanged) {
        frame = f; lastSeq = f.seq;
        const sig = acqSig(f);
        if (sig !== lastSig) { userZoomed = false; lastSig = sig; } // band/depth/run change → re-home
        if (!userZoomed) { const h = homeWindow(f); view.win.a = h.a; view.win.b = h.b; }
        computeDecode(); redraw(); updateMeas(); updateCursors();
      }
    } catch (e) { /* keep last */ }
  }
  setTimeout(pollFrame, 90);
}
async function pollStatus() {
  try { st = await (await fetch("/api/status")).json(); applyStatus(); }
  catch (e) { $("line").textContent = "no connection"; }
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
  for (const id of PRESSED) { const b = $(id); if (b) b.setAttribute("aria-pressed", b.classList.contains("on") ? "true" : "false"); }
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
  $("line").innerHTML =
    "<b>" + fmtTdiv(b) + "/div</b>" + zoomTxt + " · " + st.band + " · " + st.fps.toFixed(0) + " fps · seq <b>" + st.seq + "</b>" +
    " · cols " + reqCols + (st.mmap_drain ? "" : " (ioctl)") + (st.dead_runs ? " · DEAD " + st.dead_runs : "") +
    " · cal:" + (st.cal_source || "?") + " · " + st.version;
  $("scope").setAttribute("aria-label", "oscilloscope — trigger " + trigState() + ", " + fmtTdiv(b) + "/div");
}

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
  // The navigator lives in Y-T only; toggling it changes the scope height, so
  // re-measure (resize() ends with redraw()).
  nav.style.display = m === "YT" ? "" : "none";
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

$("ePNG").onclick = () => {
  const a = document.createElement("a");
  a.download = "scope-" + (frame ? frame.seq : 0) + ".png";
  a.href = scope.toDataURL("image/png"); a.click();
};
$("eCSV").onclick = () => {
  if (!frame || !frame.c1) return;
  const dt = (frame.col_span_s || 0) / frame.c1.length;
  // Emit calibrated, probe-referred volts (same mapping as the measurements):
  // v = (code-128)·vpc − offset. vpc and offset are already tip-scaled.
  const vpc1 = frame.vpc1 || (1 / 32), vpc2 = frame.vpc2 || (1 / 32);
  const o1 = frame.off1_v || 0, o2 = frame.off2_v || 0;
  const toV = (code, vpc, off) => (code === undefined ? "" : ((code - 128) * vpc - off).toExponential(6));
  let csv = "# open-sds1000cml capture seq=" + frame.seq +
    " tdiv_s=" + (frame.tdiv_s || 0) + " probe_c1=" + (st ? st.probe1 || 1 : 1) +
    " probe_c2=" + (st ? st.probe2 || 1 : 1) + "\n";
  csv += "t_s,c1_v,c2_v\n";
  for (let i = 0; i < frame.c1.length; i++)
    csv += (i * dt).toExponential(6) + "," + toV(frame.c1[i], vpc1, o1) + "," +
      toV(frame.c2 ? frame.c2[i] : undefined, vpc2, o2) + "\n";
  const a = document.createElement("a");
  a.download = "scope-" + frame.seq + ".csv";
  a.href = URL.createObjectURL(new Blob([csv], { type: "text/csv" })); a.click();
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
  { label: "Wheel", desc: "Zoom the time axis about the cursor" },
  { label: "Shift+Wheel", desc: "Pan left / right through the record" },
  { label: "Ctrl+Wheel", desc: "Change time/div (zoom the acquisition)" },
  { label: "Double-click", desc: "Reset zoom to the full record" },
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
pollFrame();
pollStatus();
