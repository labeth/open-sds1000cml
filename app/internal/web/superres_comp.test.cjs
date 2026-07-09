// Node tests for superres_comp.js: the analog-falloff compensation math. A
// synthetic multi-tone in code space is ATTENUATED by the measured cal H(f)
// (simulating the scope rolloff), then srCompensate must RESTORE the in-band
// tones toward flat — much closer to unity than the attenuated input — while
// preserving DC/offset and the gap sentinel. Run by superres_comp_node_test.go.
"use strict";
const C = require("./superres_comp.js");

let fails = 0;
function check(name, ok, detail) {
  console.log((ok ? "ok   " : "FAIL ") + name + (detail ? "  [" + detail + "]" : ""));
  if (!ok) fails++;
}

// ---- FFT/IFFT round-trip ----
{
  const N = 256;
  const re = new Float64Array(N), im = new Float64Array(N);
  for (let i = 0; i < N; i++) { re[i] = Math.sin(i * 0.3) + 0.5 * Math.cos(i * 1.1); }
  const re0 = Float64Array.from(re);
  C.srCompFFT(re, im, false);
  C.srCompFFT(re, im, true);
  let maxErr = 0;
  for (let i = 0; i < N; i++) maxErr = Math.max(maxErr, Math.abs(re[i] - re0[i]), Math.abs(im[i]));
  check("FFT/IFFT round-trips", maxErr < 1e-9, "maxErr=" + maxErr.toExponential(1));
}

// ---- filter figures ----
{
  const info = C.srCompInfo();
  check("recovered -3dB ~61 MHz", info.recoveredF3 > 55e6 && info.recoveredF3 < 66e6,
    (info.recoveredF3 / 1e6).toFixed(1) + " MHz");
  check("peak boost 6..9 dB", info.peakBoostDb > 6 && info.peakBoostDb < 9,
    info.peakBoostDb.toFixed(1) + " dB");
  check("no in-band peaking (boost bounded)", info.peakBoostDb < 12);
}

// ---- amplitude at a bin-exact tone via single-bin DFT ----
function toneAmp(x, M, cyclesPerRecord) {
  let re = 0, im = 0;
  for (let i = 0; i < M; i++) {
    const p = 2 * Math.PI * cyclesPerRecord * i / M;
    re += x[i] * Math.cos(p); im += x[i] * Math.sin(p);
  }
  return 2 * Math.hypot(re, im) / M;
}

// ---- restore an attenuated multi-tone toward flat ----
{
  const M = 4096;               // power of two -> no resample, pure FFT path
  const dtFine = 0.5e-9;        // 2 GSa/s fine grid
  const T = M * dtFine;         // 2048 ns record
  const A = 12;                 // code amplitude per tone
  const DC = 128;
  // bin-exact tones (cycles per record), so no leakage:
  const tones = [
    { m: 10 }, { m: 41 }, { m: 82 }, { m: 102 }, { m: 123 },
  ].map(t => ({ m: t.m, f: t.m / T }));
  const clean = new Float32Array(M), atten = new Float32Array(M);
  for (let i = 0; i < M; i++) {
    let c = DC, a = DC;
    for (const t of tones) {
      const ph = 2 * Math.PI * t.m * i / M;
      c += A * Math.cos(ph);
      a += A * C.srCompCalH(t.f) * Math.cos(ph); // scope attenuation
    }
    clean[i] = c; atten[i] = a;
  }
  const r = C.srCompensate(atten, dtFine);
  const comp = r.comp;

  // DC preserved.
  let sc = 0, sr = 0;
  for (let i = 0; i < M; i++) { sc += atten[i]; sr += comp[i]; }
  check("DC/offset preserved", Math.abs(sc / M - sr / M) < 0.5,
    "in=" + (sc / M).toFixed(2) + " out=" + (sr / M).toFixed(2));

  // Per-tone restoration: compensated ratio to clean must beat the attenuated
  // ratio, and be ~1 in the flat band.
  for (const t of tones) {
    const rc = toneAmp(comp, M, t.m) / A;   // compensated / clean
    const ra = C.srCompCalH(t.f);           // attenuated / clean
    const flat = t.f <= 50e6;
    // Flat band: restored to ~unity. Above it: meaningfully lifted vs the
    // attenuated input (but the target deliberately rolls, so not to unity).
    const ok = flat ? (rc > 0.82 && rc < 1.18) : (rc > ra + 0.2);
    check(`tone ${(t.f / 1e6).toFixed(0)}MHz restored`, ok && rc >= ra - 0.03,
      `atten=${ra.toFixed(2)} comp=${rc.toFixed(2)}${flat ? " (flat band)" : ""}`);
  }

  // No spurious negatives (would read as gaps).
  let neg = 0;
  for (let i = 0; i < M; i++) if (comp[i] < 0) neg++;
  check("no fabricated negatives", neg === 0, "neg=" + neg);
}

