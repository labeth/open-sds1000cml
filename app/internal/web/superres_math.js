// superres_math.js — leaf math/util for the stacker (mean/std, clip, crossings, align, gain-offset, srNew).

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
  const chan = () => ({
    sum: new Float64Array(nbins),
    sum2: new Float64Array(nbins),
    cnt: new Float64Array(nbins), // WEIGHT sums (linear drizzle splits samples)
    sumA: new Float64Array(nbins),
    cntA: new Float64Array(nbins),
    ref: null, // Float32Array reference for the drift fit
    vpc: 1 / 32, offV: 0,
    clipSkips: 0, // frames whose data was excluded because THIS channel clipped
  });
  return {
    n, K, nbins,
    kernel: "interp", // "interp" = resample every frame at every fine bin
    // (each bin averages ALL frames); "drizzle" = linear deposit (each bin
    // averages ~frames/K). See superres-lab.md iteration 4.
    c: [chan(), chan()], // both channels stack; c[align] drives align/lucky
    align: 0, // which channel alignment/lucky-selection runs on
    gated: false, hits: 0, // reference-lock v2: gate grid + occurrence count
    frames: 0, rejected: 0, clipped: 0, reseeds: 0,
    attempts: 0, // frames offered since the current reference was adopted
    scores: [], shifts: [],
    refEdgeX: -1,
    statLo: 0, statHi: n, // coarse-sample range the sigma stats cover (the
    // alignment window): dead-tail bins vary per frame by drain boundary and
    // would drown the real-signal noise figures.
    sampleS: 0, edgeX: -1,
  };
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { srMeanStd, srClipped, srCrossings, srAlign, srMidSwing, srGainOffset, srNew };
}
