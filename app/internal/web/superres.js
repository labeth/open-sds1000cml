"use strict";
// superres.js — stack-and-crunch super-resolution for a repetitive waveform.
//
// Many raw acquisitions of the same repeating signal are combined into one
// waveform with more vertical resolution and a finer time grid, the way
// astro stackers align+drizzle+stack frames:
//   1. ALIGN  — each frame is aligned to a reference with sub-sample
//               precision (normalized cross-correlation; for edgy signals the
//               sub-sample refinement averages interpolated mid-level
//               crossing offsets, which is unbiased where a parabolic peak
//               fit "peak-locks" toward integer lags).
//   2. LUCKY  — frames that correlate poorly (glitch, missed trigger) or
//               clip are rejected/down-weighted before they blur the stack.
//   3. DRIZZLE— because asynchronous trigger vs sample clocks land every
//               frame on a different sub-sample phase, samples are binned
//               onto a K× finer time grid.
//   4. STACK  — per-bin averaging drops noise ~sqrt(N) → extra effective
//               bits (needs >~0.5 LSB of real noise for dither; this front
//               end has it).
//   5. DRIFT  — per-frame gain/offset is fit against the reference and
//               normalized out so slow drift doesn't blur long captures.
//   6. MODEL  — (optional) a sum-of-sinusoids LSQ fit over the stack gives
//               an arbitrarily dense analytic reconstruction.
//
// Pure functions over typed arrays; classic script + CJS guard so node can
// unit-test the math (superres.test.cjs) exactly like peaks.js/decode.js.

// srMeanStd returns {mean, std} of arr (population std).
function srMeanStd(arr) {
  let s = 0, s2 = 0;
  const n = arr.length;
  for (let i = 0; i < n; i++) { s += arr[i]; s2 += arr[i] * arr[i]; }
  const mean = s / n;
  const v = Math.max(0, s2 / n - mean * mean);
  return { mean, std: Math.sqrt(v) };
}

// srClipped mirrors the device's rail calibration: piled samples within 2
// codes of a rail (low ~6, high ~252) mean the frame is clipping.
function srClipped(sig) {
  const n = sig.length;
  let lo = 255, hi = 0;
  for (let i = 0; i < n; i++) { const v = sig[i]; if (v < lo) lo = v; if (v > hi) hi = v; }
  if (lo > 6 && hi < 253) return false;
  let nlo = 0, nhi = 0;
  for (let i = 0; i < n; i++) { const v = sig[i]; if (v <= lo + 2) nlo++; if (v >= hi - 2) nhi++; }
  return (lo <= 6 && nlo * 200 > n) || (hi >= 253 && nhi * 200 > n);
}

// srCrossings returns interpolated sample indices where sig crosses `level`
// in the given direction (rising: below→at/above). Sub-sample via linear
// interpolation — the same estimator the engine's edge discern uses.
function srCrossings(sig, level, rising) {
  const out = [];
  for (let i = 1; i < sig.length; i++) {
    const a = sig[i - 1], b = sig[i];
    if (rising ? (a < level && b >= level) : (a >= level && b < level)) {
      out.push(b === a ? i : (i - 1) + (level - a) / (b - a));
    }
  }
  return out;
}

