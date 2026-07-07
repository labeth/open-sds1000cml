// Node tests for superres.js: synthetic repetitive waveforms with KNOWN
// sub-sample shifts, noise, drift and glitches — the stacker must recover
// the shifts, reject the junk, drop the noise ~sqrt(N), and fill the fine
// grid without peak-locking. Run by superres_node_test.go.
"use strict";
const { srAlign, srGainOffset, srNew, srSeedRef, srFeed, srResult, srModelFit, srClipped, srMeasure } = require("./superres.js");
const peaksLib = require("./peaks.js");

let fails = 0;
function check(name, ok, detail) {
  console.log((ok ? "ok   " : "FAIL ") + name + (detail ? "  [" + detail + "]" : ""));
  if (!ok) fails++;
}

// Deterministic PRNG (mulberry32) — reproducible tests.
function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const rnd = rng(0xC0FFEE);
// Box-Muller gaussian.
function gauss() {
  const u = Math.max(rnd(), 1e-12), v = rnd();
  return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v);
}

// Band-limited square: sum of odd harmonics up to fmax — models the analog
// front end rounding the edges (what makes sub-sample alignment possible).
function blSquare(n, period, phase, harmonics) {
  const out = new Float64Array(n);
  for (let i = 0; i < n; i++) {
    let v = 0;
    for (let h = 1; h <= harmonics; h += 2) v += Math.sin(2 * Math.PI * h * (i - phase) / period) / h;
    out[i] = v * (4 / Math.PI);
  }
  return out;
}
// Frame generator: codes = 128 + amp·wave + gaussian noise.
function frame(n, period, shift, noise, amp, harmonics) {
  const w = blSquare(n, period, shift, harmonics || 15);
  const out = new Float64Array(n);
  for (let i = 0; i < n; i++) out[i] = 128 + (amp || 60) * w[i] + noise * gauss();
  return out;
}

// ---- alignment accuracy: known sub-sample shifts recovered ----
{
  const n = 2048, period = 256;
  const ref = frame(n, period, 0, 0.8, 60);
  let maxErr = 0, worst = 0;
  for (let k = 0; k < 40; k++) {
    const trueShift = (rnd() - 0.5) * 6; // ±3 samples of delay
    const sig = frame(n, period, trueShift, 0.8, 60); // phase p delays by p
    const al = srAlign(ref, sig, 8);
    const err = Math.abs(al.shift - trueShift);
    if (err > maxErr) { maxErr = err; worst = trueShift; }
  }
  check("alignment error < 0.05 samples", maxErr < 0.05, `max ${maxErr.toFixed(4)} @ true ${worst.toFixed(3)}`);
}

// ---- peak-locking: fractional shifts stay uniform, no integer clustering ----
{
  const n = 2048, period = 256;
  const ref = frame(n, period, 0, 0.8, 60);
  const bins = new Array(8).fill(0);
  const N = 400;
  for (let k = 0; k < N; k++) {
    const trueShift = (rnd() - 0.5) * 4;
    const sig = frame(n, period, -trueShift, 0.8, 60);
    const al = srAlign(ref, sig, 8);
    const frac = ((al.shift % 1) + 1) % 1;
    bins[Math.min(7, Math.floor(frac * 8))]++;
  }
  const exp = N / 8;
  const chi2 = bins.reduce((s, o) => s + (o - exp) * (o - exp) / exp, 0);
  // 7 dof, p=0.001 cutoff ≈ 24.3 — generous but catches real peak-locking
  // (a locked estimator concentrates >2x mass at the 0-bin: chi2 hundreds).
  check("no peak-locking (chi2 < 24.3)", chi2 < 24.3, `chi2 ${chi2.toFixed(1)} bins ${bins.join(",")}`);
}

// ---- stacking: noise drops ~sqrt(N), fine grid fills ----
{
  const n = 1024, period = 128, K = 16, noise = 2.0, N = 256;
  const st = srNew(n, K);
  st.kernel = "drizzle"; // this block calibrates the deposit kernel's stats
  st.sampleS = 1e-8;
  for (let k = 0; k < N; k++) {
    const sig = frame(n, period, (rnd() - 0.5) * 4, noise, 60);
    srFeed(st, sig, null, { maxLag: 8 });
  }
  const res = srResult(st);
  check("all frames stacked", res.frames === N, `${res.frames}/${N} (rej ${res.rejected})`);
  check("fine grid fills (>60%)", res.fill > 0.6, `fill ${(res.fill * 100).toFixed(1)}%`);
  // Per-bin counts ~ N/K ≈ 16 → expect ~sqrt(16)=4x noise cut → 2 bits.
  const gain = res.sigmaSingle / res.sigmaStack;
  check("noise drops ~sqrt(count)", gain > 2.8 && gain < 5.6, `x${gain.toFixed(2)} (ideal 4)`);
  check("bits gained ~2", res.bitsGained > 1.5 && res.bitsGained < 2.5, res.bitsGained.toFixed(2));
  check("effective rate = K/sampleS", Math.abs(res.effRateSa - K / 1e-8) < 1, String(res.effRateSa));
}

