// superres_measure.js — post-stack model fit + measurement (dual-mode).

// srModelFit least-squares-fits a sum of sinusoids at the top spectral peaks
// of the stacked waveform (frequencies from peaksLib.spectrum/detectPeaks),
// returning {freqs, synth(nOut)} for an arbitrarily dense reconstruction.
// Linear LSQ per frequency pair (sin,cos) + DC over the FILLED bins only.
if (typeof require !== "undefined") { Object.assign(globalThis, require("./superres_math.js"), require("./superres_template.js"), require("./superres_gate.js")); }

function srModelFit(mean, K, sampleS, peaksLib, nPeaks) {
  // Decimate a huge stack up front: the fit's frequencies and coefficients are
  // span-limited, so the K× fine-grid points don't change the result, and a
  // full 1.3M-element gap-fill + normal-equations pass froze for ~0.4 s. The
  // dense output (synth) is unaffected — it evaluates the fitted sinusoids at
  // any density. dt scales by the decimation so the time axis is unchanged.
  const FIT_MAX = 32768;
  let dec = 1;
  if (mean.length > FIT_MAX) {
    dec = Math.ceil(mean.length / FIT_MAX);
    const m2 = new Float32Array(Math.floor(mean.length / dec));
    for (let i = 0; i < m2.length; i++) m2[i] = mean[i * dec];
    mean = m2;
  }
  const dt = (sampleS / K) * dec;
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
  // Cap the FFT input: only the low-frequency peaks matter for the fit, and a
  // 1M-point FFT froze for ~130 ms. Decimating preserves peak frequencies
  // (span-limited resolution); the fit uses the full-resolution grid anyway.
  let fgrid = grid, fdt = dt;
  const fftDec = Math.max(1, Math.ceil(nb / 32768));
  if (fftDec > 1) {
    fgrid = new Float64Array(Math.floor(nb / fftDec));
    for (let i = 0; i < fgrid.length; i++) fgrid[i] = grid[i * fftDec];
    fdt = dt * fftDec;
  }
  const spec = peaksLib.spectrum(fgrid, 1 / (2 * fdt));
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
    const t = idx[r] * dt, y = vals[r];
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
      const dtOut = (mean.length * dt) / nOut;
      for (let i = 0; i < nOut; i++) {
        const t = i * dtOut;
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

// srMeasure computes the auto-measurement set over a stacked waveform —
// the same semantics as the device's measure.Compute (internal/measure) but
// over FLOAT codes, so the stack's sub-LSB precision carries through to the
// readouts. codes: Float32Array with -1 gaps; vpc volts/code; offV applied
// offset; dtS seconds per element. Returns the frame.m1-shaped object every
// existing consumer (meas panel, autoset) already reads, or null.
function srMeasure(codes, vpc, offV, dtS) {
  // Work on the contiguous filled run (gaps only at the ends in practice).
  let a = 0, b = codes.length - 1;
  while (a <= b && codes[a] < 0) a++;
  while (b >= a && codes[b] < 0) b--;
  const n = b - a + 1;
  if (n < 16) return null;
  let cmin = Infinity, cmax = -Infinity, sum = 0, sum2 = 0, cnt = 0;
  const hist = new Float64Array(256);
  for (let i = a; i <= b; i++) {
    const v = codes[i];
    if (v < 0) continue;
    if (v < cmin) cmin = v;
    if (v > cmax) cmax = v;
    sum += v; sum2 += v * v; cnt++;
    const h = Math.round(v);
    if (h >= 0 && h <= 255) hist[h]++;
  }
  if (!cnt) return null;
  const mean = sum / cnt;
  const variance = Math.max(0, sum2 / cnt - mean * mean);
  const toV = (code) => (code - 128) * vpc - offV;
  // Top/base via histogram modes either side of the midpoint (mirrors
  // measure.go — robust against overshoot ringing).
  const mid = Math.round((cmin + cmax) / 2);
  const mode = (lo, hi) => {
    let best = -1, bn = 0;
    for (let c = Math.max(0, lo); c <= Math.min(255, hi); c++) if (hist[c] > bn) { best = c; bn = hist[c]; }
    return best;
  };
  let topCode = mode(mid + 1, Math.ceil(cmax));
  let baseCode = mode(Math.floor(cmin), mid);
  if (topCode < 0) topCode = cmax;
  if (baseCode < 0) baseCode = cmin;
  const m = {
    vpp: (cmax - cmin) * vpc,
    vmax: toV(cmax), vmin: toV(cmin), vmean: toV(mean),
    vrms: Math.sqrt(variance) * vpc,
    vtop: toV(topCode), vbase: toV(baseCode),
    vampl: (topCode - baseCode) * vpc,
    overshoot: 0, preshoot: 0,
    freq: 0, period: 0, duty: 0, rise_s: 0, fall_s: 0,
    pos_width_s: 0, neg_width_s: 0, has_timing: false,
  };
  const amp = topCode - baseCode;
  if (amp > 0) {
    m.overshoot = (cmax - topCode) / amp * 100;
    m.preshoot = (baseCode - cmin) / amp * 100;
  }
  if (amp < 8 || !(dtS > 0)) return m;
  const run = codes.subarray ? codes.subarray(a, b + 1) : codes.slice(a, b + 1);
  const mid50 = baseCode + 0.5 * amp;
  const rise = srCrossings(run, mid50, true), fall = srCrossings(run, mid50, false);
  if (rise.length >= 2) {
    const period = (rise[rise.length - 1] - rise[0]) / (rise.length - 1) * dtS;
    if (period > 0) { m.period = period; m.freq = 1 / period; m.has_timing = true; }
  }
  // Mean pulse widths by two-pointer merge (ascending lists).
  const width = (from, to) => {
    let j = 0, s = 0, c = 0;
    for (const f of from) {
      while (j < to.length && to[j] <= f) j++;
      if (j === to.length) break;
      s += to[j] - f; c++;
    }
    return c ? s / c * dtS : 0;
  };
  m.pos_width_s = width(rise, fall);
  m.neg_width_s = width(fall, rise);
  if (m.has_timing && m.period > 0) m.duty = m.pos_width_s / m.period * 100;
  // 10–90% rise/fall over the first clean edge.
  const lo10 = baseCode + 0.1 * amp, hi90 = baseCode + 0.9 * amp;
  const edge = (rising) => {
    const first = rising ? lo10 : hi90, second = rising ? hi90 : lo10;
    for (let i = 1; i < run.length; i++) {
      const p = run[i - 1], q = run[i];
      const crossed = rising ? (p < first && q >= first) : (p > first && q <= first);
      if (!crossed) continue;
      const t1 = q === p ? i : (i - 1) + (first - p) / (q - p);
      for (let j = i; j < run.length; j++) {
        const c0 = run[j - 1], d0 = run[j];
        if (rising ? d0 < first : d0 > first) break;
        const crossed2 = rising ? (c0 < second && d0 >= second) : (c0 > second && d0 <= second);
        if (crossed2) {
          const t2 = d0 === c0 ? j : (j - 1) + (second - c0) / (d0 - c0);
          return (t2 - t1) * dtS;
        }
      }
    }
    return 0;
  };
  m.rise_s = edge(true);
  m.fall_s = edge(false);
  return m;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { srModelFit, srMeasure };
}
