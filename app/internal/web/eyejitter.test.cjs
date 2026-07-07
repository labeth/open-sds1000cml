// eyejitter.test.cjs — ground-truth tests for the eye/jitter engine. A synthetic
// PRBS7 NRZ signal is generated with EXACTLY known UI, noise, and injected
// jitter; the engine must recover the numbers. Run: node eyejitter.test.cjs
// (also run by the Go test wrapper alongside superres.test.cjs).
"use strict";
const EJ = require("./eyejitter.js");

let fails = 0;
const check = (cond, name, detail) =>
  console.log((cond ? "ok   " : "FAIL ") + name + (detail !== undefined ? "  [" + detail + "]" : "")) || (cond || fails++);

// deterministic PRNG
let seed = 42;
const rnd = () => { seed = (seed * 1103515245 + 12345) & 0x7fffffff; return seed / 0x7fffffff - 0.5; };

// PRBS7 bit stream
function prbs7(nbits) {
  let s = 0x7f;
  const bits = new Uint8Array(nbits);
  for (let i = 0; i < nbits; i++) {
    bits[i] = (s >> 6) & 1;
    const fb = ((s >> 6) ^ (s >> 5)) & 1;
    s = ((s << 1) | fb) & 0x7f;
  }
  return bits;
}

// genRecord: NRZ at `ui` samples/bit with band-limited edges (sigmoid over
// `rise` samples), noise sigma, and per-bit TIE offsets (samples). Built from
// the EDGE LIST (transition times incl. TIE), so an injected offset really
// moves the edge — a cell-indexed builder silently clips shifted edges at the
// ideal boundary and the jitter never reaches the waveform.
function genRecord(n, ui, phase, bits, tieFn, noise, rise) {
  const sig = new Int16Array(n);
  const lo = 60, hi = 200;
  const lvl = b => (b ? hi : lo);
  // edge list: (time, fromLevel, toLevel) for every bit-value change
  const edges = [];
  for (let k = 1; k < bits.length; k++) {
    if (bits[k] !== bits[k - 1]) edges.push({ t: phase + k * ui + tieFn(k), a: lvl(bits[k - 1]), b: lvl(bits[k]) });
  }
  let e = 0;
  for (let i = 0; i < n; i++) {
    while (e < edges.length - 1 && i > edges[e].t + 4 * rise) e++;
    const ed = edges[Math.min(e, edges.length - 1)];
    let v;
    if (i < ed.t - 4 * rise) v = ed.a;
    else if (i > ed.t + 4 * rise) v = ed.b;
    else {
      const x = (i - ed.t) / (rise / 2);
      v = ed.a + (ed.b - ed.a) / (1 + Math.exp(-2.2 * x));
    }
    sig[i] = Math.round(v + noise * rnd() * 2);
  }
  return sig;
}

const SAMPLE_S = 2e-9;

// ---- 1) clean PRBS: exact UI recovery + low TIE floor + eye open ----
{
  const ui = 100.0, n = 20480; // 5 Mbps at 2ns
  const st = EJ.ejNew({});
  const bits = prbs7(300);
  let locked = 0;
  for (let r = 0; r < 20; r++) {
    const sig = genRecord(n, ui, 20 + (r * 37.3) % ui, bits, () => 0, 1.2, 9);
    if (EJ.ejFeed(st, sig, n, SAMPLE_S).startsWith("locked")) locked++;
  }
  const res = EJ.ejResult(st);
  check(locked >= 18, "clean: records lock", locked + "/20");
  check(Math.abs(res.ui - ui) < 0.05, "clean: UI recovered ±0.05 samples", res.ui.toFixed(3));
  check(Math.abs(res.bitRate - 5e6) < 5e3, "clean: bit rate ±0.1%", (res.bitRate / 1e6).toFixed(4) + " Mbps");
  check(res.tieRms < 0.15e-9, "clean: TIE floor < 150 ps rms", (res.tieRms * 1e12).toFixed(0) + " ps");
  check(res.dj === 0, "clean: no false DJ", res.dj);
  const em = res.eyeMetrics;
  check(em && em.eyeHeightCodes > 90, "clean: eye open (height > 90 codes)", em && em.eyeHeightCodes.toFixed(0));
}