// srAlign estimates the shift of sig relative to ref (in samples, sub-sample
// precision) and a match score in [0,1]. Convention: shift is how far sig is
// DELAYED — sig[i] ≈ ref[i − shift] — which is exactly what srAccum expects
// (samples land at fine bin (i − shift)·K). maxLag bounds the search
// (trigger jitter is small — a handful of samples).
//   score: peak normalized cross-correlation over the central window.
//   shift: integer NCC argmax refined by (a) mean of per-edge mid-level
//          crossing offsets when both traces have enough edges (unbiased for
//          square-ish signals), else (b) parabolic interpolation of the NCC
//          peak (fine for smooth/band-limited signals).
function srAlign(ref, sig, maxLag, base, wLo, wHi) {
  base = base | 0;
  const n = Math.min(ref.length, sig.length);
  if (wLo == null) wLo = 0;
  if (wHi == null) wHi = n;
  // Correlate over [wLo,wHi) of the REFERENCE (callers pass a window around
  // the trigger edge — the region guaranteed to carry real signal; deep
  // drains have a dead tail whose position varies per frame and would
  // punish genuinely good frames if scored). `base` is the COARSE alignment
  // (trigger-edge difference from the frame headers): on decimated deep
  // bands the trigger lands anywhere in the drained record — far beyond any
  // affordable NCC search — and for periodic signals a bare NCC is
  // ambiguous modulo the period. The shared edge resolves both; NCC refines
  // the residual jitter.
  const lo = Math.max(wLo, maxLag, maxLag - base);
  const hi = Math.min(wHi, n, sig.length - base) - maxLag;
  const m = hi - lo;
  if (m < Math.max(32, 4 * maxLag)) return null;
  let rm = 0, sm = 0;
  for (let i = lo; i < hi; i++) { rm += ref[i]; sm += sig[i + base]; }
  rm /= m; sm /= m;
  let rss = 0;
  for (let i = lo; i < hi; i++) { const d = ref[i] - rm; rss += d * d; }
  const scores = new Float64Array(2 * maxLag + 1);
  let best = 0, bestK = 0;
  for (let k = -maxLag; k <= maxLag; k++) {
    let dot = 0, sss = 0;
    for (let i = lo; i < hi; i++) {
      const r = ref[i] - rm, s = sig[i + base + k] - sm;
      dot += r * s; sss += s * s;
    }
    const denom = Math.sqrt(rss * sss);
    const sc = denom > 0 ? dot / denom : 0;
    scores[k + maxLag] = sc;
    if (sc > best) { best = sc; bestK = k; }
  }
  const resK = bestK; // residual lag (indexes `scores`); bestK = total shift
  bestK += base;
  let shift = bestK;
  let method = "int";
  // (a) edge-crossing refinement about the integer alignment, over the same
  // window. The crossing level is the ROBUST mid-swing (p10/p90 midpoint of
  // the reference) — the plain mean sits near the baseline for narrow-duty
  // pulses, where baseline noise crossings would flood the refinement.
  const refWin = ref.subarray ? ref.subarray(lo, hi) : ref.slice(lo, hi);
  const mid = srMidSwing(refWin);
  const re = srCrossings(refWin, mid, true).concat(srCrossings(refWin, mid, false)).map(t => t + lo);
  if (re.length >= 4) {
    const sLo = Math.max(0, lo + base - 2 * maxLag), sHi = Math.min(sig.length, hi + base + 2 * maxLag);
    const sigWin = sig.subarray ? sig.subarray(sLo, sHi) : sig.slice(sLo, sHi);
    const se = srCrossings(sigWin, mid, true).concat(srCrossings(sigWin, mid, false)).map(t => t + sLo);
    const offs = [];
    for (const t of re) {
      // nearest sig crossing to t+bestK within ±1.5 samples
      let bd = 1.5, bo = null;
      for (const u of se) {
        const d = u - t - bestK;
        if (Math.abs(d) < bd) { bd = Math.abs(d); bo = d; }
      }
      if (bo !== null) offs.push(bo);
    }
    if (offs.length >= 4) {
      offs.sort((a, b) => a - b);
      // trimmed mean (drop 25% tails) resists stray/mispaired edges
      const q = Math.floor(offs.length / 4);
      let s = 0, c = 0;
      for (let i = q; i < offs.length - q; i++) { s += offs[i]; c++; }
      shift = bestK + s / c;
      method = "edges";
    }
  }
  if (method === "int") {
    // (b) parabolic refinement of the NCC peak (residual-lag indexed).
    const i = resK + maxLag;
    if (i > 0 && i < scores.length - 1) {
      const a = scores[i - 1], b = scores[i], c = scores[i + 1];
      const den = a - 2 * b + c;
      if (den < 0) {
        let d = 0.5 * (a - c) / den;
        if (d > -1 && d < 1) { shift = bestK + d; method = "parabola"; }
      }
    }
  }
  return { shift, score: best, method };
}

