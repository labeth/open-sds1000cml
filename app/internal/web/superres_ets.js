// superres_ets.js — phase-coherent EQUIVALENT-TIME super-resolution for a
// FREE-RUN (untriggerable) periodic signal: a clock above the ~65 MHz trigger
// comparator and/or near the ADC Nyquist (few samples/cycle), where the normal
// trigger-edge + NCC alignment cannot lock.
//
// Each acquisition samples the periodic signal at a RANDOM phase. We measure
// each frame's fundamental phase by a single-bin DFT at the clock frequency —
// which uses ALL ~thousands of cycles in the record, so the phase is precise
// even at ~3 samples/cycle — then FOLD every raw sample onto a one-period fine
// grid at its phase (f·t + φ). Averaging the many samples that land in each
// phase bin reconstructs one period at high effective resolution and drops the
// noise ~√N (extra ENOB), exactly like a sampling scope's random-interleaved
// equivalent-time reconstruction. No trigger, no NCC.
//
// Pure functions over typed arrays (node-tested by superres_ets.test.cjs).
"use strict";

// srEtsDft1: single-bin DFT of x (dt spacing) at frequency f. Phase is the
// fundamental's phase at t=0 (n=0): x[n] ≈ (2·mag/N)·cos(2πf·n·dt − phase).
function srEtsDft1(x, dt, f) {
  let re = 0, im = 0;
  const w = 2 * Math.PI * f * dt;
  for (let n = 0; n < x.length; n++) {
    const p = w * n;
    re += x[n] * Math.cos(p);
    im -= x[n] * Math.sin(p);
  }
  return { re, im, mag: Math.hypot(re, im), phase: Math.atan2(im, re) };
}

// srEtsRefineFreq: coarse-to-fine search that maximizes |X(f)| around fGuess —
// the max-likelihood single-tone frequency estimate. f MUST be accurate to
// within ~1/(record length) or the fold smears across the record; the fine
// search + parabolic step gets there. Returns the refined frequency.
function srEtsRefineFreq(x, dt, fGuess, opts) {
  opts = opts || {};
  const span = opts.span || fGuess * 0.002; // ±0.2%
  const steps = opts.steps || 400;
  let f = fGuess, best = -1;
  for (let i = 0; i <= steps; i++) {
    const ff = fGuess - span + (2 * span * i) / steps;
    const m = srEtsDft1(x, dt, ff).mag;
    if (m > best) { best = m; f = ff; }
  }
  const d = (2 * span) / steps;
  const a = srEtsDft1(x, dt, f - d).mag, b = srEtsDft1(x, dt, f).mag, c = srEtsDft1(x, dt, f + d).mag;
  const den = a - 2 * b + c;
  if (den < 0) { const s = 0.5 * (a - c) / den; if (s > -1 && s < 1) f += s * d; }
  return f;
}

// srEtsNew allocates a fold state: `nbins` phase bins across ONE period, two
// channels (the align channel supplies the phase reference; both fold to it).
function srEtsNew(nbins, dt) {
  const chan = () => ({
    sum: new Float64Array(nbins), sum2: new Float64Array(nbins), cnt: new Float64Array(nbins),
    sumA: new Float64Array(nbins), cntA: new Float64Array(nbins), // odd half-stack for honest σ
    vpc: 1 / 32, offV: 0, present: false,
  });
  return {
    nbins, dt, f: 0, align: 0,
    c: [chan(), chan()],
    frames: 0, rejected: 0,
    fSum: 0, fN: 0, // running mean of the per-frame refined frequency
  };
}