// ---- lucky-frame rejection: glitches and flatlines don't pollute ----
{
  const n = 1024, period = 128, K = 8;
  const st = srNew(n, K);
  for (let k = 0; k < 30; k++) srFeed(st, frame(n, period, (rnd() - 0.5) * 4, 1.0, 60), { maxLag: 8 });
  const framesBefore = st.frames;
  // A flatline (missed trigger, no signal).
  const flat = new Float64Array(n).fill(128);
  const d1 = srFeed(st, flat, null, { maxLag: 8 });
  // A glitch: right period, but half the record is garbage.
  const glitch = frame(n, period, 0, 1.0, 60);
  for (let i = 0; i < n / 2; i++) glitch[i] = 128 + (rnd() - 0.5) * 120;
  const d2 = srFeed(st, glitch, null, { maxLag: 8 });
  // A clipped frame: rails pile up.
  const clip = frame(n, period, 0, 1.0, 140);
  for (let i = 0; i < n; i++) clip[i] = Math.max(4, Math.min(254, clip[i]));
  const d3 = srFeed(st, clip, null, { maxLag: 8 });
  check("flatline rejected", d1.startsWith("rejected"), d1);
  check("glitch rejected", d2.startsWith("rejected"), d2);
  check("clipped frame rejected", d3 === "rejected:clip", d3);
  check("stack count unchanged by junk", st.frames === framesBefore);
}

// ---- drift normalization: injected gain/offset drift is warped out ----
{
  const ref = frame(2048, 256, 0, 0.5, 60);
  const drifted = new Float64Array(2048);
  for (let i = 0; i < 2048; i++) drifted[i] = ref[i] * 1.07 + 3.5 + 0.5 * gauss();
  const { g, b } = srGainOffset(ref, drifted);
  check("gain drift recovered", Math.abs(g - 1.07) < 0.01, g.toFixed(4));
  check("offset drift recovered", Math.abs(b - 3.5) < 0.8, b.toFixed(2));
  // Degenerate fit guarded.
  const flat = new Float64Array(2048).fill(100);
  const gb2 = srGainOffset(flat, ref);
  check("degenerate fit falls back to identity", gb2.g === 1 && gb2.b === 0);
}

// ---- clip detector sanity ----
{
  const clean = frame(1024, 128, 0, 1.0, 55);
  check("clean frame not clipped", !srClipped(clean));
  const railed = new Float64Array(1024);
  for (let i = 0; i < 1024; i++) railed[i] = i % 128 < 64 ? 5 : 253;
  check("railed frame clipped", srClipped(railed));
}

// ---- model fit: single sine reconstructed within 1% ----
{
  const n = 1024, K = 8, period = 128, sampleS = 1e-8;
  const st = srNew(n, K);
  st.sampleS = sampleS;
  for (let k = 0; k < 200; k++) {
    const shift = (rnd() - 0.5) * 4;
    const sig = new Float64Array(n);
    for (let i = 0; i < n; i++) sig[i] = 128 + 50 * Math.sin(2 * Math.PI * (i - shift) / period) + 1.5 * gauss();
    srFeed(st, sig, null, { maxLag: 8 });
  }
  const res = srResult(st);
  const fit = srModelFit(res.mean, K, sampleS, peaksLib, 3);
  check("model fit returns", fit !== null && fit.freqs.length >= 1);
  if (fit) {
    const fTrue = 1 / (period * sampleS);
    const fBest = fit.freqs.reduce((a, b) => Math.abs(b - fTrue) < Math.abs(a - fTrue) ? b : a);
    check("fitted freq within 1%", Math.abs(fBest - fTrue) / fTrue < 0.01, `${fBest.toExponential(3)} vs ${fTrue.toExponential(3)}`);
    const amp = Math.hypot(fit.coeffs[1 + 2 * fit.freqs.indexOf(fBest)], fit.coeffs[2 + 2 * fit.freqs.indexOf(fBest)]);
    check("fitted amplitude within 3%", Math.abs(amp - 50) / 50 < 0.03, amp.toFixed(2));
    const dense = fit.synth(10000);
    check("dense synth produced", dense.length === 10000 && isFinite(dense[123]));
  }
}


