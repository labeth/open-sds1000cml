// eyejitter_breaker.cjs — adversarial harness for the eye/jitter engine.
//
// DEV TOOL (not part of go test — seconds): run after any engine change.
// 50 seeded signal families spanning encodings, unit intervals, jitter types
// and impairments, every one with GROUND TRUTH (the injected TIE per edge is
// known exactly). The invariant: the engine must either measure the truth
// (UI, TIE rms, DJ, spectral peak) within stated tolerances, or reject/refuse
// HONESTLY — it must never lock wrong, fabricate jitter, or crash.
//
// Usage: node eyejitter_breaker.cjs [lo hi] [--json out.json] [-v]
"use strict";
const EJ = require("./eyejitter.js");

const SAMPLE_S = 2e-9;
const N = 20480;

function makeRng(seed) {
  let s = seed >>> 0 || 1;
  return () => { s = (s * 1103515245 + 12345) & 0x7fffffff; return s / 0x7fffffff; };
}
function gauss(r) { // Box-Muller-ish from two uniforms
  return Math.sqrt(-2 * Math.log(Math.max(1e-12, r()))) * Math.cos(2 * Math.PI * r());
}

// ---------- bit-stream sources ----------
function prbs(nbits, taps) { // taps: [7,6] PRBS7 | [15,14] PRBS15
  const [a, b] = taps;
  let s = (1 << a) - 1;
  const bits = new Uint8Array(nbits);
  for (let i = 0; i < nbits; i++) {
    bits[i] = (s >> (a - 1)) & 1;
    const fb = ((s >> (a - 1)) ^ (s >> (b - 1))) & 1;
    s = ((s << 1) | fb) & ((1 << a) - 1);
  }
  return bits;
}
const STREAMS = {
  prbs7:  (nb, r) => prbs(nb, [7, 6]),
  prbs15: (nb, r) => prbs(nb, [15, 14]),
  uart:   (nb, r) => { // framed bytes: start(0) 8 random stop(1) idle(1x3)
    const bits = new Uint8Array(nb);
    let i = 0;
    while (i < nb) {
      bits[i++] = 0;
      for (let k = 0; k < 8 && i < nb; k++) bits[i++] = r() > 0.5 ? 1 : 0;
      for (let k = 0; k < 4 && i < nb; k++) bits[i++] = 1;
    }
    return bits;
  },
  burst:  (nb, r) => { // packets of PRBS with LONG idle gaps (25 bits high)
    const p = prbs(nb, [7, 6]);
    const bits = new Uint8Array(nb).fill(1);
    let i = 8;
    while (i + 40 < nb) {
      for (let k = 0; k < 32 && i < nb; k++) { bits[i] = p[i]; i++; }
      i += 25 + Math.floor(10 * r());
    }
    return bits;
  },
  clock:  nb => { const b = new Uint8Array(nb); for (let i = 0; i < nb; i++) b[i] = i & 1; return b; },
  pwm:    (nb, r) => { // duty wanders slowly — edge pairs move together
    const b = new Uint8Array(nb);
    for (let i = 0; i < nb; i++) b[i] = i & 1; // toggling base; duty applied via DCD-like tie below
    return b;
  },
  sparse: (nb, r) => { // isolated pulses: 1 bit high every ~20 bits
    const b = new Uint8Array(nb);
    for (let i = 3; i < nb; i += 17 + Math.floor(6 * r())) b[i] = 1;
    return b;
  },
};

