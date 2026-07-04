// Node tests for superres.js: synthetic repetitive waveforms with KNOWN
// sub-sample shifts, noise, drift and glitches — the stacker must recover
// the shifts, reject the junk, drop the noise ~sqrt(N), and fill the fine
// grid without peak-locking. Run by superres_node_test.go.
"use strict";
const { srAlign, srGainOffset, srNew, srFeed, srResult, srModelFit, srClipped } = require("./superres.js");
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
  st.sampleS = 1e-8;
  for (let k = 0; k < N; k++) {
    const sig = frame(n, period, (rnd() - 0.5) * 4, noise, 60);
    srFeed(st, sig, { maxLag: 8 });
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
  const d1 = srFeed(st, flat, { maxLag: 8 });
  // A glitch: right period, but half the record is garbage.
  const glitch = frame(n, period, 0, 1.0, 60);
  for (let i = 0; i < n / 2; i++) glitch[i] = 128 + (rnd() - 0.5) * 120;
  const d2 = srFeed(st, glitch, { maxLag: 8 });
  // A clipped frame: rails pile up.
  const clip = frame(n, period, 0, 1.0, 140);
  for (let i = 0; i < n; i++) clip[i] = Math.max(4, Math.min(254, clip[i]));
  const d3 = srFeed(st, clip, { maxLag: 8 });
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
    srFeed(st, sig, { maxLag: 8 });
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
    const d = srFeed(st, sig, { maxLag: 8, edgeX });
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
    srFeed(st, sig, { maxLag: 8, edgeX, winHalf: 1024 });
  }
  const res = srResult(st);
  check("dead tail: frames still stack", res.frames >= N - 4, `${res.frames}/${N} (rej ${res.rejected})`);
  check("dead tail: bits gained > 0.8", res.bitsGained > 0.8, `+${res.bitsGained.toFixed(2)}`);
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
