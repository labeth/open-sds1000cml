// ENGMODEL-OWNER-UNIT: FU-WEB-PEAKS
// Shared FFT + peak-detection logic, used by ui.html (loaded as a <script>) and
// by peaks.test.cjs under node. Keep it pure (no DOM / no globals) so the exact
// code the browser runs is the code the test exercises.

// Iterative radix-2 FFT (in-place). re/im length must be a power of two.
// TRLC-LINKS: REQ-SDS-070
function fftInPlace(re, im) {
  const n = re.length;
  for (let i = 1, j = 0; i < n; i++) {
    let bit = n >> 1;
    for (; j & bit; bit >>= 1) j ^= bit;
    j ^= bit;
    if (i < j) { [re[i], re[j]] = [re[j], re[i]]; [im[i], im[j]] = [im[j], im[i]]; }
  }
  for (let len = 2; len <= n; len <<= 1) {
    const ang = -2 * Math.PI / len, wr = Math.cos(ang), wi = Math.sin(ang);
    for (let i = 0; i < n; i += len) {
      let cwr = 1, cwi = 0;
      for (let k = 0; k < len / 2; k++) {
        const ur = re[i + k], ui = im[i + k];
        const vr = re[i + k + len / 2] * cwr - im[i + k + len / 2] * cwi;
        const vi = re[i + k + len / 2] * cwi + im[i + k + len / 2] * cwr;
        re[i + k] = ur + vr; im[i + k] = ui + vi;
        re[i + k + len / 2] = ur - vr; im[i + k + len / 2] = ui - vi;
        const nwr = cwr * wr - cwi * wi; cwi = cwr * wi + cwi * wr; cwr = nwr;
      }
    }
  }
}

// spectrum: Hann-windowed magnitude spectrum of a real sample vector. nyq is
// the Nyquist frequency in Hz (caller computes it from the record's column
// count and duration). Returns null if there is too little data.
// TRLC-LINKS: REQ-SDS-070
function spectrum(samples, nyq) {
  let N = 1; while (N * 2 <= samples.length) N <<= 1;
  if (N < 16) return null;
  const re = new Float64Array(N), im = new Float64Array(N);
  let mean = 0; for (let i = 0; i < N; i++) mean += samples[i]; mean /= N;
  for (let i = 0; i < N; i++) {
    const win = 0.5 - 0.5 * Math.cos(2 * Math.PI * i / (N - 1)); // Hann
    re[i] = (samples[i] - mean) * win;
  }
  fftInPlace(re, im);
  const half = N / 2;
  const mags = new Float64Array(half);
  let peak = 1e-9;
  for (let k = 0; k < half; k++) { mags[k] = Math.hypot(re[k], im[k]); if (mags[k] > peak) peak = mags[k]; }
  return { mags, half, nyq: nyq || 0, peak };
}

// detectPeaks: local maxima above floorDb, parabola-interpolated for sub-bin
// frequency. Returns AT MOST maxPeaks entries — the strongest by magnitude —
// but SORTED BY FREQUENCY ascending so the displayed list order is stable
// frame-to-frame (magnitude ranking jitters with noise; frequency does not).
// TRLC-LINKS: REQ-SDS-070
function detectPeaks(spec, opts) {
  const floorDb = (opts && opts.floorDb != null) ? opts.floorDb : -50;
  const maxPeaks = (opts && opts.maxPeaks != null) ? opts.maxPeaks : 8;
  if (!spec) return [];
  const { mags, half, nyq, peak } = spec;
  // TRLC-LINKS: REQ-SDS-070
  const dbAt = k => 20 * Math.log10(mags[k] / peak + 1e-12);
  const cand = [];
  for (let k = 2; k < half - 2; k++) {
    if (mags[k] > mags[k-1] && mags[k] >= mags[k+1] &&
        mags[k] > mags[k-2] && mags[k] >= mags[k+2] && dbAt(k) > floorDb) {
      const a = Math.log(mags[k-1] + 1e-12), b = Math.log(mags[k] + 1e-12), c = Math.log(mags[k+1] + 1e-12);
      let d = 0.5 * (a - c) / (a - 2 * b + c || -1e-9); if (Math.abs(d) > 1) d = 0;
      cand.push({ k, frac: k + d, freq: nyq > 0 ? (k + d) / half * nyq : 0, db: dbAt(k) });
    }
  }
  cand.sort((p, q) => q.db - p.db);        // strongest first
  const top = cand.slice(0, maxPeaks);
  top.sort((p, q) => p.freq - q.freq);     // stable display order
  return top;
}