// srMidSwing returns the robust mid-swing level of a trace: the midpoint of
// the p10/p90 quantiles (sampled with a stride on long records). Unlike the
// mean, it stays mid-amplitude for any duty cycle.
function srMidSwing(sig) {
  const stride = Math.max(1, Math.floor(sig.length / 4096));
  const s = [];
  for (let i = 0; i < sig.length; i += stride) s.push(sig[i]);
  s.sort((a, b) => a - b);
  return (s[Math.floor(s.length * 0.1)] + s[Math.floor(s.length * 0.9)]) / 2;
}

// srGainOffset fits sig ≈ g·ref + b by least squares over the ALIGNED
// overlap — `lag` is the frame's integer shift (sig[i+lag] pairs with
// ref[i]). Fitting unaligned would make the slope the autocorrelation at the
// lag (g = cos(2π·lag/period) for a sine): a systematic amplitude shrink for
// short-period signals. Returns {g, b}; callers divide out g and subtract b.
function srGainOffset(ref, sig, lag, wLo, wHi) {
  lag = lag | 0;
  const lo = Math.max(wLo == null ? 0 : wLo, -lag);
  const hi = Math.min(wHi == null ? ref.length : wHi, ref.length, sig.length - lag);
  const n = hi - lo;
  if (n < 16) return { g: 1, b: 0 };
  let sr = 0, ss = 0, srr = 0, srs = 0;
  for (let i = lo; i < hi; i++) {
    const r = ref[i], s = sig[i + lag];
    sr += r; ss += s; srr += r * r; srs += r * s;
  }
  const den = n * srr - sr * sr;
  if (den <= 0) return { g: 1, b: 0 };
  const g = (n * srs - sr * ss) / den;
  if (!(g > 0.1 && g < 10)) return { g: 1, b: 0 }; // degenerate fit — don't warp
  return { g, b: (ss - g * sr) / n };
}

// srNew allocates a stack state: n input samples drizzled onto n*K fine bins.
// sumA/cntA hold the odd-numbered frames — the odd/even HALF-STACK split
// measures the stacked noise honestly (astro practice): rms((meanA−meanB)/2)
// includes quantization floor and correlated alignment error, which the
// naive σ/√cnt would silently assume away.
function srNew(n, K) {
  const nbins = n * K;
  return {
    n, K, nbins,
    sum: new Float64Array(nbins),
    sum2: new Float64Array(nbins),
    cnt: new Uint32Array(nbins),
    sumA: new Float64Array(nbins),
    cntA: new Uint32Array(nbins),
    frames: 0, rejected: 0, clipped: 0, reseeds: 0,
    attempts: 0, // frames offered since the current reference was adopted
    scores: [], shifts: [],
    ref: null, // Float32Array reference (first accepted frame with signal)
    refEdgeX: -1,
    statLo: 0, statHi: n, // coarse-sample range the sigma stats cover (the
    // alignment window): dead-tail bins vary per frame by drain boundary and
    // would drown the real-signal noise figures.
    sampleS: 0, vpc: 1 / 32, offV: 0, edgeX: -1,
  };
}