// ---------- jitter models (per-edge TIE truth, in SAMPLES) ----------
// each returns tie(k) where k = bit index of the edge; also carries meta about
// what the engine is expected to report.
function jitterModel(kind, ui, r, fam) {
  switch (kind) {
    case "none":   return { fn: () => 0, rmsS: 0, djS: 0, tone: null };
    case "square": { // the FPGA scheme: 0 ↔ 2A alternating every JP/2 bits
      const A = fam.jaSamp, JP = fam.jp;
      return {
        fn: k => (Math.floor(k / (JP / 2)) % 2) ? 2 * A : 0,
        rmsS: A * SAMPLE_S, djS: 2 * A * SAMPLE_S,
        tone: { fHz: 1 / (JP * ui * SAMPLE_S), ampS: (4 / Math.PI) * A * SAMPLE_S },
      };
    }
    case "sine": {
      const A = fam.jaSamp, JP = fam.jp;
      return {
        fn: k => A * Math.sin(2 * Math.PI * k / JP),
        rmsS: A / Math.SQRT2 * SAMPLE_S, djS: -1 /*bounded, don't assert exact*/,
        tone: { fHz: 1 / (JP * ui * SAMPLE_S), ampS: A * SAMPLE_S },
      };
    }
    case "gauss": { // pure RJ
      const sig = fam.jaSamp;
      return { fn: () => sig * gauss(r), rmsS: sig * SAMPLE_S, djS: 0, tone: null, rjS: sig * SAMPLE_S };
    }
    case "dcd": { // duty-cycle distortion: rising early, falling late by ±A
      const A = fam.jaSamp;
      return { fn: (k, pol) => (pol > 0 ? -A : A), rmsS: A * SAMPLE_S, djS: 2 * A * SAMPLE_S, tone: null, isDcd: true };
    }
    case "isi": { // deterministic, data-dependent: edge late after a long run
      const A = fam.jaSamp;
      return { fn: (k, pol, runLen) => Math.min(runLen, 4) / 4 * A, rmsS: -1, djS: -1, tone: null };
    }
  }
}

// ---------- waveform builder (edge-list-driven; the superres lesson) ----------
function buildRecord(fam, frameIdx) {
  const r = makeRng(fam.fi * 92821 + frameIdx * 613 + 5);
  const ui = fam.ui;
  const nbits = Math.ceil(N / ui) + 8;
  // rotate the stream window per record: a huge-UI record holds only ~10 bits,
  // and replaying the same slice every time (plus PRBS7's leading ones-run)
  // starves the CDR of edges through no fault of the engine
  const off = (frameIdx * 13) % 127;
  const bits = STREAMS[fam.stream](nbits + off, r).subarray(off);
  const jm = jitterModel(fam.jitter, ui, r, fam);
  const phase = 4 + r() * ui;
  const lo = 128 - fam.amp, hi = 128 + fam.amp;
  const lvl = b => (b ? hi : lo);
  // edge list with truth TIE
  const edges = [];
  let runLen = 1;
  for (let k = 1; k < nbits; k++) {
    if (bits[k] !== bits[k - 1]) {
      const pol = bits[k] ? 1 : -1;
      const tie = jm.fn(k, pol, runLen);
      edges.push({ t: phase + k * ui + tie, a: lvl(bits[k - 1]), b: lvl(bits[k]), tie, pol, k });
      runLen = 1;
    } else runLen++;
  }
  const sig = new Int16Array(N);
  const rise = fam.rise;
  // SUPERPOSITION of step responses (v = firstLevel + Σ Δ·σ) — a nearest-edge
  // builder distorts whenever transitions overlap within the rise span (small
  // UI), fabricating crossings the engine gets blamed for.
  let eLo = 0, eHi = 0;
  const win = 5 * rise;
  for (let i = 0; i < N; i++) {
    while (eLo < edges.length && edges[eLo].t < i - win) eLo++;
    while (eHi < edges.length && edges[eHi].t <= i + win) eHi++;
    let v = eLo < edges.length ? edges[eLo].a : (edges.length ? edges[edges.length - 1].b : 128);
    for (let e = eLo; e < eHi; e++) {
      v += (edges[e].b - edges[e].a) / (1 + Math.exp(-2.2 * (i - edges[e].t) / (rise / 2)));
    }
    // impairments
    if (fam.wander) v += fam.wander * Math.sin(2 * Math.PI * i / N * 1.7 + frameIdx);
    if (fam.am) v = 128 + (v - 128) * (1 + fam.am * Math.sin(2 * Math.PI * i / N * 3.1));
    v += fam.noise * (r() - 0.5) * 2;
    if (fam.clip) v = Math.max(128 - fam.amp * 0.92, Math.min(128 + fam.amp * 0.92, v));
    sig[i] = Math.max(0, Math.min(255, Math.round(v)));
  }
  // dropouts: blank a random span to the idle level (kills edges there)
  if (fam.dropout && r() < 0.5) {
    const s0 = Math.floor(r() * (N - 2000)), s1 = s0 + 500 + Math.floor(1500 * r());
    for (let i = s0; i < s1; i++) sig[i] = lvl(1);
  }
  return { sig, edges, jm };
}

