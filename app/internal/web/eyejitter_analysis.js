// ENGMODEL-OWNER-UNIT: FU-WEB-EYEJITTER-ANALYSIS
// eyejitter_analysis.js — edge/UI/jitter analysis + FFT helpers (dual-mode).

// ejEdges extracts all mid-level crossings with hysteresis (±15% of swing) and
// sub-sample linear interpolation. Returns {t:[...positions in samples], pol:[+1/-1]}
// or null when the record has no usable swing.
"use strict";

// TRLC-LINKS: REQ-SDS-199
function ejEdges(sig, n) {
  // Base/top levels by DUAL HISTOGRAM MODE (IEEE pulse-measurement style): the
  // two dominant population levels, regardless of duty. Percentile levels (the
  // first cut) systematically under-read the rare rail of a low-duty signal
  // (a sparse pulse train is low ~95% of the time), biasing the mid threshold
  // and fabricating ~±0.5-sample DCD between rising and falling edges.
  const hist = new Float64Array(256);
  const stride = Math.max(1, n >> 13);
  for (let i = 0; i < n; i += stride) hist[Math.max(0, Math.min(255, Math.round(sig[i])))]++;
  // light smoothing so single-code noise spikes don't win the mode
  const sm = new Float64Array(256);
  for (let c = 0; c < 256; c++) sm[c] = (hist[c - 1] || 0) + 2 * hist[c] + (hist[c + 1] || 0);
  let m1 = 0;
  for (let c = 1; c < 256; c++) if (sm[c] > sm[m1]) m1 = c;
  let m2 = -1;
  for (let c = 0; c < 256; c++) {
    if (Math.abs(c - m1) < 24) continue; // must be a DISTINCT level
    if (m2 < 0 || sm[c] > sm[m2]) m2 = c;
  }
  if (m2 < 0 || sm[m2] < 4) return null; // single-level record: no serial swing
  const lo = Math.min(m1, m2), hi = Math.max(m1, m2);
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

// ejEstimateUI: robust unit-interval estimate from consecutive-edge intervals.
// NRZ intervals are k·UI (k = run lengths); seed with a low percentile, then
// refine by dividing each interval by its nearest integer multiple. Returns
// UI in samples, or 0 when the intervals don't cluster on a common grid.
// TRLC-LINKS: REQ-SDS-199
function ejEstimateUI(t) {
  const d = [];
  for (let i = 1; i < t.length; i++) d.push(t[i] - t[i - 1]);
  return ejEstimateUIFromIntervals(d);
}

// ejEstimateUIFromIntervals: the estimator core over a bag of intervals (used
// per-record and over the cross-record pool for edge-starved signals).
// TRLC-LINKS: REQ-SDS-199
function ejEstimateUIFromIntervals(d) {
  if (d.length < 8) return 0;
  const s = Float64Array.from(d).sort(); // numeric without a comparator call
  let ui = s[Math.floor(s.length * 0.1)]; // near the shortest run
  if (!(ui > 1)) return 0;
  for (let pass = 0; pass < 3; pass++) {
    let num = 0, den = 0, bad = 0;
    for (const dd of d) {
      const k = Math.round(dd / ui);
      // k caps the accepted run length: sparse pulse trains and framed protocols
      // legitimately idle for tens of UI (a 16-UI cap silently rejected them);
      // beyond ~64 UI the k-assignment ambiguity dominates, so such intervals
      // are EXCLUDED from the average but are not evidence against the grid.
      if (k < 1) { bad++; continue; }
      if (k > 64) continue;
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
// TRLC-LINKS: REQ-SDS-199
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
// TRLC-LINKS: REQ-SDS-199
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

// TRLC-LINKS: REQ-SDS-199
function ejPushCapped(arr, v, cap) { if (arr.length < cap) arr.push(v); }

// TRLC-LINKS: REQ-SDS-199
function ejMedian(a) {
  if (!a.length) return 0;
  const s = Float64Array.from(a).sort(); // numeric without a comparator call
  return s[s.length >> 1];
}

// dual-Dirac-lite on the aggregated TIE. Splitting a UNIMODAL gaussian at its
// median always yields side-medians ±0.674σ apart (a fabricated DJ = 1.35σ), so
// bimodality is tested FIRST: for two separated Diracs the central band between
// the side modes is nearly empty, for a gaussian it holds ~26% of the samples.
// Unimodal → DJ = 0, RJ = global MAD·1.4826. Bimodal → DJ = mode separation,
// RJ = the per-mode spread (each side about its own mode).
// TRLC-LINKS: REQ-SDS-199
function ejRjDj(tie) {
  if (tie.length < 200) return { rj: 0, dj: 0, ok: false };
  const s = Float64Array.from(tie).sort(); // numeric without a comparator call
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

// TRLC-LINKS: REQ-SDS-199
function rmsOf(a) {
  if (!a.length) return 0;
  let ss = 0;
  for (const v of a) { const p = v * v; ss += p; }
  return Math.sqrt(ss / a.length);
}

// ejEyeMetrics: measured-at-density eye figures. Eye centre = phase 0.5 of the
// 2UI fold (the crossings land at 0.25/0.75); rails from the centre column's
// density about the mid level between the levels at the first lock; height =
// inner gap (0.1% trimmed); width = the contiguous gap at the mid level around
// the centre. box is the opening in the window: left/right in UI from its left
// edge (0..2) and lo/hi in codes.
// TRLC-LINKS: REQ-SDS-199, REQ-SDS-215
function ejEyeMetrics(st) {
  const W = st.eyeW, H = st.eyeH;
  // TRLC-LINKS: REQ-SDS-199
  const colAt = f => Math.min(W - 1, Math.floor(f * W));
  // TRLC-LINKS: REQ-SDS-199
  const codeOf = y => st.eyeY0 + y * (st.eyeY1 - st.eyeY0) / (H - 1);
  // the centre column ± a few: the rails above and below the mid level
  const c = colAt(0.5), span = 3;
  let total = 0;
  const col = new Float64Array(H);
  for (let x = c - span; x <= c + span; x++) {
    if (x < 0 || x >= W) continue;
    for (let y = 0; y < H; y++) { col[y] += st.eye[y * W + x]; total += st.eye[y * W + x]; }
  }
  if (total < 500) return null;
  const midY = Math.min(H - 1, Math.max(0, ((st.lo + st.hi) / 2 - st.eyeY0) * (H - 1) / Math.max(1, st.eyeY1 - st.eyeY0)));
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
  // eye width: at the mid code row band, the crossing clusters' gap around the centre
  const yMid = Math.round(midY), band = 2;
  const row = new Float64Array(W);
  let rowTot = 0;
  for (let y = yMid - band; y <= yMid + band; y++) {
    if (y < 0 || y >= H) continue;
    for (let x = 0; x < W; x++) { row[x] += st.eye[y * W + x]; rowTot += st.eye[y * W + x]; }
  }
  let eyeWidthUI = 0, left = NaN, right = NaN;
  if (rowTot > 200) {
    // The eye width is the LONGEST CONTIGUOUS mid-row gap containing the eye
    // centre (crossings cluster at phases 0.25 / 0.75 of the 2-UI fold; the
    // opening around 0.5 is one eye). Counting ALL empty columns across the
    // fold over-reads by including the half eyes at the window's edges.
    const thr = rowTot / W * 0.5; // half of uniform density = crossing cluster
    const xc = colAt(0.5);
    let l = xc, r = xc;
    if (row[xc] < thr) {
      while (l > 0 && row[l - 1] < thr) l--;
      while (r < W - 1 && row[r + 1] < thr) r++;
      eyeWidthUI = (r - l + 1) / W * 2; // fold spans 2 UI
      left = l / W * 2; right = (r + 1) / W * 2;
    }
  }
  const box = eyeHeight > 0 && eyeWidthUI > 0
    ? { left, right, lo: codeOf(botInner), hi: codeOf(topInner) } : null;
  return { eyeHeightCodes: eyeHeight, eyeWidthUI: Math.min(1, eyeWidthUI), crossTotal: total, midCode: codeOf(midY), box };
}

// ejEdgeTimes measures each edge's 10-90 % transition time around its mid
// crossing: from the edge's interpolated mid time, walk back to the near
// threshold (10 % for a rising edge, 90 % for a falling one) and forward to the
// far one, interpolating both crossings. The walk is bounded by maxSpan samples
// either side (half a UI), so a sluggish edge never reaches into the next bit.
// Returns {rise:[samples], fall:[samples]}.
// TRLC-LINKS: REQ-SDS-215
function ejEdgeTimes(sig, n, e, maxSpan) {
  const out = { rise: [], fall: [] };
  if (!e || !(e.hi > e.lo)) return out;
  const p10 = e.lo + 0.1 * (e.hi - e.lo), p90 = e.lo + 0.9 * (e.hi - e.lo);
  const span = Math.max(2, Math.floor(maxSpan || 8));
  // cross: the fractional index where sig crosses level between i and i+1
  // TRLC-LINKS: REQ-SDS-215
  const cross = (i, level) => { const a = sig[i], b = sig[i + 1]; return b === a ? i : i + (level - a) / (b - a); };
  for (let k = 0; k < e.t.length; k++) {
    const t = e.t[k], up = e.pol[k] > 0, c = Math.floor(t);
    const near = up ? p10 : p90, far = up ? p90 : p10;
    // back from the mid crossing to the near threshold
    let i = c, t0 = NaN;
    for (let s = 0; s < span && i >= 0 && i + 1 < n; s++, i--) {
      const a = sig[i], b = sig[i + 1];
      if (up ? (a <= near && b > near) : (a >= near && b < near)) { t0 = cross(i, near); break; }
    }
    // forward from the mid crossing to the far threshold
    let j = c, t1 = NaN;
    for (let s = 0; s < span && j + 1 < n; s++, j++) {
      const a = sig[j], b = sig[j + 1];
      if (up ? (a < far && b >= far) : (a > far && b <= far)) { t1 = cross(j, far); break; }
    }
    if (!(t1 > t0)) continue;
    (up ? out.rise : out.fall).push(t1 - t0);
  }
  return out;
}

// ejCrossing is the eye's crossing level as a fraction of the swing (0.5 = a
// symmetric eye). Rising and falling transitions are taken as straight lines
// through their mid-level times, which sit tieRise / tieFall from the ideal
// grid, with 10-90 % times rise / fall (all in samples): they intersect at
// 0.5 + 0.8 · (tieFall − tieRise) / (rise + fall). Late falling edges (wider
// ones) lift the crossing; NaN without both edge kinds.
// TRLC-LINKS: REQ-SDS-215
function ejCrossing(tieRise, tieFall, rise, fall) {
  if (![tieRise, tieFall, rise, fall].every(Number.isFinite) || !(rise + fall > 0)) return NaN;
  return Math.max(0, Math.min(1, 0.5 + 0.8 * (tieFall - tieRise) / (rise + fall)));
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { ejEdges, ejEstimateUI, ejEstimateUIFromIntervals, ejFitGrid, ejFFT, ejMedian, ejRjDj, rmsOf, ejEyeMetrics, ejPushCapped, ejEdgeTimes, ejCrossing };
}