// srFeed runs the full per-frame pipeline: align → lucky-select → drift
// normalize → drizzle. sig is a raw uint8-code array. Returns a disposition
// string for the UI ("stacked" | "rejected:<why>").
function srFeed(st, sig, opts) {
  opts = opts || {};
  const maxLag = opts.maxLag || 8;
  if (sig.length < st.n) return "rejected:short";
  if (srClipped(sig)) { st.clipped++; st.rejected++; return "rejected:clip"; }
  // REFERENCE RE-SEED: if most frames refuse to align to the current
  // reference, the reference itself is the outlier (this hardware's deep
  // drains come in populations — a reference from the minority rejects the
  // majority forever). Drop the stack and re-adopt from the incoming flow;
  // within a couple of re-seeds the reference lands in the dominant
  // population and acceptance recovers.
  st.attempts++;
  if (st.ref && st.attempts >= 30 && st.frames / st.attempts < 0.3) {
    const keep = { sampleS: st.sampleS, vpc: st.vpc, offV: st.offV, reseeds: st.reseeds + 1 };
    const fresh = srNew(st.n, st.K);
    Object.assign(st, fresh, keep);
  }
  if (!st.ref) {
    // Reference quality gate: a flat/untriggered first frame would zero the
    // NCC denominator and poison the whole capture (everything after scores
    // 0 and is rejected). Wait for a frame with real signal.
    let lo = 255, hi = 0;
    for (let i = 0; i < st.n; i++) { const v = sig[i]; if (v < lo) lo = v; if (v > hi) hi = v; }
    if (hi - lo < 12) { st.rejected++; return "rejected:flat"; }
    st.ref = Float32Array.from(sig.subarray ? sig.subarray(0, st.n) : sig.slice(0, st.n));
    st.refEdgeX = opts.edgeX != null ? opts.edgeX : -1;
    st.edgeX = st.refEdgeX; // the stack lives on the reference's timeline
    // The reference frame itself stacks at shift 0.
    srAccum(st, st.ref, 0);
    st.frames++;
    st.scores.push(1); st.shifts.push(0);
    return "stacked";
  }
  // Coarse alignment from the trigger-edge headers: on decimated deep bands
  // the trigger wanders through the drained record far beyond the NCC search
  // window, and a periodic signal is NCC-ambiguous modulo its period — the
  // shared trigger edge disambiguates both. NCC then refines the residual.
  let base = 0;
  if (st.refEdgeX >= 0 && opts.edgeX != null && opts.edgeX >= 0) {
    base = Math.round(opts.edgeX - st.refEdgeX);
  }
  // Align + score over a window around the reference's trigger edge: that
  // region is guaranteed real signal, whereas a deep drain's dead tail sits
  // at a different position each frame and would fail genuinely good frames.
  // ±2048 raw samples: comfortably inside the coherent capture on this
  // hardware's deep drains (real content ≈ first ~55% of a 20480 drain; the
  // dead tail mixes at per-frame boundaries and must stay out of the score).
  const center = st.refEdgeX >= 0 ? Math.round(st.refEdgeX) : st.n >> 1;
  const half = Math.min(st.n >> 1, opts.winHalf || 2048);
  const wLo = Math.max(0, center - half), wHi = Math.min(st.n, center + half);
  st.statLo = wLo; st.statHi = wHi;
  const al = srAlign(st.ref, sig, maxLag, base, wLo, wHi);
  if (!al) { st.rejected++; return "rejected:align"; }
  // Adaptive lucky threshold: after warm-up, cut at median − 3·MAD — but
  // never closer than 0.05 below the median. When all frames are good the
  // score distribution is razor thin (MAD ≈ 0) and a bare 3·MAD cut would
  // reject the healthy tail; real junk (glitch/flatline) scores FAR lower.
  let thr = opts.minScore != null ? opts.minScore : 0.6;
  if (st.scores.length >= 10) {
    const s = [...st.scores].sort((a, b) => a - b);
    const med = s[s.length >> 1];
    const mad = s.map(x => Math.abs(x - med)).sort((a, b) => a - b)[s.length >> 1];
    thr = Math.max(thr, med - Math.max(3 * 1.4826 * mad, 0.05));
  }
  if (al.score < thr) { st.rejected++; return "rejected:score"; }
  let frame = sig;
  if (opts.normalize !== false) {
    const { g, b } = srGainOffset(st.ref, sig, Math.round(al.shift), wLo, wHi);
    if (g !== 1 || b !== 0) {
      const f = new Float32Array(st.n);
      for (let i = 0; i < st.n; i++) {
        const v = (sig[i] - b) / g;
        f[i] = v < 0 ? 0 : v; // codes can't go negative; -1 is the gap sentinel
      }
      frame = f;
    }
  }
  srAccum(st, frame, al.shift);
  st.frames++;
  st.scores.push(al.score);
  st.shifts.push(al.shift);
  return "stacked";
}