// ---- deep-band case: trigger WANDERS through the record (raw deep records
// are not re-centered) and the signal spans many periods — NCC alone is
// ambiguous modulo the period; the trigger-edge coarse anchor must resolve
// it and stacking must still gain bits.
{
  const n = 4096, period = 400, K = 8, noise = 1.5, N = 120;
  const st = srNew(n, K);
  st.sampleS = 4e-7;
  let rejected = 0;
  const refWander = 0; // reference frame trigger position offset
  for (let k = 0; k < N; k++) {
    const wander = k === 0 ? refWander : Math.round((rnd() - 0.5) * 600); // ±300 samples
    const sub = (rnd() - 0.5) * 2; // sub-sample jitter on top
    const shift = wander + sub;
    const sig = frame(n, period, shift, noise, 60);
    // edge_x mimics the engine: the discerned edge index in THIS record.
    const edgeX = n / 2 + shift;
    const d = srFeed(st, sig, null, { maxLag: 8, edgeX });
    if (d.startsWith("rejected")) rejected++;
  }
  const res = srResult(st);
  check("deep wander: nearly all frames stack", res.frames >= N - 2, `${res.frames}/${N} (rej ${rejected})`);
  check("deep wander: bits gained > 1", res.bitsGained > 1, `+${res.bitsGained.toFixed(2)} bits (sigma ${res.sigmaSingle.toFixed(2)}->${res.sigmaStack.toFixed(3)})`);
  // Mis-stacking by one period would smear the edge: verify the stacked
  // waveform's transition is sharp by checking the mid-crossing count is the
  // expected ~n/period*K... simpler: sigmaStack must be far below the signal
  // swing (a period-slipped stack shows sigma comparable to the amplitude).
  check("deep wander: no period-slip smear", res.sigmaStack < 5, res.sigmaStack.toFixed(2));
}


// ---- dead-tail case: deep drains carry a flat dead region whose boundary
// moves per frame — windowed alignment (around the trigger edge) must keep
// accepting frames that a full-record NCC would fail.
{
  const n = 4096, period = 400, K = 8, noise = 1.5, N = 80;
  const st = srNew(n, K);
  st.sampleS = 4e-7;
  for (let k = 0; k < N; k++) {
    const wander = k === 0 ? 0 : Math.round((rnd() - 0.5) * 400);
    const sig = frame(n, period, wander + (rnd() - 0.5) * 2, noise, 60);
    // Dead tail: the last ~10-22% freezes at the last value (device profile:
    // trigger near record centre, dead drain at the END — outside the
    // ±winHalf alignment/stat window; σ honestly degrades if it intrudes).
    const boundary = Math.floor(n * (0.78 + 0.12 * rnd()));
    for (let i = boundary; i < n; i++) sig[i] = sig[boundary - 1];
    const edgeX = n / 2 + wander;
    srFeed(st, sig, null, { maxLag: 8, edgeX, winHalf: 1024 });
  }
  const res = srResult(st);
  check("dead tail: frames still stack", res.frames >= N - 4, `${res.frames}/${N} (rej ${res.rejected})`);
  check("dead tail: bits gained > 0.8", res.bitsGained > 0.8, `+${res.bitsGained.toFixed(2)}`);
}


// ---- minority-reference re-seed: a reference from an oddball drain must
// not permanently reject the dominant population — the stack re-seeds and
// converges on the majority.
{
  const n = 2048, period = 256, K = 8;
  const st = srNew(n, K);
  st.sampleS = 1e-8;
  // Frame 1: a DIFFERENT waveform shape (minority drain population).
  const odd = new Float64Array(n);
  for (let i = 0; i < n; i++) odd[i] = 128 + 50 * Math.sin(2 * Math.PI * i / 977) + 1.0 * gauss();
  srFeed(st, odd, null, { maxLag: 8 });
  check("minority ref adopted first", st.frames === 1);
  // Then 120 frames of the real (majority) signal.
  for (let k = 0; k < 120; k++) {
    srFeed(st, frame(n, period, (rnd() - 0.5) * 4, 1.0, 60), { maxLag: 8 });
  }
  const res = srResult(st);
  check("re-seed fired", res.reseeds >= 1, `reseeds ${res.reseeds}`);
  check("majority stacks after re-seed", res.frames > 60, `${res.frames} stacked, ${res.rejected} rej`);
  check("stack gains bits after re-seed", res.bitsGained > 1, `+${res.bitsGained.toFixed(2)}`);
}