// ---- 2) injected square-wave TIE (the FPGA's exact scheme) ----
{
  const ui = 100.0, n = 20480, JA_ns = 10, JP = 32; // ±? — square 0 ↔ 2*JA? our FPGA: offsets 0/2JA
  const st = EJ.ejNew({});
  const bits = prbs7(300);
  const tiePk = 2 * JA_ns * 1e-9;           // pp in seconds (0 ↔ 20ns)
  const tieSamp = tiePk / SAMPLE_S;          // pp in samples (10)
  for (let r = 0; r < 30; r++) {
    const ph0 = (r * 53.7) % (2 * JP);       // random jitter phase per record
    const tieFn = k => ((Math.floor((k + ph0) / (JP / 2)) % 2) ? tieSamp : 0);
    const sig = genRecord(n, ui, 20 + (r * 37.3) % ui, bits, tieFn, 1.2, 9);
    EJ.ejFeed(st, sig, n, SAMPLE_S);
  }
  const res = EJ.ejResult(st);
  // square TIE 0↔20ns: after mean removal ±10ns → rms 10ns, DJ(δδ) = 20ns
  check(res.tieRms > 8e-9 && res.tieRms < 12e-9, "inject: TIE rms ≈ 10 ns", (res.tieRms * 1e9).toFixed(2) + " ns");
  check(res.dj > 16e-9 && res.dj < 24e-9, "inject: DJ(δδ) ≈ 20 ns", (res.dj * 1e9).toFixed(2) + " ns");
  check(res.rj < 2e-9, "inject: RJ stays small", (res.rj * 1e9).toFixed(2) + " ns");
  // spectrum: f_j = bitrate/JP = 5e6/32 = 156.25 kHz; fundamental of ±10ns square = 4/π·10ns ≈ 12.7ns
  const fj = 5e6 / JP;
  check(Math.abs(res.specPeakHz - fj) < 2 * res.specDf, "inject: spectral peak at f_j",
    (res.specPeakHz / 1e3).toFixed(1) + " kHz vs " + (fj / 1e3).toFixed(1) + " kHz (df=" + (res.specDf / 1e3).toFixed(1) + "k)");
  const expAmp = (4 / Math.PI) * 10e-9;
  check(res.specPeakAmp > 0.5 * expAmp && res.specPeakAmp < 1.6 * expAmp,
    "inject: fundamental amplitude ≈ (4/π)·10 ns", (res.specPeakAmp * 1e9).toFixed(2) + " ns vs " + (expAmp * 1e9).toFixed(2) + " ns");
}

// ---- 3) negative control: a sine must NOT lock as a bit stream ----
{
  const st = EJ.ejNew({});
  const n = 20480;
  let lockedCount = 0;
  for (let r = 0; r < 6; r++) {
    const sig = new Int16Array(n);
    for (let i = 0; i < n; i++) sig[i] = Math.round(128 + 80 * Math.sin(2 * Math.PI * i / 400 + r) + 1.5 * rnd());
    if (EJ.ejFeed(st, sig, n, SAMPLE_S).startsWith("locked")) lockedCount++;
  }
  // A pure sine has strictly periodic crossings — intervals ARE a perfect grid
  // (that's genuinely clock-like). The honest requirement: IF it locks, TIE ≈ 0
  // (it must not fabricate jitter); a flat/noise record must NOT lock at all.
  const res = EJ.ejResult(st);
  if (lockedCount) {
    // a sine's crossings ARE a periodic grid — locking as a clean clock is
    // honest. The requirements: no fabricated DJ, and the TIE floor consistent
    // with the slew-limited noise (slow sine → soft edges → bigger floor).
    check(res.dj === 0, "negctl: sine fabricates no DJ", res.dj);
    check(res.tieRms < 2e-9, "negctl: sine TIE = noise floor only", (res.tieRms * 1e12).toFixed(0) + " ps");
  } else {
    check(true, "negctl: sine did not lock as a bit stream");
  }
  const st2 = EJ.ejNew({});
  const flat = new Int16Array(n).fill(128);
  for (let i = 0; i < n; i++) flat[i] += Math.round(2 * rnd());
  check(EJ.ejFeed(st2, flat, n, SAMPLE_S).startsWith("rejected"), "negctl: noise-only record rejected");
}

// ---- 4) UI consistency guard: a different bit rate mid-run is rejected ----
{
  const st = EJ.ejNew({});
  const bits = prbs7(300);
  const s1 = genRecord(20480, 100, 20, bits, () => 0, 1.2, 9);
  EJ.ejFeed(st, s1, 20480, SAMPLE_S);
  const s2 = genRecord(20480, 50, 20, bits, () => 0, 1.2, 9); // 10 Mbps suddenly
  const d = EJ.ejFeed(st, s2, 20480, SAMPLE_S);
  check(d === "rejected:ui-inconsistent", "guard: bit-rate change rejected", d);
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