// srAccum drizzles one aligned frame onto the fine grid: sample i lands at
// fine bin round((i − shift)·K). Bins off the grid are dropped. Odd frames
// also land in the A half-stack (see srNew).
function srAccum(st, sig, shift) {
  const K = st.K, nb = st.nbins, n = st.n;
  const sum = st.sum, sum2 = st.sum2, cnt = st.cnt;
  const odd = (st.frames & 1) === 1;
  const sumA = st.sumA, cntA = st.cntA;
  for (let i = 0; i < n; i++) {
    const b = Math.round((i - shift) * K);
    if (b < 0 || b >= nb) continue;
    const v = sig[i];
    sum[b] += v; sum2[b] += v * v; cnt[b]++;
    if (odd) { sumA[b] += v; cntA[b]++; }
  }
}

// srResult reduces the accumulators to the stacked waveform + honest stats.
// mean: Float32Array of code-space values, -1 in unfilled bins (pen-up, the
// convention every renderer/exporter already understands).
//
// sigmaStack is MEASURED, not assumed: half the rms difference between the
// odd/even half-stack means. σ/√cnt (reported as sigmaStackTheory) is what
// independent Gaussian noise would give — the measured value also carries
// the quantization floor and correlated alignment error, so bitsGained
// cannot over-claim on a quiet (sub-LSB-noise) signal.
//
// opts.statsOnly skips the mean array; opts.stride subsamples the bins for
// the sigma medians (the live 500 ms stats tick uses both — a full reduction
// over 1.3M bins with two million-element sorts would jank the UI).
function srResult(st, opts) {
  opts = opts || {};
  const nb = st.nbins;
  const stride = Math.max(1, opts.stride | 0 || 1);
  const mean = opts.statsOnly ? null : new Float32Array(nb);
  const statLo = st.statLo * st.K, statHi = st.statHi * st.K;
  let filled = 0, scanned = 0;
  const sigSingles = [], halves = [];
  for (let b = 0; b < nb; b++) {
    const c = st.cnt[b];
    if (mean) mean[b] = c === 0 ? -1 : st.sum[b] / c;
    if (b % stride !== 0 || b < statLo || b >= statHi) continue;
    scanned++;
    if (c === 0) continue;
    filled++;
    const m = st.sum[b] / c;
    if (c >= 4) {
      const v = Math.max(0, st.sum2[b] / c - m * m);
      sigSingles.push(Math.sqrt(v));
      const ca = st.cntA[b], cb = c - ca;
      if (ca >= 2 && cb >= 2) {
        const ma = st.sumA[b] / ca, mb = (st.sum[b] - st.sumA[b]) / cb;
        halves.push((ma - mb) / 2);
      }
    }
  }
  const med = a => { if (!a.length) return 0; const s = [...a].sort((x, y) => x - y); return s[s.length >> 1]; };
  const sigmaSingle = med(sigSingles);
  let sigmaStack = 0;
  if (halves.length >= 16) {
    let s2 = 0;
    for (const d of halves) s2 += d * d;
    sigmaStack = Math.sqrt(s2 / halves.length);
  }
  const cnts = [];
  for (let b = statLo; b < statHi; b += stride) if (st.cnt[b] > 0) cnts.push(st.cnt[b]);
  const sigmaStackTheory = sigmaSingle > 0 && cnts.length ? sigmaSingle / Math.sqrt(med(cnts)) : 0;
  const bitsGained = sigmaStack > 0 && sigmaSingle > 0 ? Math.log2(sigmaSingle / sigmaStack) : 0;
  return {
    mean,
    fill: filled / Math.max(1, scanned),
    frames: st.frames, rejected: st.rejected, clipped: st.clipped, reseeds: st.reseeds,
    sigmaSingle, sigmaStack, sigmaStackTheory, bitsGained,
    effBits: 8 + bitsGained,
    fineDtS: st.sampleS > 0 ? st.sampleS / st.K : 0,
    effRateSa: st.sampleS > 0 ? st.K / st.sampleS : 0,
  };
}