// ---- quantization staircase + offset dither: slow (sub-LSB-slope) signal
// regions quantized to 8 bits "stick" to code levels when stacked plainly;
// sweeping a sub-LSB offset before quantization and subtracting it after
// (what the UI's dither mode does via the offset DAC) recovers the truth.
{
  const n = 2048, K = 8, N = 240, noise = 0.15; // noise well below 0.5 LSB
  const P = 1024, A = 40;
  const trueVal = i => 128 + A * Math.sin(2 * Math.PI * i / P);
  const slopeAt = i => A * 2 * Math.PI / P * Math.cos(2 * Math.PI * i / P);
  const mkStack = (dither) => {
    const st = srNew(n, K);
    st.sampleS = 1e-8;
    for (let k = 0; k < N; k++) {
      const shift = (rnd() - 0.5) * 4;
      const offCodes = dither ? -((k % 8) / 8) : 0; // commanded sub-LSB offset
      const sig = new Float64Array(n);
      for (let i = 0; i < n; i++) {
        const v = trueVal(i - shift) + noise * gauss();
        sig[i] = Math.round(v + offCodes) - offCodes; // quantize, then correct
      }
      srFeed(st, sig, null, { maxLag: 8, normalize: false });
    }
    return srResult(st);
  };
  // Error measured on SLOW bins only (|slope| < 0.05 codes/sample — the
  // staircase regime); rms not max (single-bin noise shouldn't decide).
  const errOf = (res) => {
    let s2 = 0, c = 0;
    for (let b = 64 * K; b < (n - 64) * K; b++) {
      const i = b / K;
      if (Math.abs(slopeAt(i)) > 0.05) continue;
      const m = res.mean[b];
      if (m < 0) continue;
      const e = m - trueVal(i);
      s2 += e * e; c++;
    }
    return Math.sqrt(s2 / Math.max(1, c));
  };
  const plain = mkStack(false), dithered = mkStack(true);
  check("staircase test stacks frames", plain.frames > 200 && dithered.frames > 200, `${plain.frames}/${dithered.frames}`);
  const e0 = errOf(plain), e1 = errOf(dithered);
  check("staircase visible without dither", e0 > 0.1, `rmsErr ${e0.toFixed(3)} codes`);
  check("dither removes the staircase", e1 < 0.06, `rmsErr ${e1.toFixed(3)} codes`);
  check("dither wins by >2x", e0 / e1 > 2, `${(e0 / e1).toFixed(1)}x`);
}

// ---- weighted drizzle smooths the independent-noise ribbon: with the SAME
// frames and sub-sample phases, the linear kernel's bin-to-bin roughness on
// a flat region beats nearest-bin (adjacent bins share samples).
{
  const n = 1024, K = 16, N = 200, noise = 2.0;
  const nb = n * K;
  const mk = () => ({ sum: new Float64Array(nb), cnt: new Float64Array(nb) });
  const lin = mk(), near = mk();
  for (let k = 0; k < N; k++) {
    const phase = rnd(); // sub-sample trigger phase (uniform, like hardware)
    for (let i = 0; i < n; i++) {
      const v = 150 + noise * gauss();
      const pos = (i - phase) * K;
      const bN = Math.round(pos);
      if (bN >= 0 && bN < nb) { near.sum[bN] += v; near.cnt[bN]++; }
      const b0 = Math.floor(pos), w1 = pos - b0, w0 = 1 - w1;
      if (b0 >= 0 && b0 < nb) { lin.sum[b0] += w0 * v; lin.cnt[b0] += w0; }
      if (b0 + 1 >= 0 && b0 + 1 < nb) { lin.sum[b0 + 1] += w1 * v; lin.cnt[b0 + 1] += w1; }
    }
  }
  const roughness = (acc) => {
    const diffs = [];
    let prev = null;
    for (let b = 100 * K; b < 900 * K; b++) {
      const m = acc.cnt[b] > 0.05 ? acc.sum[b] / acc.cnt[b] : null;
      if (m !== null && prev !== null) diffs.push(m - prev);
      prev = m;
    }
    const mu = diffs.reduce((s, v) => s + v, 0) / diffs.length;
    return Math.sqrt(diffs.reduce((s, v) => s + (v - mu) * (v - mu), 0) / diffs.length);
  };
  const rL = roughness(lin), rN = roughness(near);
  check("weighted drizzle smooths the ribbon vs nearest-bin", rL < 0.8 * rN, `linear ${rL.toFixed(4)} vs nearest ${rN.toFixed(4)}`);
}