// ---------- the 50 families ----------
function families() {
  const F = [];
  const add = (name, o) => F.push(Object.assign({
    fi: F.length, name, stream: "prbs7", ui: 100, amp: 70, noise: 1.5, rise: 9,
    jitter: "none", jaSamp: 0, jp: 32, wander: 0, am: 0, clip: false, dropout: false,
    expect: "measure", // measure | reject | lockOnly (lock w/o fabricating jitter)
  }, o));

  // -- encodings (clean) --
  add("prbs7-clean", {});
  add("prbs15-clean", { stream: "prbs15" });
  add("uart-framed", { stream: "uart" });
  add("burst-idle", { stream: "burst" });
  add("clock-50", { stream: "clock", expect: "lockOnly" });
  add("sparse-pulses", { stream: "sparse" });
  // -- unit intervals --
  add("ui-frac-73.4", { ui: 73.4 });
  add("ui-frac-131.7", { ui: 131.7 });
  add("ui-tiny-6", { ui: 6, rise: 3, noise: 1.0 });
  add("ui-tiny-4", { ui: 4, rise: 2, noise: 0.8 });
  add("ui-small-10", { ui: 10, rise: 4 });
  add("ui-small-25", { ui: 25, rise: 6 });
  add("ui-huge-2000", { ui: 2000 });
  add("ui-odd-997", { ui: 997 });
  // -- jitter: square (the validated scheme) at various points --
  add("sq-5samp", { jitter: "square", jaSamp: 5 });
  add("sq-1samp", { jitter: "square", jaSamp: 1 });
  add("sq-subsample", { jitter: "square", jaSamp: 0.4 });
  add("sq-fast-jp8", { jitter: "square", jaSamp: 3, jp: 8 });
  add("sq-slow-jp256", { jitter: "square", jaSamp: 3, jp: 256 });
  add("sq-frac-ui", { ui: 73.4, jitter: "square", jaSamp: 3 });
  // -- jitter: sinusoidal --
  add("sin-5samp", { jitter: "sine", jaSamp: 5 });
  add("sin-1samp", { jitter: "sine", jaSamp: 1 });
  add("sin-fast-jp8", { jitter: "sine", jaSamp: 3, jp: 8 });
  add("sin-slow-jp512", { jitter: "sine", jaSamp: 3, jp: 512 });
  // -- jitter: random (RJ) --
  add("rj-0.5samp", { jitter: "gauss", jaSamp: 0.5 });
  add("rj-2samp", { jitter: "gauss", jaSamp: 2 });
  add("rj-8samp-heavy", { jitter: "gauss", jaSamp: 8 });
  // -- DCD + ISI --
  add("dcd-2samp", { jitter: "dcd", jaSamp: 2 });
  add("dcd-6samp", { jitter: "dcd", jaSamp: 6 });
  add("isi-4samp", { jitter: "isi", jaSamp: 4 });
  // -- eye-closure pushes --
  add("push-jitter-0.3ui", { jitter: "gauss", jaSamp: 30, expect: "rejectOrLock" }); // σ = 0.3 UI: beyond ANY CDR — honest rejection is correct
  add("push-slowrise-half-ui", { rise: 50 });                // rise = UI/2: soft eye
  add("push-noise-quarter-swing", { noise: 17 });
  add("push-small-swing", { amp: 16, noise: 2 });
  add("push-tiny-ui-jitter", { ui: 6, rise: 3, jitter: "square", jaSamp: 1, noise: 1.0 });
  // -- impairments --
  add("wander-baseline", { wander: 20 });
  add("am-20pct", { am: 0.2, amPm: true }); // AM->PM at a fixed threshold is physics, not fabrication
  add("clip-rails", { clip: true });
  add("dropout-idle", { dropout: true });
  add("uart-jitter", { stream: "uart", jitter: "square", jaSamp: 3 });
  add("burst-jitter", { stream: "burst", jitter: "square", jaSamp: 3 });
  add("sparse-rj", { stream: "sparse", jitter: "gauss", jaSamp: 1 });
  add("clock-dcd", { stream: "clock", jitter: "dcd", jaSamp: 3, expect: "measure" });
  // -- non-serial / degenerate: must reject or lock honestly --
  add("sine-analog", { special: "sine", expect: "lockOnly" });
  add("noise-only", { special: "noise", expect: "reject" });
  add("flat", { special: "flat", expect: "reject" });
  add("chirp", { special: "chirp", expect: "reject" });
  add("two-tone", { special: "twotone", expect: "rejectOrLock" });
  add("staircase", { special: "stairs", expect: "rejectOrLock" });
  add("prbs-plus-glitches", { glitch: true });
  return F;
}