// srModelFit least-squares-fits a sum of sinusoids at the top spectral peaks
// of the stacked waveform (frequencies from peaksLib.spectrum/detectPeaks),
// returning {freqs, synth(nOut)} for an arbitrarily dense reconstruction.
// Linear LSQ per frequency pair (sin,cos) + DC over the FILLED bins only.
function srModelFit(mean, K, sampleS, peaksLib, nPeaks) {
  const dt = sampleS / K;
  // The spectrum needs a UNIFORM grid: gap-fill unfilled bins by linear
  // interpolation between their filled neighbours (gaps are rare in a
  // well-filled stack; bail out below 50% fill).
  const nb = mean.length;
  const grid = new Float64Array(nb);
  const vals = [], idx = [];
  let last = -1, lastV = 0;
  for (let i = 0; i < nb; i++) {
    if (mean[i] >= 0) {
      if (last < i - 1) {
        const v0 = last >= 0 ? lastV : mean[i];
        for (let j = last + 1; j < i; j++) grid[j] = v0 + (mean[i] - v0) * (last >= 0 ? (j - last) / (i - last) : 0);
      }
      grid[i] = mean[i];
      vals.push(mean[i]); idx.push(i);
      last = i; lastV = mean[i];
    }
  }
  for (let j = last + 1; j < nb; j++) grid[j] = lastV; // trailing gap: hold
  if (vals.length < 64 || vals.length < nb / 2) return null;
  const spec = peaksLib.spectrum(grid, 1 / (2 * dt));
  if (!spec) return null;
  const peaks = peaksLib.detectPeaks(spec, { floorDb: -60, maxPeaks: nPeaks || 6 });
  if (!peaks.length) return null;
  const freqs = peaks.map(p => p.freq);
  // Accumulate the normal equations (AᵀA)x = Aᵀy directly — never
  // materialize the design matrix (up to ~1.3M rows on a deep stack). Large
  // stacks are strided down to ~200k rows; statistically equivalent.
  const cols = 1 + 2 * freqs.length;
  const stride = Math.max(1, Math.floor(idx.length / 200000));
  const ata = Array.from({ length: cols }, () => new Float64Array(cols));
  const aty = new Float64Array(cols);
  const row = new Float64Array(cols);
  row[0] = 1;
  for (let r = 0; r < idx.length; r += stride) {
    const t = idx[r] * (sampleS / K), y = vals[r];
    for (let f = 0; f < freqs.length; f++) {
      const w = 2 * Math.PI * freqs[f] * t;
      row[1 + 2 * f] = Math.sin(w);
      row[2 + 2 * f] = Math.cos(w);
    }
    for (let i = 0; i < cols; i++) {
      aty[i] += row[i] * y;
      for (let j = i; j < cols; j++) ata[i][j] += row[i] * row[j];
    }
  }
  for (let i = 0; i < cols; i++) for (let j = 0; j < i; j++) ata[i][j] = ata[j][i];
  for (let i = 0; i < cols; i++) { // elimination with partial pivot
    let p = i;
    for (let r = i + 1; r < cols; r++) if (Math.abs(ata[r][i]) > Math.abs(ata[p][i])) p = r;
    if (Math.abs(ata[p][i]) < 1e-12) return null;
    [ata[i], ata[p]] = [ata[p], ata[i]];
    [aty[i], aty[p]] = [aty[p], aty[i]];
    for (let r = i + 1; r < cols; r++) {
      const f = ata[r][i] / ata[i][i];
      for (let c = i; c < cols; c++) ata[r][c] -= f * ata[i][c];
      aty[r] -= f * aty[i];
    }
  }
  const x = new Float64Array(cols);
  for (let i = cols - 1; i >= 0; i--) {
    let s = aty[i];
    for (let c = i + 1; c < cols; c++) s -= ata[i][c] * x[c];
    x[i] = s / ata[i][i];
  }
  return {
    freqs,
    coeffs: x,
    synth(nOut) {
      const out = new Float32Array(nOut);
      const dt = (mean.length * (sampleS / K)) / nOut;
      for (let i = 0; i < nOut; i++) {
        const t = i * dt;
        let v = x[0];
        for (let f = 0; f < freqs.length; f++) {
          const w = 2 * Math.PI * freqs[f] * t;
          v += x[1 + 2 * f] * Math.sin(w) + x[2 + 2 * f] * Math.cos(w);
        }
        out[i] = v;
      }
      return out;
    },
  };
}

if (typeof module !== "undefined") {
  module.exports = { srAlign, srCrossings, srGainOffset, srNew, srFeed, srAccum, srResult, srModelFit, srClipped, srMeanStd };
}