// ---- iteration 4: interpolated resampling vs drizzle — every fine bin
// averages every frame, so noise drops ~sqrt(K); the guard is edge
// sharpness (10-90% rise in fine bins) which must not blur >10%.
{
  const n = 1024, period = 256, K = 16, noise = 2.0, N = 200;
  const mk = (kernel) => {
    const st = srNew(n, K);
    st.kernel = kernel;
    st.sampleS = 1e-8;
    for (let k = 0; k < N; k++) {
      srFeed(st, frame(n, period, (rnd() - 0.5) * 4, noise, 55), { maxLag: 8 });
    }
    return srResult(st);
  };
  const rise1090 = (mean) => {
    // find the largest rising edge in the middle half and measure 10-90%
    const nb = mean.length;
    let lo = 1e9, hi = -1e9;
    for (let b = nb >> 2; b < (nb * 3) >> 2; b++) { const v = mean[b]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
    const l10 = lo + 0.1 * (hi - lo), l90 = lo + 0.9 * (hi - lo);
    for (let b = nb >> 2; b < (nb * 3) >> 2; b++) {
      if (mean[b] < l10 && mean[b + 1] >= l10) {
        for (let e = b + 1; e < nb; e++) {
          if (mean[e] >= l90) return e - b;
          if (mean[e] < l10) break;
        }
      }
    }
    return -1;
  };
  const din = mk("drizzle"), inn = mk("interp");
  check("iter4: both kernels stack all frames", din.frames === N && inn.frames === N, `${din.frames}/${inn.frames}`);
  const gain = din.sigmaStack / inn.sigmaStack;
  check("iter4: interp cuts noise >2.5x vs drizzle", gain > 2.5, `x${gain.toFixed(2)} (sigma ${din.sigmaStack.toFixed(4)} -> ${inn.sigmaStack.toFixed(4)})`);
  const rD = rise1090(din.mean), rI = rise1090(inn.mean);
  check("iter4: rise found on both", rD > 0 && rI > 0, `${rD}/${rI} fine bins`);
  check("iter4: edge blur <= 20% (node bound; device guard 10%)", rI <= rD * 1.2, `rise ${rD} -> ${rI} fine bins`);
}


// ---- iteration 5: Catmull-Rom vs linear resampling — sharper passband at
// the same contributor count; noise within 10%, no spurious ringing.
{
  const n = 1024, period = 256, K = 16, noise = 2.0, N = 200;
  const mk = (kernel) => {
    const st = srNew(n, K);
    st.kernel = kernel;
    st.sampleS = 1e-8;
    for (let k = 0; k < N; k++) {
      srFeed(st, frame(n, period, (rnd() - 0.5) * 4, noise, 55, 31), { maxLag: 8 });
    }
    return srResult(st);
  };
  const rise1090 = (mean) => {
    const nb = mean.length;
    let lo = 1e9, hi = -1e9;
    for (let b = nb >> 2; b < (nb * 3) >> 2; b++) { const v = mean[b]; if (v < 0) continue; if (v < lo) lo = v; if (v > hi) hi = v; }
    const l10 = lo + 0.1 * (hi - lo), l90 = lo + 0.9 * (hi - lo);
    for (let b = nb >> 2; b < (nb * 3) >> 2; b++) {
      if (mean[b] < l10 && mean[b + 1] >= l10) {
        for (let e = b + 1; e < nb; e++) {
          if (mean[e] >= l90) return e - b;
          if (mean[e] < l10) break;
        }
      }
    }
    return -1;
  };
  const lin = mk("interp"), cub = mk("cubic");
  const rL = rise1090(lin.mean), rC = rise1090(cub.mean);
  check("iter5: cubic rise <= linear rise", rC > 0 && rC <= rL, `linear ${rL} -> cubic ${rC} fine bins`);
  check("iter5: cubic noise within 15% of linear", cub.sigmaStack < lin.sigmaStack * 1.15,
    `sigma ${lin.sigmaStack.toFixed(4)} -> ${cub.sigmaStack.toFixed(4)}`);
}


// ---- dual-channel stacking: C2 rides the align channel's shift and gets
// its own drift normalization; a clipped C2 frame is excluded from C2 only.
{
  const n = 1024, period = 128, K = 8, N = 120;
  const st = srNew(n, K);
  st.sampleS = 1e-8;
  for (let k = 0; k < N; k++) {
    const shift = k === 0 ? 0 : (rnd() - 0.5) * 4; // ref at phase 0 (the stack lives on ITS timeline)
    const c1 = frame(n, period, shift, 1.0, 60);
    const c2 = new Float64Array(n); // same timing, different shape+amplitude
    for (let i = 0; i < n; i++) c2[i] = 128 + 30 * Math.sin(2 * Math.PI * (i - shift) / period) + 1.0 * gauss();
    srFeed(st, c1, c2, { maxLag: 8 });
  }
  const res = srResult(st);
  check("dual: frames stack", res.frames === N, `${res.frames}`);
  check("dual: mean2 present", res.mean2 !== null && res.mean2.length === n * K);
  // C2 must be aligned by C1's shift: its stacked sine should be clean —
  // measure its rms vs the ideal.
  let s2 = 0, c2n = 0;
  for (let b = 100 * K; b < 900 * K; b++) {
    const m = res.mean2[b];
    if (m < 0) continue;
    const ideal = 128 + 30 * Math.sin(2 * Math.PI * (b / K) / period);
    s2 += (m - ideal) * (m - ideal); c2n++;
  }
  const rms2 = Math.sqrt(s2 / c2n);
  check("dual: C2 stacks coherently (rms < 0.3 codes)", rms2 < 0.3, `rms ${rms2.toFixed(3)}`);
  // A frame whose C2 clips is excluded from C2 only.
  const c1ok = frame(n, period, 0, 1.0, 60);
  const c2clip = new Float64Array(n);
  for (let i = 0; i < n; i++) c2clip[i] = i % 128 < 64 ? 5 : 253;
  const before = st.c[1].clipSkips;
  const d = srFeed(st, c1ok, c2clip, { maxLag: 8 });
  check("dual: C2-clip frame still stacks C1", d === "stacked", d);
  check("dual: C2-clip skips C2 data", st.c[1].clipSkips === before + 1, `${st.c[1].clipSkips}`);
}

// ---- srMeasure: the client-side measurement set matches ground truth on a
// stacked band-limited square.
{
  const n = 1024, period = 128, K = 8, N = 200;
  const st = srNew(n, K);
  st.sampleS = 1e-8; // 10 ns/sample → period 1.28 µs → 781.25 kHz
  for (let k = 0; k < N; k++) {
    srFeed(st, frame(n, period, (rnd() - 0.5) * 4, 1.5, 60), null, { maxLag: 8 });
  }
  const res = srResult(st);
  const m = srMeasure(res.mean, 1 / 32, 0, st.sampleS / K);
  check("meas: returns", m !== null && m.has_timing === true);
  const fTrue = 1 / (period * 1e-8);
  check("meas: freq within 0.5%", Math.abs(m.freq - fTrue) / fTrue < 0.005, `${m.freq.toFixed(0)} vs ${fTrue.toFixed(0)}`);
  check("meas: duty ~50%", Math.abs(m.duty - 50) < 3, m.duty.toFixed(1));
  // blSquare(4/pi sum to h15) fundamental-limited swing ≈ 2*amp*(4/pi)*sum-ish;
  // just sanity-band vpp: amp 60 → swing ≥ 100 codes → vpp ≥ 3.1 V at 1/32 V/code.
  check("meas: vpp sane", m.vpp > 3 && m.vpp < 5, m.vpp.toFixed(2) + " V");
  check("meas: vtop/vbase straddle mean", m.vtop > m.vmean && m.vbase < m.vmean,
    `${m.vbase.toFixed(2)}/${m.vmean.toFixed(2)}/${m.vtop.toFixed(2)}`);
  check("meas: rise measured", m.rise_s > 0 && m.rise_s < period * 1e-8, (m.rise_s * 1e9).toFixed(1) + " ns");
}

// ---- reference-lock v2: gated MULTI-HIT on a repetitive waveform ----
// The auto-gate narrows to ONE period, so every cycle in every frame is an
// independent occurrence — one frame yields many stacks (fast convergence), and
// the sub-sample offset of each hit fills the fine grid (super-resolution, not
// just averaging).
{
  const N = 2048, EDGE = 40, P = 40;         // triangle, 40-sample period
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  const tval = t => { const ph = ((t % P) + P) % P; return 128 + (ph < P / 2 ? -40 + 160 * ph / P : 120 - 160 * ph / P); };
  const tri = (frac, na) => {                 // triangle, sub-sample shiftable by `frac`
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) a[i] = clampC(tval(i - frac) + na * (rnd() - 0.5));
    return a;
  };
  const st = srNew(N, 16);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  check("v2/multihit: seed a repetitive reference", srSeedRef(st, tri(0, 0), tri(0, 0), EDGE) === true && st.gated === true);
  check("v2/multihit: auto-gate narrowed to ~1 period", Math.abs(st.gridL - P) <= 8, "gridL=" + st.gridL);
  const before = st.hits;
  const disp = srFeed(st, tri(0, 6), tri(0, 6), { edgeX: EDGE });
  check("v2/multihit: one frame -> many hits", disp.startsWith("stacked") && st.hits - before >= 10, "hits+=" + (st.hits - before) + " " + disp);
  for (let k = 0; k < 10; k++) srFeed(st, tri(0, 6), tri(0, 6), { edgeX: EDGE });
  const res = srResult(st, { stride: 1 });
  check("v2/multihit: bits gained > 0.5", res.bitsGained > 0.5, "+" + res.bitsGained.toFixed(2) + "b, hits=" + st.hits);
  // a sub-sample-shifted repeat must still align (parabolic offset), not reject
  check("v2/multihit: sub-sample-shifted frame stacks", srFeed(st, tri(0.5, 6), tri(0.5, 6), { edgeX: EDGE }).startsWith("stacked"));
}

