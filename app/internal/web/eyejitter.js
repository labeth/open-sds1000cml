// eyejitter.js — eye-diagram + jitter analysis engine (the serial-analysis
// package high-end scopes sell as a paid option), self-contained: no DOM, no
// external libs; loaded as a classic script in the browser and require()d by
// the node test. Pipeline per raw record:
//
//   edges: hysteresis mid-level crossings, sub-sample by linear interpolation
//   CDR:   robust UI estimate (interval clustering) + 2-pass least-squares fit
//          of edge times to an ideal grid t = t0 + n*UI (linear-fit reference
//          clock — the standard TIE reference)
//   TIE:   t_k − ideal_k, aggregated: histogram, RJ/DJ (dual-Dirac-lite),
//          per-record spectrum on the UI grid (Hann + FFT), magnitude-averaged
//          across records (trigger phase is incoherent record-to-record)
//   eye:   every sample folded at phase (t − t0) mod 2UI into a density map
//
// Honesty rules: a record that does not LOCK (too few edges, inconsistent UI,
// fit residual too large) is rejected, never guessed at. All metrics report
// what was measured at the accumulated density — no BER extrapolation.
"use strict";

// ejNew allocates the analysis state. opts: {eyeW, eyeH, fftN}
function ejNew(opts) {
  opts = opts || {};
  const eyeW = opts.eyeW || 256, eyeH = opts.eyeH || 128;
  return {
    eyeW, eyeH,
    eye: new Float64Array(eyeW * eyeH), // density counts (float: fractional deposits)
    lo: 255, hi: 0,                     // code range seen (for the eye y-scale)
    ui: 0,                              // running UI estimate (samples)
    uiN: 0,                             // records contributing to ui
    sampleS: 0,
    records: 0, rejected: 0, edges: 0,
    tie: [],                            // aggregated TIE values (seconds), capped
    tieCap: opts.tieCap || 60000,
    periodJ: [], c2cJ: [],              // period + cycle-cycle jitter samples (s)
    fftN: opts.fftN || 512,
    spec: null, specN: 0,               // magnitude-sum spectrum + record count
    specDf: 0,                          // Hz per spectrum bin
    lastErr: "",
  };
}

// ---------- edges ----------

// ejEdges extracts all mid-level crossings with hysteresis (±15% of swing) and
// sub-sample linear interpolation. Returns {t:[...positions in samples], pol:[+1/-1]}
// or null when the record has no usable swing.
function ejEdges(sig, n) {
  // robust levels: p5/p95 of a strided sample (cheap, rail-insensitive)
  const stride = Math.max(1, n >> 12), vals = [];
  for (let i = 0; i < n; i += stride) vals.push(sig[i]);
  vals.sort((a, b) => a - b);
  const lo = vals[Math.floor(vals.length * 0.05)], hi = vals[Math.floor(vals.length * 0.95)];
  if (hi - lo < 24) return null; // no serial swing
  const mid = (lo + hi) / 2, hys = (hi - lo) * 0.15;
  const t = [], pol = [];
  let state = sig[0] > mid ? 1 : -1;
  for (let i = 1; i < n; i++) {
    const v = sig[i];
    if (state < 0 && v >= mid + hys) {
      // rising: locate the mid crossing between the last below-mid sample and here
      let j = i;
      while (j > 0 && sig[j - 1] >= mid) j--;
      if (j > 0) {
        const a = sig[j - 1], b = sig[j];
        if (b !== a) { t.push(j - 1 + (mid - a) / (b - a)); pol.push(1); }
      }
      state = 1;
    } else if (state > 0 && v <= mid - hys) {
      let j = i;
      while (j > 0 && sig[j - 1] <= mid) j--;
      if (j > 0) {
        const a = sig[j - 1], b = sig[j];
        if (b !== a) { t.push(j - 1 + (mid - a) / (b - a)); pol.push(-1); }
      }
      state = -1;
    }
  }
  return t.length >= 3 ? { t, pol, lo, hi, mid } : null;
}

// ---------- CDR ----------

