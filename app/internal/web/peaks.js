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

// fftReduce is the LCD's transform of a record (framecalc.Spectrum, held to it
// by TestSpectrumJSParity): the largest power of two up to maxN points, each the
// mean of stride consecutive samples - the whole record, with a boxcar ahead of
// the decimation (taking every stride-th sample folded a square's harmonics
// back into the plot, and a stride sized from the display frame left most of a
// long record out) - Hann-windowed. nyq0 is the record's Nyquist. A decimated
// capture's filter ends the spectrum at bandHz: above it the bins hold the
// stopband (a capture filtered at 4.2 MHz is not drawn to 15.6 MHz). The
// result's nyq is the span shown, fs the transform's rate, points its length
// and peak its largest bin past DC, as the LCD's.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
function fftReduce(g, nyq0, bandHz, maxN) {
  let n = 1;
  while (n * 2 <= g.length && n < (maxN || 8192)) n <<= 1;
  if (n < 16) return null;
  const stride = Math.max(1, Math.floor(g.length / n));
  let x = g;
  if (stride > 1) {
    x = new Float64Array(n);
    for (let i = 0; i < n; i++) {
      let s = 0;
      for (let j = i * stride; j < (i + 1) * stride; j++) s += g[j];
      x[i] = s / stride;
    }
  }
  const nyq = nyq0 / stride, s = spectrum(x, nyq);
  if (!s) return null;
  s.points = 2 * s.half; s.fs = 2 * nyq; s.binHz = nyq / s.half;
  if (bandHz > 0 && bandHz < nyq) {
    const kb = Math.min(s.half, Math.max(4, Math.ceil(bandHz / s.binHz)));
    s.mags = s.mags.subarray(0, kb); s.half = kb; s.nyq = kb * s.binHz;
  }
  let pk = 1e-9;
  for (let k = 1; k < s.half; k++) if (s.mags[k] > pk) pk = s.mags[k];
  s.peak = pk;
  return s;
}

// SPG_ROW_POINTS is a spectrogram row's transform length, as the scope's
// (framecalc.RowFFT): 1024 bins, more than the waterfall's columns.
const SPG_ROW_POINTS = 2048;

// fftRows is a record as spectrogram rows, oldest first, as the scope's
// waterfall takes it (framecalc.SpectrumRows): the record in consecutive
// stretches, each transformed at SPG_ROW_POINTS points with the stride and
// span of the record's own spectrum (fftReduce), so a capture paints a row
// for each stretch of the time it spans. A record whose spectrum is shorter
// than two rows' gives that spectrum alone.
// TRLC-LINKS: REQ-SDS-068, REQ-SDS-070
function fftRows(g, nyq0, bandHz, maxN) {
  const full = fftReduce(g, nyq0, bandHz, maxN);
  if (!full) return [];
  const k = Math.floor(full.points / SPG_ROW_POINTS);
  if (k < 2) return [full];
  const stride = Math.max(1, Math.floor(g.length / full.points)), seg = SPG_ROW_POINTS * stride;
  const rows = [];
  for (let i = 0; i < k; i++) {
    const part = g.subarray ? g.subarray(i * seg, (i + 1) * seg) : g.slice(i * seg, (i + 1) * seg);
    const s = fftReduce(part, nyq0, bandHz, SPG_ROW_POINTS);
    if (s) rows.push(s);
  }
  return rows;
}

// hannGain is a Hann window's response to a tone d bins (|d| ≤ 0.5) off a bin,
// relative to a tone on the bin: sinc(d) / (1 − d²) (analysis.hannGain).
// TRLC-LINKS: REQ-SDS-216
function hannGain(d) {
  if (d === 0) return 1;
  return Math.sin(Math.PI * d) / (Math.PI * d * (1 - d * d));
}

// detectPeaks: local maxima above floorDb, parabola-interpolated for sub-bin
// frequency. Returns AT MOST maxPeaks entries — the strongest by magnitude —
// but SORTED BY FREQUENCY ascending so the displayed list order is stable
// frame-to-frame (magnitude ranking jitters with noise; frequency does not).
// Each peak has its bin level `db` and the tone's level `lvl`: the bin level
// corrected for the Hann window's scalloping at the refined frequency (a tone
// between bins reads up to 1.42 dB low in its bin) — analysis.Peaks' DB, so the
// web's and the LCD's peak tables show the same numbers.
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-216
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
      const db = dbAt(k);
      cand.push({ k, frac: k + d, freq: nyq > 0 ? (k + d) / half * nyq : 0, db, lvl: db - 20 * Math.log10(hannGain(Math.max(-0.5, Math.min(0.5, d)))) });
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
// tone falls between bins (scalloping): a 1.98 V fundamental half a bin off
// reads 1.70 V from the bin.
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

// HARM_MAX is the highest harmonic numbered and counted in the THD
// (analysis.MaxHarmonic).
const HARM_MAX = 10;

