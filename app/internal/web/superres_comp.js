// superres_comp.js — analog-falloff compensation (DSP bandwidth enhancement)
// for the super-res stack. De-embeds the MEASURED channel magnitude response
// (scope front-end + interconnect) by reshaping the crunched spectrum toward a
// flat target, recovering resolution the analog rolloff cost. It runs on the
// STACK — whose extra ENOB (≈+2..4 bits) is exactly the SNR headroom the
// high-frequency boost spends, so amplified noise stays below the single-shot
// floor. A plain single frame has no headroom to give; the stack does.
//
// MEASURED on this bench by the square-wave HARMONIC-COMB method (a 50%-duty
// square has energy only at odd harmonics ∝ 1/m, so ONE capture samples the
// chain response self-normalised against the known 1/m envelope — no
// per-frequency V/div ambiguity). Two sources (2 & 5 MHz) overlaid to <1 dB.
// See app/docs/falloff-plan.md. Findings:
//   • chain is −3 dB at ~16 MHz, a 2-pole product: fca≈21 MHz (unterminated
//     jumper-wire + scope input C — the setup) × fcb≈92 MHz (≈ the scope's
//     rated 100 MHz front-end — the fit found it unaided);
//   • the Artix-7 LVCMOS33 output (~175 MHz, fast edges) contributes < 1 dB
//     below 82 MHz — a few-percent share, REPORTED but not removed (it is the
//     DUT's real signal; the target stays below its corner so we never
//     over-restore past what the FPGA actually produced).
// De-embedding recovers a −3 dB of ~61 MHz (3.7×) at fbw=70 MHz for ~+7.6 dB
// peak boost — well inside the stack's headroom.

"use strict";

// Measured chain magnitude response |H_chain(f)|, normalised to 1 at DC,
// sampled every SRCOMP_DF Hz (smoothed combination of the 2 & 5 MHz combs).
const SRCOMP_DF = 4e6;
const SRCOMP_HCAL = [
  1.0, 0.9292, 0.8725, 0.795, 0.7151, 0.6441, 0.5836, 0.5327,
  0.4892, 0.4525, 0.4212, 0.3959, 0.3778, 0.3644, 0.3493, 0.3265,
  0.2966, 0.2641, 0.2329, 0.2056, 0.1819, 0.1612, 0.1445, 0.1323,
];
const SRCOMP_MEAS_F3_HZ = 16.4e6;   // measured chain −3 dB (reported in the UI)
const SRCOMP_FPGA_FC_HZ = 175e6;    // FPGA-driver corner (spec best guess)
const SRCOMP_FCA = 20.8e6;          // 2-pole fit: dominant pole (interconnect + input C)
const SRCOMP_FCB = 92e6;            //             second pole (≈ scope 100 MHz front-end)

const SRCOMP_DEFAULT = { fbw: 70e6, order: 3, eps: 0.06, gmax: 6 };

// srCompCalH(f): measured chain response at |f|, linearly interpolated. Beyond
// the measured table, continue the fitted 2-pole tail (matched at the boundary)
// — a physical extrapolation for the auto target when the stack's headroom
// reaches past where the odd harmonics were still above the noise (~85 MHz).
function srCompCalH(f) {
  f = Math.abs(f);
  const df = SRCOMP_DF, tab = SRCOMP_HCAL, last = (tab.length - 1) * df;
  if (f <= 0) return 1;
  if (f >= last) {
    const tp = ff => 1 / Math.sqrt((1 + (ff / SRCOMP_FCA) ** 2) * (1 + (ff / SRCOMP_FCB) ** 2));
    const k = tab[tab.length - 1] / tp(last); // match the table at the boundary
    return Math.max(1e-4, k * tp(f));
  }
  const i = Math.floor(f / df), w = f / df - i;
  return tab[i] * (1 - w) + tab[i + 1] * w;
}

// srCompTargetH(f): flat-top target response, −3 dB at fbw, order-`order`
// super-Gaussian (flat through ~0.75·fbw then a clean roll that bounds the
// high-frequency noise gain).
function srCompTargetH(f, fbw, order) {
  return Math.exp(-0.6931471805599453 * Math.pow(Math.abs(f) / fbw, 2 * order));
}

// srCompGain(f, o): the real, zero-phase de-embed gain G(f). Wiener inverse of
// H_chain (eps regularises the division) reshaped to the target; capped at
// gmax. Zero-phase (real, even) — corrects magnitude only, so edges sharpen
// symmetrically with no added group delay (the front end is ~minimum-phase, so
// magnitude correction recovers most of the edge).
function srCompGain(f, o) {
  const hc = srCompCalH(f), ht = srCompTargetH(f, o.fbw, o.order);
  const g = (ht * hc) / (hc * hc + o.eps * o.eps);
  return g > o.gmax ? o.gmax : g;
}