// ejEstimateUI: robust unit-interval estimate from consecutive-edge intervals.
// NRZ intervals are k·UI (k = run lengths); seed with a low percentile, then
// refine by dividing each interval by its nearest integer multiple. Returns
// UI in samples, or 0 when the intervals don't cluster on a common grid.
function ejEstimateUI(t) {
  const d = [];
  for (let i = 1; i < t.length; i++) d.push(t[i] - t[i - 1]);
  if (d.length < 8) return 0;
  const s = [...d].sort((a, b) => a - b);
  let ui = s[Math.floor(s.length * 0.1)]; // near the shortest run
  if (!(ui > 1)) return 0;
  for (let pass = 0; pass < 3; pass++) {
    let num = 0, den = 0, bad = 0;
    for (const dd of d) {
      const k = Math.round(dd / ui);
      if (k < 1 || k > 16) { bad++; continue; }
      const frac = Math.abs(dd / ui - k);
      if (frac > 0.3) { bad++; continue; } // off-grid interval
      num += dd; den += k;
    }
    if (den === 0 || bad > d.length * 0.3) return 0; // not a serial bit grid
    ui = num / den;
  }
  return ui;
}

// ejFitGrid: 2-pass least-squares fit of edge times to t = t0 + n·UI, n
// re-derived after the first pass. Returns {t0, ui, tie:[...samples], nIdx:[...]}
// or null if the fit does not lock (residual too large = not one bit grid).
function ejFitGrid(t, ui0) {
  let t0 = t[0], ui = ui0;
  let nIdx = null;
  for (let pass = 0; pass < 2; pass++) {
    nIdx = new Array(t.length);
    // n_k assignment against the current grid
    for (let i = 0; i < t.length; i++) nIdx[i] = Math.round((t[i] - t0) / ui);
    // least squares for (t0, ui): t_k = t0 + n_k*ui
    let sn = 0, st = 0, snn = 0, snt = 0;
    const m = t.length;
    for (let i = 0; i < m; i++) {
      const n = nIdx[i], tt = t[i];
      sn += n; st += tt;
      const p = n * n; snn += p;
      const q = n * tt; snt += q;
    }
    const den = m * snn - sn * sn;
    if (den <= 0) return null;
    ui = (m * snt - sn * st) / den;
    t0 = (st - ui * sn) / m;
    if (!(ui > 1)) return null;
  }
  const tie = new Array(t.length);
  let ss = 0;
  for (let i = 0; i < t.length; i++) {
    tie[i] = t[i] - (t0 + nIdx[i] * ui);
    const p = tie[i] * tie[i];
    ss += p;
  }
  const rms = Math.sqrt(ss / t.length);
  if (rms > 0.25 * ui) return null; // no lock: residuals are not a jitter, it's not a grid
  return { t0, ui, tie, nIdx, rms };
}

// ---------- FFT (compact radix-2, self-contained for node + browser) ----------
function ejFFT(re, im) {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) { // bit-reverse permute
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) { let x = re[i]; re[i] = re[j]; re[j] = x; x = im[i]; im[i] = im[j]; im[j] = x; }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = -2 * Math.PI / len, wr = Math.cos(ang), wi = Math.sin(ang);
    for (let i = 0; i < n; i += len) {
      let cr = 1, ci = 0;
      for (let k = 0; k < len / 2; k++) {
        const ur = re[i + k], ui_ = im[i + k];
        const vr = re[i + k + len / 2] * cr - im[i + k + len / 2] * ci;
        const vi = re[i + k + len / 2] * ci + im[i + k + len / 2] * cr;
        re[i + k] = ur + vr; im[i + k] = ui_ + vi;
        re[i + k + len / 2] = ur - vr; im[i + k + len / 2] = ui_ - vi;
        const ncr = cr * wr - ci * wi; ci = cr * wi + ci * wr; cr = ncr;
      }
    }
  }
}

// ---------- per-record feed ----------

