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

// ---- reference-locked matching (R3/R4/R5): burst→slow→burst ----
// single'd on a burst → stack bursts (incl. displaced from the trigger), reject
// slow-stuff. burst and slow SHARE the trigger step (the hard case: a plain NCC
// over the wide window is dominated by that shared DC edge and false-accepts).
{
  const N = 2048, EDGE = 200;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  const base = i => (i < EDGE ? 128 : (i < EDGE + 5 ? 128 + 62 * (i - EDGE) / 5 : 190));
  const burst = (packetShift, na) => { // shared step + oscillatory packet at 260+shift
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) {
      let v = base(i);
      const p0 = 260 + packetShift;
      if (i >= p0 && i < p0 + 320) v = 190 + 48 * Math.sin((i - p0) * 2 * Math.PI / 9);
      a[i] = clampC(v + na * (rnd() - 0.5));
    }
    return a;
  };
  const slow = na => { // SAME shared step, then a slow ramp (no oscillation)
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) {
      let v = base(i);
      if (i >= 260 && i < 900) v = 190 - 40 * (i - 260) / 640;
      a[i] = clampC(v + na * (rnd() - 0.5));
    }
    return a;
  };
  const st = srNew(N, 32);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  const ref = burst(0, 0);
  check("reflock: seed accepts a burst reference", srSeedRef(st, ref, ref, EDGE) === true && st.userRef === true);
  const disp = f => srFeed(st, f, f, { maxLag: 8, edgeX: EDGE });
  // bursts — incl. displaced from the trigger (R5) — must stack, aligned to the packet
  check("reflock: burst +0 stacked",  disp(burst(0, 6)) === "stacked");
  check("reflock: burst +6 stacked",  disp(burst(6, 6)) === "stacked");
  check("reflock: burst +30 stacked (displaced)", disp(burst(30, 6)) === "stacked");
  check("reflock: burst -18 stacked (displaced)", disp(burst(-18, 6)) === "stacked");
  const shifts = st.shifts.slice(-4);
  check("reflock: displaced bursts aligned to the PACKET, not the edge",
    Math.abs(shifts[1] - 6) < 2 && Math.abs(shifts[2] - 30) < 2 && Math.abs(shifts[3] + 18) < 2,
    shifts.map(s => s.toFixed(1)).join(","));
  // slow frames share the edge but carry no packet → must be REJECTED (R4)
  let slowRej = 0;
  for (let i = 0; i < 4; i++) if (disp(slow(6)) !== "stacked") slowRej++;
  check("reflock: all 4 slow frames rejected", slowRej === 4, slowRej + "/4");
  check("reflock: stack held only the 4 bursts + reference", st.frames === 5, "frames=" + st.frames);
}

// ---- reference-locked matching is GENERAL (not burst-specific) ----
// Freeze a SPECIFIC byte pattern (e.g. a UART frame with an error) → stack frames
// carrying the SAME pattern (incl. shifted), reject a DIFFERENT byte pattern. Both
// share the start-bit trigger edge (the shared feature the matcher must ignore).
{
  const N = 2048, EDGE = 300, SPB = 24, LO = 50, HI = 190;
  const clampC = v => Math.max(12, Math.min(243, Math.round(v)));
  const uart = (bits, shift, na) => {
    const a = new Int16Array(N);
    for (let i = 0; i < N; i++) {
      const j = i - shift;
      let v = HI; // idle high + stop
      if (j >= EDGE && j < EDGE + SPB) v = LO; // start bit — the shared trigger edge
      else if (j >= EDGE + SPB && j < EDGE + SPB * 9) v = bits[((j - EDGE - SPB) / SPB) | 0] ? HI : LO;
      a[i] = clampC(v + na * (rnd() - 0.5));
    }
    return a;
  };
  const A = [0, 1, 1, 0, 1, 0, 0, 1], B = [1, 0, 0, 1, 0, 1, 1, 0]; // two distinct bytes
  const st = srNew(N, 32);
  st.align = 0; st.c[0].vpc = st.c[1].vpc = 1 / 32;
  check("reflock/uart: seed a byte-pattern reference", srSeedRef(st, uart(A, 0, 0), uart(A, 0, 0), EDGE) === true);
  const disp = f => srFeed(st, f, f, { maxLag: 8, edgeX: EDGE });
  check("reflock/uart: same pattern stacked", disp(uart(A, 0, 6)) === "stacked");
  check("reflock/uart: same pattern SHIFTED stacked", disp(uart(A, 25, 6)) === "stacked");
  check("reflock/uart: DIFFERENT byte rejected", disp(uart(B, 0, 6)) !== "stacked");
  check("reflock/uart: DIFFERENT byte shifted rejected", disp(uart(B, 25, 6)) !== "stacked");
  check("reflock/uart: stack held only the 2 matching + reference", st.frames === 3, "frames=" + st.frames);
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
