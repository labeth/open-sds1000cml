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
// node: pull the analysis module onto globalThis so bare calls resolve
// (the browser loads it as a classic script before this one).
if (typeof require !== "undefined") { Object.assign(globalThis, require("./eyejitter_analysis.js")); }

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
    tie: [],                            // TIE sample buffer for histogram/RJ-DJ
    // (decimated when full — stays representative of the WHOLE run)
    tieCap: opts.tieCap || 60000,
    tieDecim: 1,                        // current decimation of the tie buffer
    tieSeen: 0,
    // exact running aggregates — never capped, never frozen (review finding:
    // a first-N window froze rms/pp after ~1 min while the panel looked live)
    tieSum: 0, tieSum2: 0, tieMin: Infinity, tieMax: -Infinity, tieN: 0,
    periodJ: [], c2cJ: [],              // period + cycle-cycle jitter samples (s)
    fftN: opts.fftN || 512,
    spec: null, specN: 0,               // magnitude-sum spectrum + record count
    specDf: 0,                          // Hz per spectrum bin
    dPool: [],                          // cross-record edge intervals: a HUGE-UI
    // record holds too few edges to estimate UI alone; the pool accumulates
    // intervals across records so the grid resolves after a few of them.
    lastErr: "",
  };
}

// ---------- edges ----------


// ---------- CDR ----------





// ---------- per-record feed ----------

// ejFeed analyzes one raw record. sig: Uint8/Int16Array codes, n valid samples,
// sampleS seconds/sample. Returns "locked:<edges>" | "rejected:<why>".
function ejFeed(st, sig, n, sampleS) {
  const e = ejEdges(sig, n);
  if (!e) { st.rejected++; st.lastErr = "no-swing"; return "rejected:no-swing"; }
  // pool intervals across records (cap: keep the freshest ~4000)
  for (let i = 1; i < e.t.length; i++) st.dPool.push(e.t[i] - e.t[i - 1]);
  if (st.dPool.length > 4000) st.dPool.splice(0, st.dPool.length - 4000);
  let ui0 = ejEstimateUI(e.t);
  if (!ui0 && st.dPool.length >= 8) {
    // thin record (few edges): estimate from the cross-record interval pool,
    // then still fit THIS record's edges against it.
    ui0 = ejEstimateUIFromIntervals(st.dPool);
  }
  if (!ui0) { st.rejected++; st.lastErr = "no-bit-grid"; return "rejected:no-bit-grid"; }
  // UI consistency across records: a drifting/false lock is not the same signal
  if (st.uiN > 0 && Math.abs(ui0 - st.ui) > 0.02 * st.ui) {
    st.rejected++; st.lastErr = "ui-inconsistent";
    return "rejected:ui-inconsistent";
  }
  const fit = ejFitGrid(e.t, ui0);
  if (!fit) { st.rejected++; st.lastErr = "no-lock"; return "rejected:no-lock"; }
  // Vertical scale is FROZEN at the first locked record: the density map's
  // deposits are binned against it and cannot be rebinned, so a level shift
  // (amplitude change, DC drift) must reject rather than corrupt eye metrics.
  if (st.uiN === 0) {
    // The vertical mapping is FROZEN at first lock with generous headroom (the
    // review's corruption came from a MUTATING mapping re-interpreting old
    // deposits). Later level wander/modulation lands at its true code position
    // in the fixed mapping — the rails honestly widen and the measured eye
    // shrinks, which is exactly what an eye diagram is for. Content beyond the
    // headroom clamps at the border bins, far from the center/mid metrics rows.
    st.lo = e.lo; st.hi = e.hi;
    const head = Math.max(8, 0.4 * (e.hi - e.lo));
    st.eyeY0 = Math.max(0, e.lo - head);
    st.eyeY1 = Math.min(255, e.hi + head);
  }
  st.ui = st.uiN === 0 ? fit.ui : st.ui + (fit.ui - st.ui) / Math.min(st.uiN + 1, 32);
  st.uiN++;
  st.sampleS = sampleS;

  // --- TIE aggregation (seconds): exact running stats + decimated buffer ---
  for (let i = 0; i < fit.tie.length; i++) {
    const v = fit.tie[i] * sampleS;
    st.tieSum += v;
    const p = v * v;
    st.tieSum2 += p;
    if (v < st.tieMin) st.tieMin = v;
    if (v > st.tieMax) st.tieMax = v;
    st.tieN++;
    if (st.tieSeen % st.tieDecim === 0) {
      st.tie.push(v);
      if (st.tie.length >= st.tieCap) { // halve: keep every other, double stride
        const half = [];
        for (let j = 0; j < st.tie.length; j += 2) half.push(st.tie[j]);
        st.tie = half;
        st.tieDecim *= 2;
      }
    }
    st.tieSeen++;
  }
  // period + cycle-cycle jitter over consecutive SAME-POLARITY edges (rising to
  // rising): mixing polarities lets duty-cycle distortion masquerade as period
  // jitter — a stable clock with DCD must read ~0 here (review finding).
  let prevIdx = -1, prevPeriod = null;
  for (let i = 0; i < e.t.length; i++) {
    if (e.pol[i] <= 0) continue;
    if (prevIdx >= 0) {
      const dk = fit.nIdx[i] - fit.nIdx[prevIdx];
      if (dk >= 1) {
        const err = (e.t[i] - e.t[prevIdx] - dk * fit.ui) * sampleS;
        ejPushCapped(st.periodJ, err, st.tieCap);
        if (prevPeriod !== null) ejPushCapped(st.c2cJ, err - prevPeriod, st.tieCap);
        prevPeriod = err;
      }
    }
    prevIdx = i;
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
    let last = -1, haveCnt = 0;
    for (let i = 0; i < span; i++) {
      if (have[i]) {
        haveCnt++;
        if (last >= 0 && i - last > 1) {
          for (let j = last + 1; j < i; j++) grid[j] = grid[last] + (grid[i] - grid[last]) * (j - last) / (i - last);
        } else if (last < 0) {
          for (let j = 0; j < i; j++) grid[j] = grid[i];
        }
        last = i;
      }
    }
    if (last >= 0) for (let j = last + 1; j < span; j++) grid[j] = grid[last];
    // Spectrum honesty gate: when most of the UI grid is INTERPOLATED (bursty
    // signals with long idle gaps), the resampling fabricates spectral structure
    // (measured: subharmonic images of the injected tone). Normal NRZ tops out
    // at ~50% slot coverage (a transition every other bit), bursty traffic sits
    // near ~30% — the gate lives between them. TIE stats always accumulate.
    if (haveCnt >= 0.35 * span) {
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
      // calibrate EACH record's contribution with its own Hann span (records
      // differ in edge coverage; a last-record-only calibration biased the
      // averaged tone amplitude — review finding)
      const cal1 = 2 / (0.5 * span);
      for (let k = 0; k < N / 2; k++) {
        const p = re[k] * re[k];
        const q = im[k] * im[k];
        st.spec[k] += Math.sqrt(p + q) * cal1;
      }
      st.specN++;
      // bin width: UI-grid sample spacing = ui*sampleS seconds
      st.specDf = 1 / (N * fit.ui * sampleS);
    }
  }

  st.lastSpanUI = nUI; // record span in UI (for the TIE high-pass corner report)

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
  st.records++;
  return "locked:" + e.t.length;
}