// peakLvl: a peak's tone level (detectPeaks' lvl), its bin level for a peak
// from elsewhere.
// TRLC-LINKS: REQ-SDS-216
function peakLvl(p) { return Number.isFinite(p.lvl) ? p.lvl : p.db; }

// harmonics numbers the peaks that sit on multiples of the fundamental and
// sums the THD, as analysis.Harmonics does for the LCD. The fundamental f0 is
// the strongest peak (by tone level). A peak is harmonic n (n = round(f/f0),
// 1..10) when it lies within max(1.5 bins, 1 % of n·f0) of n·f0; of several,
// the strongest. The bin width is recovered from the peaks (freq/k of the
// highest bin). THD = √(Σ_{n=2..10} 10^((L_n − L_1)/10)) over the harmonics
// LISTED (L = tone level), so a longer list may include more of them; `count`
// says how many it summed. Returns {f0, fund, topDb, harm[], thd, count}:
// fund is the fundamental's index, topDb the strongest tone level (Rel is
// relative to it), harm[i] 0 for a peak that is no harmonic, thd a fraction
// (NaN without a harmonic). `spec` is accepted for older callers and unused.
// TRLC-LINKS: REQ-SDS-216
function harmonics(peaks, spec) { // eslint-disable-line no-unused-vars
  const out = { f0: 0, fund: -1, topDb: 0, harm: [], thd: NaN, count: 0 };
  if (!peaks || !peaks.length) return out;
  out.harm = peaks.map(() => 0);
  let top = -1;
  for (let i = 0; i < peaks.length; i++) {
    const p = peaks[i], l = peakLvl(p);
    if (Number.isFinite(l) && p.freq > 0 && Number.isFinite(p.freq) && (top < 0 || l > peakLvl(peaks[top]))) top = i;
  }
  if (top < 0) return out;
  const f0 = peaks[top].freq;
  out.f0 = f0; out.topDb = peakLvl(peaks[top]);
  let binHz = 0, topBin = 0;
  for (const p of peaks) if (p.k > topBin && p.freq > 0 && Number.isFinite(p.freq)) { binHz = p.freq / p.k; topBin = p.k; }
  const best = new Array(HARM_MAX + 1).fill(-1);
  for (let i = 0; i < peaks.length; i++) {
    const p = peaks[i], l = peakLvl(p);
    if (!Number.isFinite(p.freq) || !Number.isFinite(l)) continue;
    const n = Math.round(p.freq / f0);
    if (!(n >= 1 && n <= HARM_MAX)) continue;
    if (Math.abs(p.freq - n * f0) > Math.max(1.5 * binHz, 0.01 * n * f0)) continue;
    if (best[n] < 0 || l > peakLvl(peaks[best[n]])) best[n] = i;
  }
  best.forEach((i, n) => { if (i >= 0) out.harm[i] = n; });
  out.fund = best[1];
  if (out.fund < 0) return out;
  const l1 = peakLvl(peaks[out.fund]);
  let sum = 0, count = 0;
  for (let n = 2; n <= HARM_MAX; n++) if (best[n] >= 0) { sum += Math.pow(10, (peakLvl(peaks[best[n]]) - l1) / 10); count++; }
  out.count = count;
  if (count) out.thd = Math.sqrt(sum);
  return out;
}

// PICK_BINS is how far a picked peak follows its tone from frame to frame:
// a drifting tone is followed, another peak is never taken for it (the
// scope's marker allows the same 2.5 bins). PICK_LOST_MS is how long a pick
// whose tone is gone stays, shown lost, before it is cleared: a tone that
// drops out briefly keeps its pick.
const PICK_BINS = 2.5, PICK_LOST_MS = 3000;

// trackPicks follows the picked frequencies sel onto this frame's peaks: a
// pick moves to the peak within PICK_BINS bins of it (two picks on one peak
// become one); with none that near it keeps its frequency while its tone has
// been gone under PICK_LOST_MS (lostSince: when each pick lost it), then is
// cleared. Returns the picks kept, the indices of the peaks they hold and the
// lost times to pass to the next frame.
// TRLC-LINKS: REQ-SDS-070
function trackPicks(sel, peaks, binHz, lostSince, now) {
  const kept = [], idxs = new Set(), lost = new Map();
  const reach = PICK_BINS * (binHz > 0 ? binHz : 0);
  for (const f of sel) {
    const i = nearestPeak(peaks, f);
    if (i >= 0 && Math.abs(peaks[i].freq - f) <= reach) {
      if (!idxs.has(i)) { idxs.add(i); kept.push(peaks[i].freq); }
      continue;
    }
    const since = lostSince && lostSince.has(f) ? lostSince.get(f) : now;
    if (now - since < PICK_LOST_MS) { kept.push(f); lost.set(f, since); }
  }
  return { sel: kept, idxs, lostSince: lost };
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { fftInPlace, spectrum, fftReduce, fftRows, SPG_ROW_POINTS, detectPeaks, nearestPeak, trackPicks, PICK_LOST_MS, component, fitTone, toneAmplitude, harmonics };
}
