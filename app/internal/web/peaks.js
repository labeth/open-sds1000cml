// Shared FFT + peak-detection logic, used by ui.html (loaded as a <script>) and
// by peaks.test.cjs under node. Keep it pure (no DOM / no globals) so the exact
// code the browser runs is the code the test exercises.

// Iterative radix-2 FFT (in-place). re/im length must be a power of two.
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
function detectPeaks(spec, opts) {
  const floorDb = (opts && opts.floorDb != null) ? opts.floorDb : -50;
  const maxPeaks = (opts && opts.maxPeaks != null) ? opts.maxPeaks : 8;
  if (!spec) return [];
  const { mags, half, nyq, peak } = spec;
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
function nearestPeak(peaks, freq) {
  if (!peaks || !peaks.length || !(freq >= 0)) return -1;
  let best = 0, bd = Infinity;
  for (let i = 0; i < peaks.length; i++) {
    const d = Math.abs(peaks[i].freq - freq);
    if (d < bd) { bd = d; best = i; }
  }
  return best;
}

// component: reconstruct the single-frequency contribution of `samples` at
// cyclesPerLen cycles across the whole array (= freq * recordDuration). Fits
// amplitude+phase of that one tone by projecting the mean-removed signal onto
// cos/sin (a single-bin, un-windowed DFT), then returns a same-length array in
// the SAME code units so it can be drawn straight over the time trace — "show me
// just this frequency inside the waveform". Values < 0 are treated as gaps:
// skipped in the fit, still produced in the output.
function component(samples, cyclesPerLen) {
  const M = samples.length;
  if (M < 2) return null;
  const w = 2 * Math.PI * cyclesPerLen / M;
  let mean = 0, cnt = 0;
  for (let i = 0; i < M; i++) { const v = samples[i]; if (v >= 0) { mean += v; cnt++; } }
  if (!cnt) return null; mean /= cnt;
  let ci = 0, si = 0;
  for (let i = 0; i < M; i++) { const v = samples[i]; if (v < 0) continue; const d = v - mean; ci += d * Math.cos(w * i); si += d * Math.sin(w * i); }
  ci = 2 * ci / cnt; si = 2 * si / cnt;
  const out = new Array(M);
  for (let i = 0; i < M; i++) out[i] = mean + ci * Math.cos(w * i) - si * Math.sin(w * i);
  return out;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { fftInPlace, spectrum, detectPeaks, nearestPeak, component };
}