// ---- gap sentinel preserved ----
{
  const M = 1024, dtFine = 0.5e-9;
  const x = new Float32Array(M);
  for (let i = 0; i < M; i++) x[i] = 128 + 20 * Math.cos(2 * Math.PI * 20 * i / M);
  x[100] = -1; x[101] = -1; x[500] = -1; // gaps
  const r = C.srCompensate(x, dtFine);
  check("gaps preserved as -1", r.comp[100] === -1 && r.comp[101] === -1 && r.comp[500] === -1);
  check("non-gap samples finite & >=0", r.comp[200] >= 0 && isFinite(r.comp[200]));
}

// ---- non-power-of-two length (exercises the resample path) ----
{
  const M = 3000, dtFine = 0.5e-9, T = M * dtFine;
  const cyc = 45, f = cyc / T;   // 45 cycles / 1500 ns = 30 MHz, bin-exact
  const atten = new Float32Array(M);
  for (let i = 0; i < M; i++) atten[i] = 128 + 12 * C.srCompCalH(f) * Math.cos(2 * Math.PI * cyc * i / M);
  const r = C.srCompensate(atten, dtFine);
  const rc = toneAmp(r.comp, M, cyc) / 12;
  check(`resample path restores ${(f / 1e6).toFixed(0)}MHz tone`, rc > 0.88 && rc < 1.15, "comp=" + rc.toFixed(2));
}

// ---- adaptive target: more bits → higher recovered −3 dB, bounded by budget ----
{
  const nyq = 250e6;
  let prev = 0, mono = true;
  const rows = [];
  for (const bits of [1.5, 2.3, 3.3, 4, 5, 6]) {
    const o = C.srCompAuto(bits, nyq, 0.8);
    const info = C.srCompInfo(o);
    rows.push({ bits, f3: info.recoveredF3, peak: info.peakBoostDb, budget: o.budgetDb });
    if (info.recoveredF3 < prev - 1e6) mono = false;
    prev = info.recoveredF3;
  }
  check("auto: recovered −3dB rises with bits", mono,
    rows.map(r => `${r.bits}b→${(r.f3 / 1e6).toFixed(0)}MHz`).join(" "));
  const at23 = rows.find(r => r.bits === 2.3), at5 = rows.find(r => r.bits === 5);
  check("auto: +2.3 bit recovers ~70-85 MHz", at23.f3 > 65e6 && at23.f3 < 90e6, (at23.f3 / 1e6).toFixed(0) + " MHz");
  // A deep stack pushes HIGH (into the extrapolated band) — kept in; the detrend
  // keeps it from ringing (see the edge-gate test below).
  check("auto: +5 bit pushes past 130 MHz", at5.f3 > 130e6, (at5.f3 / 1e6).toFixed(0) + " MHz");
  check("auto: peak boost tracks the spent budget", rows.every(r => r.peak <= r.budget + 2.5),
    rows.map(r => `+${r.peak.toFixed(1)}/${r.budget.toFixed(1)}`).join(" "));
  const huge = C.srCompAuto(12, nyq, 0.95);
  check("auto: fbw capped at 0.8·Nyquist", huge.fbw <= 0.8 * nyq + 1, (huge.fbw / 1e6).toFixed(0) + " MHz");
}

// ---- edge-gate ringing: a gated single EDGE is a step; the detrend must keep
// the circular-FFT wrap from ringing across the record even at max boost ----
{
  const M = 4096, dtFine = 1 / 32e9, lo = 60, hi = 200;
  const clean = new Float32Array(M); // chain-limited edge (~22 ns rise — the real signal can't be sharper)
  for (let i = 0; i < M; i++) clean[i] = (lo + hi) / 2 + (hi - lo) / 2 * Math.tanh((i - M / 2) / 160);
  // aggressive auto budget (deep stack) — the exact case that rang before
  const opts = C.srCompAuto(9.6, 250e6, 0.8);
  const comp = C.srCompensate(clean, dtFine, opts).comp;
  let mn = 1e9, mx = -1e9, wrap = 0;
  for (let i = 0; i < M; i++) { if (comp[i] < mn) mn = comp[i]; if (comp[i] > mx) mx = comp[i]; }
  for (let i = 0; i < 40; i++) wrap = Math.max(wrap, Math.abs(comp[i] - clean[i]), Math.abs(comp[M - 1 - i] - clean[M - 1 - i]));
  const span = hi - lo;
  check("edge-gate: total swing bounded (< 1.6× signal, was 5.2×)", (mx - mn) < 1.6 * span, `${((mx - mn) / span).toFixed(2)}×`);
  check("edge-gate: NO wrap-boundary ringing (detrend)", wrap < 0.05 * span, `boundary dev ${wrap.toFixed(1)} codes`);
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