// srEtsFeed folds one frame onto the grid. sigs = [c1, c2]. The align channel's
// fundamental phase anchors the fold; both channels deposit at that phase, so an
// unlocked companion channel smears honestly instead of corrupting the anchor.
// A frame whose align-channel tone is too weak (mag below `minMag`) is rejected.
function srEtsFeed(st, sig1, sig2, opts) {
  opts = opts || {};
  const sigs = [sig1, sig2];
  const a = sigs[st.align];
  if (!a || a.length < 8 || !(st.f > 0) || !(st.dt > 0)) { st.rejected++; return "rejected"; }
  const ref = srEtsDft1(a, st.dt, st.f);
  // SNR gate: the tone must stand well above the local noise floor, probed at
  // two OFF-tone frequencies (non-harmonic multipliers that dodge the aliased
  // harmonic comb). Scales correctly with record length — a signal's DFT
  // magnitude grows ∝ N, white noise's only ∝ √N, so a real tone always wins.
  const nz = 0.5 * (srEtsDft1(a, st.dt, st.f * 1.29).mag + srEtsDft1(a, st.dt, st.f * 0.71).mag);
  const snrGate = opts.snrGate != null ? opts.snrGate : 4;
  if (!(ref.mag > snrGate * nz)) { st.rejected++; return "rejected:weak"; }
  const off = ref.phase / (2 * Math.PI);
  const nb = st.nbins, f = st.f, dt = st.dt;
  const odd = (st.frames & 1) === 1;
  for (let ch = 0; ch < 2; ch++) {
    const s = sigs[ch];
    if (!s || s.length < a.length) continue;
    const C = st.c[ch]; C.present = true;
    const sum = C.sum, sum2 = C.sum2, cnt = C.cnt, sumA = C.sumA, cntA = C.cntA;
    for (let n = 0; n < s.length; n++) {
      let ph = (f * n * dt + off) % 1;
      if (ph < 0) ph += 1;
      // linear-weight deposit into the two adjacent phase bins (smoother than
      // nearest-bin; the grid is circular so bin nb−1 wraps to 0).
      const t = ph * nb, b0 = Math.floor(t), w1 = t - b0, w0 = 1 - w1;
      const i0 = b0 % nb, i1 = (b0 + 1) % nb, v = s[n];
      sum[i0] += w0 * v; sum2[i0] += w0 * v * v; cnt[i0] += w0;
      sum[i1] += w1 * v; sum2[i1] += w1 * v * v; cnt[i1] += w1;
      if (odd) { sumA[i0] += w0 * v; cntA[i0] += w0; sumA[i1] += w1 * v; cntA[i1] += w1; }
    }
  }
  st.frames++;
  return "folded";
}

// srEtsResult reduces to one reconstructed period per channel + honest stats.
// mean: Float32Array(nbins) in code space (−1 in any unfilled bin). effBits from
// the odd/even half-stack difference (measured, not σ/√N).
function srEtsResult(st, opts) {
  opts = opts || {};
  const nb = st.nbins, EPS = 0.5;
  const out = { nbins: nb, f: st.f, frames: st.frames, rejected: st.rejected, periodS: st.f > 0 ? 1 / st.f : 0 };
  const reduce = (C) => {
    if (!C.present) return null;
    const mean = new Float32Array(nb);
    let filled = 0, lo = 1e9, hi = -1e9;
    for (let b = 0; b < nb; b++) { const c = C.cnt[b]; if (c < EPS) { mean[b] = -1; continue; } const m = C.sum[b] / c; mean[b] = m; filled++; if (m < lo) lo = m; if (m > hi) hi = m; }
    // measured noise: single-sample σ (median per-bin variance) and stacked σ
    // (median |odd−even|/2 across bins) → bitsGained.
    const sig = [], half = [];
    for (let b = 0; b < nb; b++) {
      const c = C.cnt[b]; if (c < 4) continue;
      const m = C.sum[b] / c; sig.push(Math.sqrt(Math.max(0, C.sum2[b] / c - m * m)));
      const ca = C.cntA[b], cb = c - ca;
      if (ca >= 2 && cb >= 2) half.push(Math.abs((C.sumA[b] / ca) - (C.sum[b] - C.sumA[b]) / cb) / 2);
    }
    const med = (arr) => { if (!arr.length) return 0; const s = [...arr].sort((x, y) => x - y); return s[s.length >> 1]; };
    const sigmaSingle = med(sig), sigmaStack = 1.4826 * med(half);
    const bitsGained = sigmaSingle > 0 && sigmaStack > 0 ? Math.log2(sigmaSingle / sigmaStack) : 0;
    return { mean, fill: filled / nb, swing: hi - lo, sigmaSingle, sigmaStack, bitsGained, effBits: 8 + bitsGained };
  };
  out.c1 = reduce(st.c[0]);
  out.c2 = reduce(st.c[1]);
  const aln = st.c[st.align].present ? (st.align === 0 ? out.c1 : out.c2) : (out.c1 || out.c2);
  if (aln) { out.bitsGained = aln.bitsGained; out.effBits = aln.effBits; out.swing = aln.swing; out.fill = aln.fill; out.sigmaSingle = aln.sigmaSingle; out.sigmaStack = aln.sigmaStack; }
  return out;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { srEtsDft1, srEtsRefineFreq, srEtsNew, srEtsFeed, srEtsResult };
}