// ejFeed analyzes one raw record. sig: Uint8/Int16Array codes, n valid samples,
// sampleS seconds/sample. Returns "locked:<edges>" | "rejected:<why>".
function ejFeed(st, sig, n, sampleS) {
  const e = ejEdges(sig, n);
  if (!e) { st.rejected++; st.lastErr = "no-swing"; return "rejected:no-swing"; }
  const ui0 = ejEstimateUI(e.t);
  if (!ui0) { st.rejected++; st.lastErr = "no-bit-grid"; return "rejected:no-bit-grid"; }
  // UI consistency across records: a drifting/false lock is not the same signal
  if (st.uiN > 0 && Math.abs(ui0 - st.ui) > 0.02 * st.ui) {
    st.rejected++; st.lastErr = "ui-inconsistent";
    return "rejected:ui-inconsistent";
  }
  const fit = ejFitGrid(e.t, ui0);
  if (!fit) { st.rejected++; st.lastErr = "no-lock"; return "rejected:no-lock"; }
  st.ui = st.uiN === 0 ? fit.ui : st.ui + (fit.ui - st.ui) / Math.min(st.uiN + 1, 32);
  st.uiN++;
  st.sampleS = sampleS;
  if (e.lo < st.lo) st.lo = e.lo;
  if (e.hi > st.hi) st.hi = e.hi;

  // --- TIE aggregation (seconds) ---
  for (let i = 0; i < fit.tie.length && st.tie.length < st.tieCap; i++) {
    st.tie.push(fit.tie[i] * sampleS);
  }
  // period jitter (consecutive same-polarity edge spacing error) + cycle-cycle
  let prevP = null, prevPeriod = null;
  for (let i = 1; i < e.t.length; i++) {
    const dk = fit.nIdx[i] - fit.nIdx[i - 1];
    if (dk < 1) continue;
    const err = (e.t[i] - e.t[i - 1] - dk * fit.ui) * sampleS;
    if (st.periodJ.length < st.tieCap) st.periodJ.push(err);
    if (prevP !== null && st.c2cJ.length < st.tieCap) st.c2cJ.push(err - prevPeriod);
    prevP = i; prevPeriod = err;
  }
  st.edges += e.t.length;

  // --- TIE spectrum on the UI grid (per record, magnitude-averaged) ---
  const nUI = fit.nIdx[fit.nIdx.length - 1] - fit.nIdx[0];
  if (nUI >= 32) {
    const N = st.fftN;
    const grid = new Float64Array(N);
    const have = new Uint8Array(N);
    const base = fit.nIdx[0];
    for (let i = 0; i < fit.nIdx.length; i++) {
      const idx = fit.nIdx[i] - base;
      if (idx >= 0 && idx < N) { grid[idx] = fit.tie[i] * sampleS; have[idx] = 1; }
    }
    const span = Math.min(N, nUI + 1);
    // linear-interp the data-dependent gaps (PRBS7 max run 7 UI)
    let last = -1;
    for (let i = 0; i < span; i++) {
      if (have[i]) {
        if (last >= 0 && i - last > 1) {
          for (let j = last + 1; j < i; j++) grid[j] = grid[last] + (grid[i] - grid[last]) * (j - last) / (i - last);
        } else if (last < 0) {
          for (let j = 0; j < i; j++) grid[j] = grid[i];
        }
        last = i;
      }
    }
    if (last >= 0) for (let j = last + 1; j < span; j++) grid[j] = grid[last];
    // detrend (mean) + Hann over the covered span, zero-pad to N
    let mean = 0;
    for (let i = 0; i < span; i++) mean += grid[i];
    mean /= span;
    const re = new Float64Array(N), im = new Float64Array(N);
    for (let i = 0; i < span; i++) {
      const w = 0.5 - 0.5 * Math.cos(2 * Math.PI * i / (span - 1));
      re[i] = (grid[i] - mean) * w;
    }
    ejFFT(re, im);
    if (!st.spec) st.spec = new Float64Array(N / 2);
    for (let k = 0; k < N / 2; k++) {
      const p = re[k] * re[k];
      const q = im[k] * im[k];
      st.spec[k] += Math.sqrt(p + q);
    }
    st.specN++;
    // bin width: UI-grid sample spacing = ui*sampleS seconds, span samples used
    st.specDf = 1 / (N * fit.ui * sampleS);
    st.specSpan = span;
  }

  // --- eye fold: every sample at phase (t − t0) mod 2UI ---
  const W = st.eyeW, H = st.eyeH;
  const y0 = st.lo - 8, y1 = st.hi + 8;
  const yscale = (H - 1) / Math.max(1, y1 - y0);
  const fold = 2 * fit.ui;
  const iLo = Math.max(0, Math.ceil(fit.t0)), iHi = Math.min(n, Math.floor(fit.t0 + (nUI - 1) * fit.ui));
  for (let i = iLo; i < iHi; i++) {
    let ph = (i - fit.t0) % fold;
    if (ph < 0) ph += fold;
    const x = Math.min(W - 1, Math.floor(ph / fold * W));
    const y = Math.min(H - 1, Math.max(0, Math.round((sig[i] - y0) * yscale)));
    st.eye[y * W + x]++;
  }
  st.eyeY0 = y0; st.eyeY1 = y1;
  st.records++;
  return "locked:" + e.t.length;
}