// ---- reference-lock v2: GATED feature detection (UART-with-a-glitch) ----
// Freeze a specific feature → the gate isolates it. Frames CONTAINING it stack;
// frames without it (zero occurrences) are rejected. General: the feature is
// whatever you froze — a glitch, one UART byte, a runt pulse.
{
  const N = 1024, EDGE = 100;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  const withFeat = na => {                    // flat baseline + one distinctive bipolar pulse
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) { let v = 128; const j = i - 400; if (j >= 0 && j < 40) v = 128 + 70 * Math.sin(j * 2 * Math.PI / 40); a[i] = clampC(v + na * (rnd() - 0.5)); }
    return a;
  };
  const other = na => {                        // a DIFFERENT, non-matching waveform (slow ramp)
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) a[i] = clampC(90 + 70 * i / N + na * (rnd() - 0.5));
    return a;
  };
  const st = srNew(N, 16);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  check("v2/gate: seed a feature reference", srSeedRef(st, withFeat(0), withFeat(0), EDGE) === true);
  check("v2/gate: gate isolates the feature", st.gridL >= 16 && st.gridL <= 90, "gridL=" + st.gridL);
  check("v2/gate: frame WITH the feature stacks", srFeed(st, withFeat(6), withFeat(6), { edgeX: EDGE }).startsWith("stacked"));
  const rejBefore = st.rejected;
  const d2 = srFeed(st, other(6), other(6), { edgeX: EDGE });
  check("v2/gate: frame WITHOUT the feature rejected", d2.startsWith("rejected") && st.rejected === rejBefore + 1, d2);
}