// srCompInfo(o): filter figures independent of the data — peak boost (dB) and
// the recovered −3 dB of the compensated response (Hc·G). Cheap scan.
function srCompInfo(opts) {
  const o = Object.assign({}, SRCOMP_DEFAULT, opts || {});
  let peak = 0, f3 = 0, prevDb = 0, prevF = 0;
  for (let f = 0; f <= 260e6; f += 0.5e6) {
    const g = srCompGain(f, o);
    if (g > peak) peak = g;
    const respDb = 20 * Math.log10(Math.max(1e-9, srCompCalH(f) * g));
    if (f3 === 0 && f > 0 && respDb <= -3 && prevDb > -3) {
      f3 = prevF + (f - prevF) * (-3 - prevDb) / (respDb - prevDb); // linear interp
    }
    prevDb = respDb; prevF = f;
  }
  return { peakBoostDb: 20 * Math.log10(peak), recoveredF3: f3, fbw: o.fbw, measF3: SRCOMP_MEAS_F3_HZ };
}

// srCompAuto(bitsGained, rawNyqHz, spend): choose the compensation to spend the
// stack's MEASURED noise reduction as high-frequency boost. The boost G(f)
// amplifies the stack noise at f by G(f); the stack is quieter than one raw
// frame by 2^bitsGained, so the largest boost that keeps the compensated trace
// no noisier than a single acquisition is 2^bitsGained (= bitsGained·6.02 dB).
// We spend a fraction `spend` of that budget and pick the HIGHEST recovered
// bandwidth whose peak boost fits — so a longer stack (more bits) automatically
// recovers a higher −3 dB. Ceilings: the raw ADC Nyquist (hard — no real signal
// beyond) and a cal-trust cap; near those, super-res alignment jitter (not
// noise) is the real limit, so pushing further buys little.
function srCompAuto(bitsGained, rawNyqHz, spend) {
  const s = spend > 0 ? spend : 0.8;
  const budgetDb = Math.max(4, (bitsGained || 0) * 6.0206 * s);
  const budgetLin = Math.pow(10, budgetDb / 20);
  const eps = Math.min(0.12, 0.5 / budgetLin); // Wiener floor admits up to `budgetLin` boost
  const gmax = budgetLin * 1.25;               // hard cap just above the budget
  const order = 3;
  // Ceiling = 0.8·raw Nyquist (nothing real beyond Nyquist). NOTE: above the
  // measured cal range (~85 MHz) the falloff is 2-pole EXTRAPOLATION — a best
  // guess — and this bench's signal is already buried there, so recovering that
  // high boosts extrapolation + noise more than measured signal. The detrend
  // (srCompensate) is what keeps that from RINGING; the honest caveat stands.
  const ceil = Math.min(200e6, 0.8 * (rawNyqHz > 0 ? rawNyqHz : 250e6));
  const floor = 40e6;
  const peak = fbw => srCompInfo({ fbw, eps, gmax, order }).peakBoostDb;
  const mk = fbw => ({ fbw, eps, gmax, order, budgetDb, bitsGained: bitsGained || 0, auto: true });
  if (peak(floor) >= budgetDb) return mk(floor);
  if (peak(ceil) <= budgetDb) return mk(ceil);
  let lo = floor, hi = ceil;
  for (let i = 0; i < 26; i++) { const mid = (lo + hi) / 2; if (peak(mid) <= budgetDb) lo = mid; else hi = mid; }
  return mk(lo);
}

// ---- radix-2 iterative FFT (in place). n MUST be a power of two. inverse:
// conjugate-FFT-conjugate with 1/n scaling. re/im are Float64Array(n). ----
function srCompFFT(re, im, inverse) {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) { const tr = re[i]; re[i] = re[j]; re[j] = tr; const ti = im[i]; im[i] = im[j]; im[j] = ti; }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = (inverse ? 2 : -2) * Math.PI / len;
    const wr = Math.cos(ang), wi = Math.sin(ang);
    for (let i = 0; i < n; i += len) {
      let cr = 1, ci = 0;
      for (let k = 0; k < len / 2; k++) {
        const a = i + k, b = i + k + len / 2;
        const xr = re[b] * cr - im[b] * ci, xi = re[b] * ci + im[b] * cr;
        re[b] = re[a] - xr; im[b] = im[a] - xi;
        re[a] += xr; im[a] += xi;
        const ncr = cr * wr - ci * wi; ci = cr * wi + ci * wr; cr = ncr;
      }
    }
  }
  if (inverse) for (let i = 0; i < n; i++) { re[i] /= n; im[i] /= n; }
}