// nearestPeak: index of the peak whose frequency is closest to freq, or -1 if
// there is no selection (freq < 0) or no peaks. This is what makes a selection
// survive across frames — we re-locate the tracked peak by FREQUENCY every
// frame instead of trusting a list index that magnitude re-sorting would move.
// TRLC-LINKS: REQ-SDS-070
function nearestPeak(peaks, freq) {
  if (!peaks || !peaks.length || !(freq >= 0)) return -1;
  let best = 0, bd = Infinity;
  for (let i = 0; i < peaks.length; i++) {
    const d = Math.abs(peaks[i].freq - freq);
    if (d < bd) { bd = d; best = i; }
  }
  return best;
}

// fitTone: amplitude+phase of the single tone at cyclesPerLen cycles across
// the whole array (= freq * recordDuration), by projecting the mean-removed
// signal onto cos/sin. Values < 0 are gaps, skipped in the fit.
// TRLC-LINKS: REQ-SDS-070
function fitTone(samples, cyclesPerLen) {
  const M = samples.length;
  if (M < 2) return null;
  const w = 2 * Math.PI * cyclesPerLen / M;
  let mean = 0, cnt = 0;
  for (let i = 0; i < M; i++) { const v = samples[i]; if (v >= 0) { mean += v; cnt++; } }
  if (!cnt) return null; mean /= cnt;
  // LEAST-SQUARES fit of A·cos(wi)+B·sin(wi) over the valid samples. A naive
  // 2/N DFT coefficient assumes the basis is orthogonal over the window, which is
  // only true for an INTEGER number of cycles — for a non-integer cycle count (or
  // gaps) it overestimates the amplitude, so subtracting it to null a carrier
  // would OVERSHOOT. Solving the 2×2 normal equations is correct in all cases.
  // Evaluate cos(w·i)/sin(w·i) by incremental phasor rotation rather than two
  // trig calls per sample — the fit and synth loops each run over up to ~1.3M
  // samples on a superres stack. Re-anchor to exact trig every 1024 steps so
  // float drift stays ~1e-13 (negligible against 8-bit codes) and can't build up
  // across a long record.
  const cw = Math.cos(w), sw = Math.sin(w);
  // A single tone is massively over-determined: fitting A/B on ~64k samples is
  // statistically indistinguishable from fitting all ~1.3M, so stride the FIT
  // (the synth below stays full-res — its output is drawn/subtracted per sample).
  // The phasor then rotates by fstep·w each step (re-anchored the same way).
  const FIT_MAX = 65536;
  const fstep = M > FIT_MAX ? Math.floor(M / FIT_MAX) : 1;
  const cwF = fstep === 1 ? cw : Math.cos(fstep * w), swF = fstep === 1 ? sw : Math.sin(fstep * w);
  let scc = 0, sss = 0, scs = 0, dc = 0, ds = 0;
  { let c = 1, s = 0, k = 0;
    for (let i = 0; i < M; i += fstep) {
      if ((k++ & 1023) === 0) { const a = w * i; c = Math.cos(a); s = Math.sin(a); }
      const v = samples[i];
      if (v >= 0) { const d = v - mean; scc += c * c; sss += s * s; scs += c * s; dc += d * c; ds += d * s; }
      const nc = c * cwF - s * swF; s = s * cwF + c * swF; c = nc;
    }
  }
  const det = scc * sss - scs * scs;
  let A = 0, B = 0;
  if (Math.abs(det) > 1e-9) { A = (dc * sss - ds * scs) / det; B = (ds * scc - dc * scs) / det; }
  return { A, B, mean, w, cw, sw, M };
}