// ---- reference-lock v2: the DRIZZLE kernel is preserved on the gated path ----
// interp is the default (smooth, lowest noise); drizzle deposits real samples at
// their sub-sample position — near-Nyquist fidelity at a higher noise floor (the
// 250 MHz path). Both must stack; drizzle must fill the fine grid (not all gaps).
{
  const N = 2048, EDGE = 40, P = 40;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  const tval = t => { const ph = ((t % P) + P) % P; return 128 + (ph < P / 2 ? -40 + 160 * ph / P : 120 - 160 * ph / P); };
  const tri = (frac, na) => { const a = new Int16Array(N); for (let i = 0; i < N; i++) a[i] = clampC(tval(i - frac) + na * (rnd() - 0.5)); return a; };
  const st = srNew(N, 16);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  st.kernel = "drizzle";
  check("v2/drizzle: seed", srSeedRef(st, tri(0, 0), tri(0, 0), EDGE) === true && st.kernel === "drizzle");
  for (let k = 0; k < 12; k++) srFeed(st, tri(k * 0.13, 6), tri(k * 0.13, 6), { edgeX: EDGE }); // varied sub-sample phase
  const res = srResult(st, { stride: 1 });
  const filled = res.mean.reduce((a, v) => a + (v >= 0 ? 1 : 0), 0);
  check("v2/drizzle: fine grid filled (not gappy staircase)", filled / res.mean.length > 0.6, (100 * filled / res.mean.length).toFixed(0) + "% filled");
  check("v2/drizzle: bits gained > 0.5", res.bitsGained > 0.5, "+" + res.bitsGained.toFixed(2) + "b, hits=" + st.hits);
}