// ---------- results ----------

function ejMedian(a) {
  if (!a.length) return 0;
  const s = [...a].sort((x, y) => x - y);
  return s[s.length >> 1];
}

// dual-Dirac-lite on the aggregated TIE. Splitting a UNIMODAL gaussian at its
// median always yields side-medians ±0.674σ apart (a fabricated DJ = 1.35σ), so
// bimodality is tested FIRST: for two separated Diracs the central band between
// the side modes is nearly empty, for a gaussian it holds ~26% of the samples.
// Unimodal → DJ = 0, RJ = global MAD·1.4826. Bimodal → DJ = mode separation,
// RJ = the per-mode spread (each side about its own mode).
function ejRjDj(tie) {
  if (tie.length < 200) return { rj: 0, dj: 0, ok: false };
  const s = [...tie].sort((a, b) => a - b);
  const med = s[s.length >> 1];
  const left = s.filter(v => v <= med), right = s.filter(v => v > med);
  const mL = ejMedian(left), mR = ejMedian(right);
  const sep = mR - mL;
  // central-band occupancy between the side modes — centred on the MIDPOINT of
  // the two modes (the median itself sits inside one cluster when bimodal)
  let central = 0;
  const mid = (mL + mR) / 2;
  const bandLo = mid - 0.25 * sep, bandHi = mid + 0.25 * sep;
  for (const v of s) if (v > bandLo && v < bandHi) central++;
  const centralFrac = central / s.length;
  if (!(sep > 0) || centralFrac > 0.08) {
    // unimodal: all spread is random jitter
    const mad = ejMedian(s.map(v => Math.abs(v - med)));
    return { rj: 1.4826 * mad, dj: 0, ok: true };
  }
  const madL = ejMedian(left.map(v => Math.abs(v - mL)));
  const madR = ejMedian(right.map(v => Math.abs(v - mR)));
  return { rj: 1.4826 * ((madL + madR) / 2), dj: sep, ok: true };
}

// ejResult reduces the state to displayable metrics. Cheap (no copies of eye).
function ejResult(st) {
  const out = {
    records: st.records, rejected: st.rejected, edges: st.edges,
    ui: st.ui, sampleS: st.sampleS,
    uiSeconds: st.ui * st.sampleS,
    bitRate: st.ui > 0 && st.sampleS > 0 ? 1 / (st.ui * st.sampleS) : 0,
    lastErr: st.lastErr,
  };
  // TIE stats
  if (st.tie.length >= 8) {
    let ss = 0, mn = Infinity, mx = -Infinity, mean = 0;
    for (const v of st.tie) mean += v;
    mean /= st.tie.length;
    for (const v of st.tie) {
      const d = v - mean;
      const p = d * d;
      ss += p;
      if (v < mn) mn = v;
      if (v > mx) mx = v;
    }
    out.tieRms = Math.sqrt(ss / st.tie.length);
    out.tiePp = mx - mn;
    const rd = ejRjDj(st.tie);
    out.rj = rd.rj; out.dj = rd.dj;
    out.periodJRms = rmsOf(st.periodJ);
    out.c2cJRms = rmsOf(st.c2cJ);
  }
  // spectrum (averaged magnitude, calibrated to sinusoid zero-peak amplitude)
  if (st.spec && st.specN > 0) {
    const N = st.fftN, span = st.specSpan || N;
    // Hann coherent gain over the covered span, single-sided ×2, /span
    const cal = 2 / (0.5 * span) / st.specN;
    const mags = new Float64Array(st.spec.length);
    for (let k = 0; k < st.spec.length; k++) mags[k] = st.spec[k] * cal;
    out.spectrum = mags;
    out.specDf = st.specDf;
    // dominant off-DC peak
    let pk = 0, pkV = 0;
    for (let k = 3; k < mags.length; k++) if (mags[k] > pkV) { pkV = mags[k]; pk = k; }
    out.specPeakHz = pk * st.specDf;
    out.specPeakAmp = pkV;
  }
  // eye metrics from the density map
  if (st.records > 0) out.eyeMetrics = ejEyeMetrics(st);
  return out;
}

