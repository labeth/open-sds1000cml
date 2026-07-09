// Node tests for superres_ets.js: synthetic FREE-RUN frames of a near-Nyquist
// clock at RANDOM phase + noise (3.3 samples/cycle, like a 150 MHz clock at
// 500 MSa/s) must reconstruct to a clean period with recovered frequency,
// correct amplitude, and measured ENOB gain. Run by superres_ets_node_test.go.
"use strict";
const E = require("./superres_ets.js");

let fails = 0;
function check(name, ok, detail) { console.log((ok ? "ok   " : "FAIL ") + name + (detail ? "  [" + detail + "]" : "")); if (!ok) fails++; }

function rng(seed) { let a = seed >>> 0; return () => { a |= 0; a = (a + 0x6d2b79f5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; }; }
const rnd = rng(0xC10C0);
function gauss() { const u = Math.max(rnd(), 1e-12), v = rnd(); return Math.sqrt(-2 * Math.log(u)) * Math.cos(2 * Math.PI * v); }

const dt = 2e-9, F = 150.0e6, N = 8192, NF = 60;
const DC = 184, A = 2.2, NOISE = 2.7; // ~4.4-code sine buried in 2.7-code noise (like the real bench)

// make a free-run frame at a random phase (shape: 'sine' or 'square')
function frame(shape) {
  const phi = 2 * Math.PI * rnd();
  const x = new Float64Array(N);
  for (let n = 0; n < N; n++) {
    const arg = 2 * Math.PI * F * n * dt + phi;
    let s = shape === "square" ? (Math.sin(arg) >= 0 ? 1 : -1) : Math.cos(arg);
    x[n] = DC + A * s + NOISE * gauss();
  }
  return x;
}

// single-period DFT amplitude at harmonic h (cycles per record = h)
function harmAmp(mean, h) {
  const nb = mean.length; let re = 0, im = 0, c = 0;
  for (let i = 0; i < nb; i++) { if (mean[i] < 0) continue; const p = 2 * Math.PI * h * i / nb; re += mean[i] * Math.cos(p); im += mean[i] * Math.sin(p); c++; }
  return c ? 2 * Math.hypot(re, im) / c : 0;
}

// ---- frequency refinement ----
{
  const x = frame("sine");
  const f = E.srEtsRefineFreq(x, dt, 150.05e6); // guess 500 ppm off
  check("frequency refined to <50 ppm", Math.abs(f - F) < F * 50e-6, `${(f / 1e6).toFixed(5)} MHz (${((f / F - 1) * 1e6).toFixed(0)} ppm)`);
}

// ---- reconstruct a free-run SINE ----
{
  const st = E.srEtsNew(240, dt);
  st.f = E.srEtsRefineFreq(frame("sine"), dt, 150.03e6);
  for (let i = 0; i < NF; i++) E.srEtsFeed(st, frame("sine"), null);
  const r = E.srEtsResult(st);
  check("all frames folded (none rejected)", r.frames === NF && r.rejected === 0, `${r.frames} folded, ${r.rejected} rej`);
  check("period grid ~fully filled", r.c1.fill > 0.98, `fill ${(r.c1.fill * 100).toFixed(0)}%`);
  const a1 = harmAmp(r.c1.mean, 1);
  check("reconstructed amplitude ≈ true (within 12%)", Math.abs(a1 - A) < 0.12 * A, `recon ${a1.toFixed(3)} vs ${A} codes`);
  check("period reported", Math.abs(r.periodS - 1 / F) / (1 / F) < 1e-3, `${(r.periodS * 1e9).toFixed(3)} ns`);
  // ENOB: buried in 2.7-code noise, averaging ~2000/bin should recover several bits
  check("measured ENOB gain > +4 bits", r.bitsGained > 4, `+${r.bitsGained.toFixed(1)} bits (σ ${r.sigmaSingle.toFixed(2)}→${r.sigmaStack.toFixed(3)})`);
  // the reconstruction is a clean fundamental: harmonic 2/3 far below the fundamental
  const h2 = harmAmp(r.c1.mean, 2), h3 = harmAmp(r.c1.mean, 3);
  check("sine: harmonics suppressed", h2 < 0.15 * a1 && h3 < 0.15 * a1, `h2 ${(h2 / a1).toFixed(2)} h3 ${(h3 / a1).toFixed(2)} of fund`);
}

// ---- reconstruct a free-run SQUARE: fundamental + odd harmonics present ----
{
  const st = E.srEtsNew(240, dt);
  st.f = E.srEtsRefineFreq(frame("square"), dt, 149.97e6);
  for (let i = 0; i < NF; i++) E.srEtsFeed(st, frame("square"), null);
  const r = E.srEtsResult(st);
  const a1 = harmAmp(r.c1.mean, 1), a3 = harmAmp(r.c1.mean, 3);
  // a square's 3rd harmonic is ~1/3 of the fundamental — the fold recovers it
  check("square: 3rd harmonic recovered (~1/3 fund)", a3 > 0.2 * a1 && a3 < 0.45 * a1, `h3/h1 = ${(a3 / a1).toFixed(2)}`);
}

// ---- weak/absent tone is rejected (no phase to fold) ----
{
  const st = E.srEtsNew(240, dt); st.f = 150e6;
  const flat = new Float64Array(N).fill(128);
  for (let n = 0; n < N; n++) flat[n] += 2.7 * gauss(); // pure noise, no tone
  const res = E.srEtsFeed(st, flat, null);
  check("pure-noise frame rejected (weak tone)", res === "rejected:weak", res);
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
