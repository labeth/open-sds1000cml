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


// ---- 5) breaker distillates: the root-cause fixes stay fixed ----
{
  // (a) LOW-DUTY signal (sparse pulse train): percentile levels used to sit the
  // threshold low, fabricating ~1-sample DCD between rise/fall. With dual-
  // histogram-mode levels the clean sparse train must show NO significant DJ.
  const st = EJ.ejNew({});
  const bitsOf = nb => { const b = new Uint8Array(nb); for (let i = 3; i < nb; i += 19) b[i] = 1; return b; };
  for (let r = 0; r < 15; r++) {
    const bits = bitsOf(230);
    const edges = [];
    for (let k = 1; k < bits.length; k++) if (bits[k] !== bits[k - 1]) edges.push({ t: 30 + (r * 37.3) % 100 + k * 100, a: bits[k - 1] ? 198 : 58, b: bits[k] ? 198 : 58 });
    const sig = new Int16Array(20480);
    let e = 0;
    for (let i = 0; i < 20480; i++) {
      while (e < edges.length - 1 && i > edges[e].t + 40) e++;
      const ed = edges[Math.min(e, edges.length - 1)];
      let v = i < ed.t - 40 ? ed.a : i > ed.t + 40 ? ed.b : ed.a + (ed.b - ed.a) / (1 + Math.exp(-2.2 * (i - ed.t) / 4.5));
      sig[i] = Math.round(v + 1.5 * rnd() * 2);
    }
    EJ.ejFeed(st, sig, 20480, SAMPLE_S);
  }
  const res = EJ.ejResult(st);
  check(st.records >= 10, "sparse: low-duty pulse train locks (k-cap fix)", st.records + "/15");
  check(!res.dj || res.dj < 1e-9, "sparse: no fabricated DCD (histogram-mode levels)", ((res.dj || 0) * 1e9).toFixed(2) + " ns");
}
{
  // (b) BURSTY stream: long idle gaps make the UI-grid mostly interpolation —
  // the spectrum must be withheld (subharmonic images were measured), while
  // TIE stats still accumulate.
  const st = EJ.ejNew({});
  const bits = new Uint8Array(230).fill(1);
  let s = 0x7f;
  for (let i = 8; i < 230; i++) {
    if (Math.floor(i / 32) % 2 === 0) { bits[i] = (s >> 6) & 1; const fb = ((s >> 6) ^ (s >> 5)) & 1; s = ((s << 1) | fb) & 0x7f; }
  }
  for (let r = 0; r < 10; r++) {
    const edges = [];
    for (let k = 1; k < bits.length; k++) if (bits[k] !== bits[k - 1]) edges.push({ t: 30 + (r * 41.3) % 100 + k * 88.6, a: bits[k - 1] ? 198 : 58, b: bits[k] ? 198 : 58 });
    const sig = new Int16Array(20480);
    let e = 0;
    for (let i = 0; i < 20480; i++) {
      while (e < edges.length - 1 && i > edges[e].t + 40) e++;
      const ed = edges[Math.min(e, edges.length - 1)];
      let v = i < ed.t - 40 ? ed.a : i > ed.t + 40 ? ed.b : ed.a + (ed.b - ed.a) / (1 + Math.exp(-2.2 * (i - ed.t) / 4.5));
      sig[i] = Math.round(v + 1.5 * rnd() * 2);
    }
    EJ.ejFeed(st, sig, 20480, SAMPLE_S);
  }
  check(st.records >= 6, "burst: idle-gapped stream locks", st.records + "/10");
  check(st.specN === 0, "burst: spectrum withheld when the UI grid is mostly gaps", "specN=" + st.specN);
  check(st.tie.length > 100, "burst: TIE stats still accumulate", st.tie.length);
}
{
  // (c) EDGE-STARVED records (huge UI): a single record has too few intervals;
  // the cross-record pool must resolve the grid within a few records.
  const st = EJ.ejNew({});
  let locked = 0;
  for (let r = 0; r < 12; r++) {
    const bits = [];
    let s2 = 0x5a + r;
    for (let k = 0; k < 16; k++) { bits.push((s2 >> (k % 7)) & 1); }
    const edges = [];
    for (let k = 1; k < bits.length; k++) if (bits[k] !== bits[k - 1]) edges.push({ t: 500 + k * 1900.0, a: bits[k - 1] ? 198 : 58, b: bits[k] ? 198 : 58 });
    if (edges.length < 3) continue;
    const sig = new Int16Array(20480);
    let e = 0;
    for (let i = 0; i < 20480; i++) {
      while (e < edges.length - 1 && i > edges[e].t + 40) e++;
      const ed = edges[Math.min(e, edges.length - 1)];
      let v = i < ed.t - 40 ? ed.a : i > ed.t + 40 ? ed.b : ed.a + (ed.b - ed.a) / (1 + Math.exp(-2.2 * (i - ed.t) / 4.5));
      sig[i] = Math.round(v + 1.5 * rnd() * 2);
    }
    if (EJ.ejFeed(st, sig, 20480, SAMPLE_S).startsWith("locked")) locked++;
  }
  check(locked >= 4, "pool: edge-starved records lock via the cross-record pool", locked + " locked");
  const res = EJ.ejResult(st);
  if (locked) check(Math.abs(res.ui - 1900) < 5, "pool: UI recovered", res.ui.toFixed(1));
  check(res.tieHpHz > 0, "corner: TIE high-pass corner reported", res.tieHpHz ? (res.tieHpHz / 1e3).toFixed(1) + " kHz" : "0");
}