// ---------- results ----------




// ejResult reduces the state to displayable metrics. Cheap (no copies of eye).
function ejResult(st) {
  const out = {
    records: st.records, rejected: st.rejected, edges: st.edges,
    ui: st.ui, sampleS: st.sampleS,
    uiSeconds: st.ui * st.sampleS,
    bitRate: st.ui > 0 && st.sampleS > 0 ? 1 / (st.ui * st.sampleS) : 0,
    lastErr: st.lastErr,
    // The TIE reference clock is a PER-RECORD linear fit: jitter slower than
    // ~1/T_record is absorbed by the fit (the scope-world analogue of a golden
    // PLL's loop bandwidth). Report the corner so slow wander is never
    // mistaken for a clean source.
    tieHpHz: st.lastSpanUI > 0 && st.ui > 0 && st.sampleS > 0
      ? 1 / (st.lastSpanUI * st.ui * st.sampleS) : 0,
  };
  // TIE stats from the EXACT running aggregates (never frozen by the buffer cap)
  if (st.tieN >= 8) {
    const mean = st.tieSum / st.tieN;
    const varc = Math.max(0, st.tieSum2 / st.tieN - mean * mean);
    out.tieRms = Math.sqrt(varc);
    out.tiePp = st.tieMax - st.tieMin;
    const rd = ejRjDj(st.tie); // histogram-shape stats from the decimated buffer
    if (rd.ok) { out.rj = rd.rj; out.dj = rd.dj; } // undefined until measurable
    out.periodJRms = rmsOf(st.periodJ);
    out.c2cJRms = rmsOf(st.c2cJ);
    out.tieWindow = st.tieN; // how many edges the stats cover
  }
  // spectrum (per-record-calibrated magnitudes, averaged)
  if (st.spec && st.specN > 0) {
    const cal = 1 / st.specN;
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



if (typeof module !== "undefined" && module.exports) {
  module.exports = { ejNew, ejFeed, ejResult, ejEdges, ejEstimateUI, ejEstimateUIFromIntervals, ejFitGrid, ejFFT, ejEyeMetrics };
}