function rmsOf(a) {
  if (!a.length) return 0;
  let ss = 0;
  for (const v of a) { const p = v * v; ss += p; }
  return Math.sqrt(ss / a.length);
}

// ejEyeMetrics: measured-at-density eye figures. Eye center = phase 0.75 of the
// 2UI fold (edges land at 0/0.5); rails from the center column's density
// clusters; height = inner gap (0.1% trimmed); width = crossing spread at the
// mid level around phase 0.5.
function ejEyeMetrics(st) {
  const W = st.eyeW, H = st.eyeH;
  const colAt = f => Math.min(W - 1, Math.floor(f * W));
  const codeOf = y => st.eyeY0 + y * (st.eyeY1 - st.eyeY0) / (H - 1);
  // center column ± a few: cluster into low/high rails about the code midpoint
  const c = colAt(0.75), span = 3;
  let total = 0;
  const col = new Float64Array(H);
  for (let x = c - span; x <= c + span; x++) {
    if (x < 0 || x >= W) continue;
    for (let y = 0; y < H; y++) { col[y] += st.eye[y * W + x]; total += st.eye[y * W + x]; }
  }
  if (total < 500) return null;
  const midY = (H - 1) / 2;
  // trim 0.1% from the inner side of each rail
  const trim = Math.max(1, total * 0.001);
  let acc = 0, topInner = -1, botInner = -1;
  for (let y = Math.floor(midY); y >= 0; y--) { acc += col[y]; if (acc >= trim) { botInner = y; break; } } // lower rail's inner edge (scanning down from mid)
  acc = 0;
  for (let y = Math.ceil(midY); y < H; y++) { acc += col[y]; if (acc >= trim) { topInner = y; break; } }
  // NOTE: y index grows with code here (row 0 = lowest code)
  let eyeHeight = 0;
  if (topInner >= 0 && botInner >= 0 && topInner > botInner) {
    eyeHeight = codeOf(topInner) - codeOf(botInner);
  }
  // eye width: at the mid code row band, find the crossing cluster spread around phase 0.5
  const yMid = Math.round(midY), band = 2;
  const row = new Float64Array(W);
  let rowTot = 0;
  for (let y = yMid - band; y <= yMid + band; y++) {
    if (y < 0 || y >= H) continue;
    for (let x = 0; x < W; x++) { row[x] += st.eye[y * W + x]; rowTot += st.eye[y * W + x]; }
  }
  let eyeWidthUI = 0;
  if (rowTot > 200) {
    // crossings cluster near phase 0 / 0.5 / 1.0; the eye is the empty span
    // between the cluster around 0.5±  — find the density-weighted cluster edges
    const xc = colAt(0.5);
    let l = xc, r = xc;
    const thr = rowTot / W * 0.5; // half of uniform density = cluster boundary
    while (l > 0 && row[l] < thr) l--;
    while (l > 0 && row[l] >= thr) l--;
    while (r < W - 1 && row[r] < thr) r++;
    while (r < W - 1 && row[r] >= thr) r++;
    // measure between the 0-cluster right edge and the 0.5-cluster left edge:
    // simpler honest figure: fraction of the mid row that is EMPTY between crossings
    let empty = 0;
    for (let x = 0; x < W; x++) if (row[x] < thr) empty++;
    eyeWidthUI = empty / W * 2; // fold spans 2 UI → empty fraction × 2 = width in UI
  }
  return { eyeHeightCodes: eyeHeight, eyeWidthUI: Math.min(1, eyeWidthUI / 2 * 2) / 1, crossTotal: total };
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { ejNew, ejFeed, ejResult, ejEdges, ejEstimateUI, ejFitGrid, ejFFT, ejEyeMetrics };
}