// toneAmplitude: the peak amplitude (code units) of the least-squares fitted
// tone at cyclesPerLen. A Hann bin's magnitude reads up to 1.42 dB low when the
// tone falls between bins (scalloping): the FFT list's "≈Vpk" said 1.70 V for
// a 1.98 V fundamental half a bin off (bug hunt 2026-10-10).
// TRLC-LINKS: REQ-SDS-070
function toneAmplitude(samples, cyclesPerLen) {
  const t = fitTone(samples, cyclesPerLen);
  return t ? Math.hypot(t.A, t.B) : 0;
}

// component: reconstruct the single-frequency contribution of `samples`
// (fitTone) as a same-length array in the SAME code units, so it can be drawn
// straight over the time trace — "show me just this frequency inside the
// waveform". Gaps (values < 0) are still produced in the output.
// TRLC-LINKS: REQ-SDS-070
function component(samples, cyclesPerLen) {
  const t = fitTone(samples, cyclesPerLen);
  if (!t) return null;
  const { A, B, mean, w, cw, sw, M } = t;
  const out = new Array(M);
  { let c = 1, s = 0;
    for (let i = 0; i < M; i++) {
      if ((i & 1023) === 0) { const a = w * i; c = Math.cos(a); s = Math.sin(a); }
      out[i] = mean + A * c + B * s;
      const nc = c * cw - s * sw; s = s * cw + c * sw; c = nc;
    }
  }
  return out;
}

// harmonics names each peak's harmonic number and sums the THD. The
// fundamental f0 is the largest peak. A peak is harmonic n (n = round(f/f0))
// when it lies within max(2 bins, 3 % of f0) of n·f0; when two peaks claim the
// same n the stronger keeps it. THD = √(Σ_{n≥2} A_n²) / A_1 over the harmonic
// peaks LISTED (A = linear magnitude from the peak's dB), so a longer list
// (more peaks) includes more harmonics; `count` says how many it summed. spec
// (optional) supplies the bin width. Returns {f0, topDb, harm[], thd, count}:
// harm[i] is 0 for a peak that is not a harmonic; thd is NaN without one.
// TRLC-LINKS: REQ-SDS-216
function harmonics(peaks, spec) {
  const out = { f0: 0, topDb: 0, harm: [], thd: NaN, count: 0 };
  if (!peaks || !peaks.length) return out;
  let top = 0;
  for (let i = 1; i < peaks.length; i++) if (peaks[i].db > peaks[top].db) top = i;
  const f0 = peaks[top].freq;
  out.f0 = f0; out.topDb = peaks[top].db;
  out.harm = peaks.map(() => 0);
  if (!(f0 > 0)) return out;
  const binHz = spec && spec.half > 0 && spec.nyq > 0 ? spec.nyq / spec.half : 0;
  const tol = Math.max(2 * binHz, 0.03 * f0);
  const best = new Map(); // n -> peak index
  for (let i = 0; i < peaks.length; i++) {
    const n = Math.round(peaks[i].freq / f0);
    if (n < 1 || Math.abs(peaks[i].freq - n * f0) > tol) continue;
    if (n === 1 && i !== top) continue; // a near neighbour of the fundamental is not H1
    const j = best.get(n);
    if (j === undefined || peaks[i].db > peaks[j].db) best.set(n, i);
  }
  let s2 = 0, count = 0;
  for (const [n, i] of best) {
    out.harm[i] = n;
    if (n >= 2) { const a = Math.pow(10, (peaks[i].db - out.topDb) / 20); s2 += a * a; count++; }
  }
  out.count = count;
  if (count) out.thd = Math.sqrt(s2);
  return out;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { fftInPlace, spectrum, detectPeaks, nearestPeak, component, fitTone, toneAmplitude, harmonics };
}