// srCompResample: circular linear resample src[0..M-1] → length Ndst over the
// SAME time span (so frequency bin k always maps to k/T, independent of N).
function srCompResample(src, M, Ndst) {
  const dst = new Float64Array(Ndst);
  const ratio = M / Ndst;
  for (let j = 0; j < Ndst; j++) {
    const t = j * ratio, i0 = Math.floor(t), w = t - i0;
    const a = src[i0 % M], b = src[(i0 + 1) % M];
    dst[j] = a * (1 - w) + b * w;
  }
  return dst;
}

// srCompensate(mean, dtFine, opts): de-embed a code-space fine-grid stack.
//   mean   : Float32Array (code space), −1 = unfilled (gap) sentinel.
//   dtFine : seconds per fine bin (sampleS / K).
// Returns { comp: Float32Array (same length; gaps preserved as −1, filled
//           samples floored at 0), peakBoostDb, recoveredF3, fbw, measF3 }.
function srCompensate(mean, dtFine, opts) {
  const o = Object.assign({}, SRCOMP_DEFAULT, opts || {});
  const M = mean.length;
  const info = srCompInfo(o);
  if (!(dtFine > 0) || M < 8) return Object.assign({ comp: mean }, info);

  // 1. Fill −1 gaps by circular linear interpolation into a work buffer.
  const x = new Float64Array(M);
  let anyFilled = false;
  for (let i = 0; i < M; i++) { if (mean[i] >= 0) { x[i] = mean[i]; anyFilled = true; } }
  if (!anyFilled) return Object.assign({ comp: mean }, info);
  // forward/back nearest-fill then linear blend across runs of gaps
  let last = -1;
  for (let i = 0; i < M; i++) {
    if (mean[i] >= 0) {
      if (last >= 0 && i - last > 1) {
        const a = x[last], b = x[i];
        for (let j = last + 1; j < i; j++) x[j] = a + (b - a) * (j - last) / (i - last);
      } else if (last < 0) {
        for (let j = 0; j < i; j++) x[j] = x[i]; // leading gap: hold first value
      }
      last = i;
    }
  }
  if (last >= 0 && last < M - 1) for (let j = last + 1; j < M; j++) x[j] = x[last]; // trailing gap

  // 1b. Endpoint detrend: subtract the line joining the first/last samples so
  // the record is boundary-matched. The FFT is circular, so a record that
  // starts and ends at different levels — a gated single EDGE is a STEP — has a
  // discontinuity at the wrap; the boost RINGS that step across the whole record
  // (worse than the signal itself). The trend is DC + lowest frequency (gain
  // ≈ 1), so removing it before the transform and adding it back unfiltered
  // after is faithful, and it leaves the wrap continuous (endpoints → 0).
  const trend0 = x[0], trendSlope = (x[M - 1] - x[0]) / Math.max(1, M - 1);
  for (let i = 0; i < M; i++) x[i] -= trend0 + trendSlope * i;

  // 2. Resample to nearest power of two (keeps the FFT radix-2, exact freq map).
  let N = 1; while (N < M) N <<= 1;
  if (N - M > M - (N >> 1) && (N >> 1) >= 8) N >>= 1; // round to nearest pow2
  const re = srCompResample(x, M, N), im = new Float64Array(N);

  // 3. FFT → apply the real, even, zero-phase gain per bin. Time span is T =
  //    M·dtFine, so bin k ↔ frequency k/T (k ≤ N/2; mirror for k > N/2). DC
  //    (k=0) is held at unity so the vertical offset is preserved exactly.
  srCompFFT(re, im, false);
  const T = M * dtFine;
  for (let k = 0; k <= (N >> 1); k++) {
    const g = k === 0 ? 1 : srCompGain(k / T, o);
    re[k] *= g; im[k] *= g;
    const kk = (N - k) % N;
    if (kk !== k) { re[kk] *= g; im[kk] *= g; }
  }
  srCompFFT(re, im, true); // inverse

  // 4. Resample back to M, floor filled samples at 0 (the < 0 gap sentinel),
  //    and restore the original gap pattern (never fabricate a stacked sample
  //    where nothing was stacked).
  const back = srCompResample(re, N, M);
  const comp = new Float32Array(M);
  for (let i = 0; i < M; i++) comp[i] = mean[i] < 0 ? -1 : Math.max(0, back[i] + trend0 + trendSlope * i);
  return Object.assign({ comp }, info);
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = {
    srCompensate, srCompGain, srCompCalH, srCompTargetH, srCompInfo, srCompAuto, srCompFFT, srCompResample,
    SRCOMP_HCAL, SRCOMP_DF, SRCOMP_DEFAULT, SRCOMP_MEAS_F3_HZ, SRCOMP_FPGA_FC_HZ,
  };
}