// ---- reference-lock v2: SEGMENT + LEVEL consistency (breaker distillates) ----
// A hit must match the template everywhere it has information, not just on
// energy-weighted average. Distilled from the 50-family adversarial corpus:
// (a) a two-level DECOY whose differences sit in the template's flat plateaus
// (dead segments) is caught by the per-segment LEVEL check; (b) ambient
// lookalikes in the reference RAISE the acceptance floor (low-info templates).
{
  const N = 1024, L = 180;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  let s = 77;
  const rnd = () => { s = (s * 1103515245 + 12345) & 0x7fffffff; return s / 0x7fffffff - 0.5; };
  // square feature: low 30%, high 70% — one transition, wide plateaus
  const square = i => (i / L) < 0.3 ? -1 : 1;
  // decoy: same transition + an EXTRA pulse in the square's high plateau
  const dec = i => { const p = i / L; return (p < 0.3 ? -1 : 1) * (p > 0.6 && p < 0.75 ? -1 : 1); };
  const mkframe = (shape, na) => {
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) {
      let v = 128;
      const j = i - 300;
      if (j >= 0 && j < L) v = 128 + 55 * shape(j);
      a[i] = clampC(v + na * rnd());
    }
    return a;
  };
  const st = srNew(N, 16);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  check("v2/seg: seed square feature", srSeedRef(st, mkframe(square, 0), mkframe(square, 0), -1, { lo: 300, hi: 300 + L }) === true);
  check("v2/seg: genuine square stacks", srFeed(st, mkframe(square, 5), mkframe(square, 5), {}).startsWith("stacked"));
  const d = srFeed(st, mkframe(dec, 5), mkframe(dec, 5), {});
  check("v2/seg: plateau-pulse decoy REJECTED (level check)", d.startsWith("rejected"), d);
}
{
  // ambient-floor: a single smooth hump (low-information template) in an
  // environment whose filler already produces ~0.85 lookalikes. The self-
  // calibrated floor must sit above the ambient so junk humps don't stack.
  const N = 2048, L = 100;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  let s = 33;
  const rnd = () => { s = (s * 1103515245 + 12345) & 0x7fffffff; return s / 0x7fffffff - 0.5; };
  const hump = (c, w, a, out) => { for (let i = 0; i < N; i++) out[i] += a * Math.exp(-((i - c) ** 2) / (2 * w * w)); };
  const rect = (c, w, a, out) => { for (let i = Math.max(0, c - w); i < Math.min(N, c + w); i++) out[i] += a; };
  const mk = (humps, rects, na) => {
    const f = new Float64Array(N);
    for (const [c, w, a] of humps) hump(c, w, a, f);
    for (const [c, w, a] of rects) rect(c, w, a, f);
    const out = new Int16Array(N);
    for (let i = 0; i < N; i++) out[i] = clampC(128 + f[i] + na * rnd());
    return out;
  };
  // ref: the real (gauss) hump at 400 + ambient RECT pulses elsewhere — the
  // ~0.85-similarity lookalike class the self-calibrated floor must sit above.
  const ref = mk([[400, 12, 60]], [[900, 14, 40], [1500, 10, 45]], 0);
  const st = srNew(N, 16);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  check("v2/ambient: seed hump", srSeedRef(st, ref, ref, -1, { lo: 360, hi: 460 }) === true);
  check("v2/ambient: floor raised above ambient", st.adaptFloor > 0.82, "floor=" + st.adaptFloor.toFixed(2));
  // a frame with the REAL hump + fresh rect junk: only the real one may stack
  const f1 = mk([[700, 12, 60]], [[1200, 12, 42], [1800, 9, 47]], 4);
  const before = st.hits;
  const disp = srFeed(st, f1, f1, {});
  check("v2/ambient: real hump found once (junk pulses not stacked)",
    disp === "stacked:1" && st.hits === before + 1, disp + " hits+=" + (st.hits - before));
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