function buildSpecial(fam, frameIdx) {
  const r = makeRng(fam.fi * 92821 + frameIdx * 613 + 5);
  const sig = new Int16Array(N);
  for (let i = 0; i < N; i++) {
    let v = 128;
    if (fam.special === "sine") v = 128 + 80 * Math.sin(2 * Math.PI * i / 400 + frameIdx);
    else if (fam.special === "noise") v = 128 + 40 * (r() - 0.5) * 2;
    else if (fam.special === "flat") v = 128;
    else if (fam.special === "chirp") { const t = i / N; v = 128 + 70 * Math.sin(2 * Math.PI * (5 * t + 120 * t * t) * 8); }
    else if (fam.special === "twotone") v = 128 + 45 * Math.sin(2 * Math.PI * i / 300) + 45 * Math.sin(2 * Math.PI * i / 77);
    else if (fam.special === "stairs") v = 60 + 20 * Math.floor((i % 700) / 100);
    v += 1.5 * (r() - 0.5) * 2;
    sig[i] = Math.max(0, Math.min(255, Math.round(v)));
  }
  return { sig, edges: [], jm: null };
}

// ---------- referee ----------
function runFamily(fam, verbose) {
  const st = EJ.ejNew({});
  const NREC = 20;
  let locked = 0, thrown = null;
  const t0 = Date.now();
  for (let k = 0; k < NREC; k++) {
    const rec = fam.special ? buildSpecial(fam, k) : buildRecord(fam, k);
    let sig = rec.sig;
    if (fam.glitch) { // sprinkle runts
      const r = makeRng(999 + k);
      for (let g = 0; g < 6; g++) { const p = Math.floor(r() * (N - 10)); for (let i = 0; i < 4; i++) sig[p + i] = 128; }
    }
    try {
      if (EJ.ejFeed(st, sig, N, SAMPLE_S).startsWith("locked")) locked++;
    } catch (err) { thrown = err; break; }
  }
  const ms = Date.now() - t0;
  const fails = [];
  if (thrown) fails.push("THROWS: " + thrown.message);
  let res = null;
  try { res = EJ.ejResult(st); } catch (err) { fails.push("RESULT-THROWS: " + err.message); }
  const stats = { locked, rejected: st.rejected, ms };
  if (res && locked > 0) {
    stats.ui = +res.ui.toFixed(2);
    stats.tieRmsNs = res.tieRms ? +(res.tieRms * 1e9).toFixed(3) : 0;
    stats.djNs = res.dj ? +(res.dj * 1e9).toFixed(2) : 0;
    stats.rjNs = res.rj ? +(res.rj * 1e9).toFixed(3) : 0;
    stats.pkHz = res.specPeakHz ? +res.specPeakHz.toFixed(0) : 0;
    stats.pkNs = res.specPeakAmp ? +(res.specPeakAmp * 1e9).toFixed(2) : 0;
    stats.eyeH = res.eyeMetrics ? +res.eyeMetrics.eyeHeightCodes.toFixed(0) : null;
  }
  if (!thrown) {
    if (fam.expect === "reject") {
      if (locked > 0) fails.push(`locked-on-nonserial x${locked}`);
    } else if (fam.expect === "lockOnly" || fam.expect === "rejectOrLock") {
      if (locked > 0 && res && res.dj > 1e-9 && fam.jitter === "none" && !fam.special)
        fails.push(`fabricated-dj ${(res.dj * 1e9).toFixed(1)}ns`);
      if (fam.expect === "lockOnly" && fam.stream === "clock" && fam.jitter === "none" && locked === 0)
        fails.push("clock-did-not-lock");
      // sine-analog etc: locking as a clean clock is fine; fabricating jitter is not
      if (locked > 0 && res && fam.special && res.dj > 1e-9) fails.push(`fabricated-dj-on-${fam.special}`);
    } else { // measure
      const jm = jitterModel(fam.jitter, fam.ui, makeRng(1), fam);
      if (locked < NREC * 0.6) fails.push(`poor-lock ${locked}/${NREC}`);
      // per-record CDR high-pass corner: jitter tones slower than ~1.5/T_rec
      // are (partially) absorbed by the linear fit — quantitative TIE/DJ/tone
      // asserts only apply ABOVE the corner (the engine reports tieHpHz).
      const cornerHz = res && res.tieHpHz ? res.tieHpHz : 1 / (N * SAMPLE_S);
      const toneBelowCorner = jm && jm.tone && jm.tone.fHz < 1.5 * cornerHz;
      if (res && locked >= NREC * 0.6) {
        // UI truth
        if (Math.abs(res.ui - fam.ui) > 0.01 * fam.ui + 0.05) fails.push(`ui-off ${res.ui.toFixed(2)} vs ${fam.ui}`);
        // TIE rms truth (when analytic; not below the CDR corner)
        if (jm && jm.rmsS > 0 && !toneBelowCorner) {
          const floor = 0.3e-9 + fam.noise / (2 * fam.amp / fam.rise) * SAMPLE_S; // edge-noise floor
          const exp = Math.sqrt(jm.rmsS ** 2 + floor ** 2);
          if (res.tieRms < 0.6 * exp || res.tieRms > 1.6 * exp + 0.3e-9)
            fails.push(`tie-rms-off ${(res.tieRms * 1e9).toFixed(2)} vs ~${(exp * 1e9).toFixed(2)}ns`);
        }
        // DJ truth for square/dcd (not below the CDR corner)
        if (jm && jm.djS > 0 && jm.djS > 3 * (res.rj || 0) && !toneBelowCorner) {
          if (!res.dj || Math.abs(res.dj - jm.djS) > 0.3 * jm.djS)
            fails.push(`dj-off ${((res.dj || 0) * 1e9).toFixed(2)} vs ${(jm.djS * 1e9).toFixed(2)}ns`);
        }
        // no fabricated DJ on clean/gauss (except AM->PM conversion families:
        // amplitude modulation at a fixed mid threshold genuinely moves the
        // crossings — every threshold-based instrument shows it)
        if (jm && jm.djS === 0 && !fam.amPm && res.dj > Math.max(1e-9, 1.5 * (jm.rjS || 0)))
          fails.push(`fabricated-dj ${(res.dj * 1e9).toFixed(2)}ns`);
        // spectral tone truth
        if (jm && jm.tone && res.specPeakHz && !toneBelowCorner) {
          const spanHz = res.specDf * (st.fftN / 2);
          if (jm.tone.fHz > 2.5 * res.specDf && jm.tone.fHz < 0.9 * spanHz) {
            if (Math.abs(res.specPeakHz - jm.tone.fHz) > 3 * res.specDf)
              fails.push(`tone-freq-off ${(res.specPeakHz / 1e3).toFixed(1)} vs ${(jm.tone.fHz / 1e3).toFixed(1)}kHz`);
            else if (res.specPeakAmp < 0.45 * jm.tone.ampS || res.specPeakAmp > 1.8 * jm.tone.ampS)
              fails.push(`tone-amp-off ${(res.specPeakAmp * 1e9).toFixed(2)} vs ${(jm.tone.ampS * 1e9).toFixed(2)}ns`);
          }
        }
      }
    }
  }
  if (ms > 4000) fails.push(`slow ${ms}ms`);
  const line = `fam ${String(fam.fi).padStart(2)} ${fam.name.padEnd(24)} ` +
    (fails.length ? `FAIL [${fails.join("; ")}] ` : "pass ") + JSON.stringify(stats);
  if (verbose || fails.length) console.log(line);
  return { fam: fam.fi, name: fam.name, fails, stats };
}

// ---------- main ----------
const args = process.argv.slice(2);
const verbose = args.includes("-v");
const lo = +args[0] >= 0 ? +args[0] : 0;
const F = families();
const hi = +args[1] >= 1 ? Math.min(+args[1], F.length) : F.length;
console.log(`${F.length} families defined, running ${lo}..${hi - 1}`);
const all = [];
for (let i = lo; i < hi; i++) all.push(runFamily(F[i], verbose));
const failed = all.filter(r => r.fails.length);
const byReason = {};
for (const r of failed) for (const f of r.fails) { const k = f.split(" ")[0]; byReason[k] = (byReason[k] || 0) + 1; }
console.log(`\n=== ${all.length} families, ${failed.length} FAIL ===`);
console.log("by reason:", JSON.stringify(byReason));
const ji = args.indexOf("--json");
if (ji >= 0 && args[ji + 1]) require("fs").writeFileSync(args[ji + 1], JSON.stringify(all, null, 1));
process.exit(failed.length ? 1 : 0);