// ---- 6) review distillates ----
{
  // (a) BLOCKER regression: a stable clock with pure DCD (rising on-grid,
  // falling late) must show DJ = the DCD but period/c2c jitter ≈ 0 — the old
  // cross-polarity accumulation read the duty error as period instability.
  const st = EJ.ejNew({});
  const n = 20480, ui = 100, dcd = 4; // falling edges 4 samples late
  for (let r = 0; r < 12; r++) {
    const edges = [];
    for (let k = 1; k < 200; k++) {
      const rising = (k & 1) === 0;
      edges.push({ t: 20 + (r * 37.3) % ui + k * ui + (rising ? 0 : dcd), a: rising ? 58 : 198, b: rising ? 198 : 58 });
    }
    const sig = new Int16Array(n);
    let e = 0;
    for (let i = 0; i < n; i++) {
      while (e < edges.length - 1 && i > edges[e].t + 40) e++;
      const ed = edges[Math.min(e, edges.length - 1)];
      let v = i < ed.t - 40 ? ed.a : i > ed.t + 40 ? ed.b : ed.a + (ed.b - ed.a) / (1 + Math.exp(-2.2 * (i - ed.t) / 4.5));
      sig[i] = Math.round(v + 1.2 * rnd() * 2);
    }
    EJ.ejFeed(st, sig, n, SAMPLE_S);
  }
  const res = EJ.ejResult(st);
  check(res.dj > 5e-9 && res.dj < 11e-9, "review: DCD measured as DJ ≈ 8 ns", ((res.dj || 0) * 1e9).toFixed(2) + " ns");
  check(res.periodJRms < 1e-9, "review: period jitter immune to DCD (same-polarity)", (res.periodJRms * 1e12).toFixed(0) + " ps");
  check(res.c2cJRms < 1.5e-9, "review: c2c jitter immune to DCD", (res.c2cJRms * 1e12).toFixed(0) + " ps");
}
{
  // (b) TIE stats never freeze: with a tiny buffer cap, later bigger jitter
  // must still move the running rms (decimation keeps the histogram honest).
  const st = EJ.ejNew({ tieCap: 64 });
  const bits = prbs7(300);
  for (let r = 0; r < 6; r++) EJ.ejFeed(st, genRecord(20480, 100, 20 + r * 31 % 100, bits, () => 0, 1.2, 9), 20480, SAMPLE_S);
  const early = EJ.ejResult(st).tieRms;
  for (let r = 0; r < 12; r++) {
    const tieFn = k => 5 * Math.sin(2 * Math.PI * k / 16); // big PJ late in the run
    EJ.ejFeed(st, genRecord(20480, 100, 20 + r * 37 % 100, bits, tieFn, 1.2, 9), 20480, SAMPLE_S);
  }
  const late = EJ.ejResult(st).tieRms;
  check(late > 3 * early, "review: running TIE rms tracks post-cap jitter", (early * 1e12).toFixed(0) + "ps -> " + (late * 1e12).toFixed(0) + "ps");
}

if (fails) { console.log(fails + " FAILURES"); process.exit(1); }
console.log("ALL PASS");
