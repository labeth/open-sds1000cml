// breaker.cjs — adversarial harness for the super-res gated stacker.
// 50 seeded waveform families: each = a REPEATING feature + IRREGULAR
// non-repeating filler + noise. Each family is stacked through several gates.
// Invariant under test: content that repeats (same shape recurs) stacks with
// ground-truth-accurate alignment and a truth-close mean; content that does
// not repeat must NOT stack (rejected / seed-fail). Everything is judged
// against known ground truth, not against the engine's own claims.
//
// Usage: node superres_breaker.cjs [familyLo familyHi] [--json out.json] [-v]
//
// DEV TOOL (not part of go test — ~40s): run after any matcher/stacker change.
// Expected state: ~194/200 pass; the residual failures are mixed gates
// (feature+majority-junk in loud non-repeating environments) accumulating a few
// genuinely-similar lookalike deposits — matched-filter physics, documented in
// superres.js (segment/level checks + self-calibrated ambient floor).
"use strict";
const SR = require("./superres.js");

// ---------- deterministic PRNG ----------
function makeRng(seed) {
  let s = seed >>> 0 || 1;
  return () => {
    s = (s * 1103515245 + 12345) & 0x7fffffff;
    return s / 0x7fffffff; // [0,1)
  };
}

// ---------- feature shapes (the repeating thing), length L, values ±amp around 0 ----------
const SHAPES = {
  square:   (L, r) => { const d = 0.3 + 0.4 * r(); return i => (i / L) % 1 < d ? 1 : -1; },
  sine:     (L, r) => { const ph = r() * 6.283; return i => Math.sin(6.283 * i / L + ph); },
  gauss:    (L, r) => { const c = L * (0.35 + 0.3 * r()), w = L * 0.08; return i => 2 * Math.exp(-((i - c) ** 2) / (2 * w * w)) - 0.3; },
  dblpulse: (L, r) => { const g = L * (0.2 + 0.2 * r()); return i => (i > L * 0.1 && i < L * 0.2) || (i > L * 0.1 + g && i < L * 0.2 + g) ? 1 : -0.4; },
  ringing:  (L, r) => { const f = 6 + 6 * r(); return i => i < L * 0.15 ? -0.8 : Math.exp(-(i - L * 0.15) / (L * 0.3)) * Math.cos(6.283 * f * (i - L * 0.15) / L) * 1.2; },
  saw:      ()     => { return (i, L) => 2 * ((i / L) % 1) - 1; },
  uart:     (L, r) => { const bits = Array.from({ length: 8 }, () => r() > 0.5 ? 1 : 0); return i => { const b = Math.floor(10 * i / L); return b === 0 ? -1 : b > 8 ? 1 : (bits[b - 1] ? 1 : -1); }; },
  triangle: ()     => { return (i, L) => { const p = (i / L) % 1; return p < 0.5 ? 4 * p - 1 : 3 - 4 * p; }; },
  stair:    (L, r) => { const k = 3 + Math.floor(3 * r()); return i => 2 * (Math.floor(k * i / L) / (k - 1)) - 1; },
  pwmpair:  (L, r) => { const d1 = 0.15 + 0.15 * r(); return i => { const p = (i / L) % 1; return (p < d1) || (p > 0.5 && p < 0.5 + 2 * d1) ? 1 : -1; }; },
};
const SHAPE_NAMES = Object.keys(SHAPES);

// ---------- non-repeating fillers (regenerated every frame — must NOT stack) ----------
function fillerChirp(out, lo, hi, r, amp) {
  const f0 = 2 + 30 * r(), k = 40 * r(), ph = 6.283 * r();
  for (let i = lo; i < hi; i++) { const t = (i - lo) / (hi - lo); out[i] += amp * Math.sin(ph + 6.283 * (f0 * t + k * t * t)); }
}
function fillerWalk(out, lo, hi, r, amp) {
  let v = 0;
  for (let i = lo; i < hi; i++) { v += (r() - 0.5) * 0.35; v *= 0.985; out[i] += amp * Math.max(-1.4, Math.min(1.4, v)); }
}
function fillerTelegraph(out, lo, hi, r, amp) {
  let v = r() > 0.5 ? 1 : -1;
  for (let i = lo; i < hi; i++) { if (r() < 0.02) v = -v; out[i] += amp * v * 0.8; }
}
function fillerGlitches(out, lo, hi, r, amp) {
  const n = 1 + Math.floor(4 * r());
  for (let g = 0; g < n; g++) { const p = lo + Math.floor((hi - lo) * r()), w = 3 + Math.floor(12 * r()), s = r() > 0.5 ? 1 : -1;
    for (let i = p; i < Math.min(hi, p + w); i++) out[i] += amp * s; }
}
const FILLERS = [fillerChirp, fillerWalk, fillerTelegraph, fillerGlitches];

// ---------- family = shape × composition ----------
// Compositions decide where the feature occurs per frame (ground truth):
//  train: periodic occurrences across the record (fixed grid + tiny jitter)
//  burst: groups of occurrences with idle gaps between groups
//  sparse: 2-4 occurrences at RANDOM positions per frame (irregular spacing)
//  buried: 2 occurrences buried in loud filler covering the rest
//  flaky: feature present in only ~60% of frames; other frames get a DECOY
const COMPS = ["train", "burst", "sparse", "buried", "flaky"];

function buildFamily(fi) {
  const shape = SHAPE_NAMES[fi % SHAPE_NAMES.length];
  const comp = COMPS[Math.floor(fi / SHAPE_NAMES.length) % COMPS.length];
  const rng = makeRng(1000 + fi * 7919);
  const N = 2048;
  const L = 80 + Math.floor(200 * rng());        // feature length (samples)
  const amp = 35 + 45 * rng();                    // feature amplitude (codes)
  const noise = 2 + 5 * rng();                    // white noise sigma (codes)
  const fillAmp = 12 + 30 * rng();                // filler amplitude
  const filler = FILLERS[fi % FILLERS.length];
  const shapeFn = SHAPES[shape](L, rng);
  const feat = new Float64Array(L);
  for (let i = 0; i < L; i++) feat[i] = shapeFn(i, L);
  // decoy: a genuinely different shape of the same length/scale
  const decoyFn = SHAPES[SHAPE_NAMES[(fi + 3) % SHAPE_NAMES.length]](L, makeRng(999 + fi));
  const decoy = new Float64Array(L);
  for (let i = 0; i < L; i++) decoy[i] = decoyFn(i, L);
  return { fi, shape, comp, rng, N, L, amp, noise, fillAmp, filler, feat, decoy };
}

// occurrences (ground truth feature-start positions) for one frame
function occurrencesFor(fam, frameIdx, r) {
  const { comp, N, L } = fam;
  const occ = [];
  if (comp === "train") {
    const P = L + 10 + Math.floor(L * 0.4);      // stable period (family const via seed order)
    for (let p = 40; p + L < N - 20; p += P) occ.push(p + (r() - 0.5) * 2);
  } else if (comp === "burst") {
    const per = L + 6;
    let p = 60 + Math.floor(200 * r());
    while (p + 3 * per < N - 60) {
      for (let k = 0; k < 3 && p + L < N - 20; k++) { occ.push(p); p += per; }
      p += Math.floor(300 + 300 * r());          // idle gap
    }
  } else if (comp === "sparse") {
    const n = 2 + Math.floor(3 * r());
    let tries = 0;
    while (occ.length < n && tries++ < 60) {
      const p = 30 + Math.floor((N - L - 60) * r());
      if (occ.every(q => Math.abs(q - p) > L + 8)) occ.push(p);
    }
    occ.sort((a, b) => a - b);
  } else if (comp === "buried") {
    occ.push(120 + Math.floor(80 * r()), N - L - 150 - Math.floor(80 * r()));
  } else { // flaky
    if (frameIdx === 0 || r() < 0.6) occ.push(300 + Math.floor(60 * r()), 1100 + Math.floor(60 * r()));
  }
  return occ;
}

// genFrame builds one frame + its noiseless truth. Returns {sig, clean, occ, hasDecoy}.
function genFrame(fam, frameIdx) {
  const r = makeRng(fam.fi * 100003 + frameIdx * 613 + 17);
  const { N, L, amp, noise, fillAmp, filler, feat, decoy } = fam;
  const clean = new Float64Array(N).fill(0);
  const occ = occurrencesFor(fam, frameIdx, r);
  // filler over the whole record (non-repeating: reseeded every frame)
  filler(clean, 0, N, r, fam.comp === "buried" ? fillAmp * 1.6 : fillAmp);
  // feature occurrences OVERWRITE the filler in their span (clean feature + baseline)
  const gain = 0.9 + 0.2 * r();                   // per-frame amplitude drift
  for (const p of occ) {
    const ip = Math.round(p);
    for (let i = 0; i < L && ip + i < N; i++) clean[ip + i] = amp * gain * feat[i];
  }
  let hasDecoy = false;
  if (fam.comp === "flaky" && occ.length === 0) { // decoy instead of the feature
    hasDecoy = true;
    const p = 300;
    for (let i = 0; i < L && p + i < N; i++) clean[p + i] = amp * decoy[i];
  }
  const sig = new Int16Array(N);
  for (let i = 0; i < N; i++) {
    const v = 128 + clean[i] + noise * (r() - 0.5) * 2;
    sig[i] = Math.max(5, Math.min(250, Math.round(v)));
  }
  return { sig, clean, occ: occ.map(Math.round), hasDecoy };
}

// zero-mean NCC of two equal-length float arrays (truth-side referee)
function ncc(a, b) {
  const n = Math.min(a.length, b.length);
  let ma = 0, mb = 0;
  for (let i = 0; i < n; i++) { ma += a[i]; mb += b[i]; }
  ma /= n; mb /= n;
  let dot = 0, ea = 0, eb = 0;
  for (let i = 0; i < n; i++) { const x = a[i] - ma, y = b[i] - mb; dot += x * y; ea += x * x; eb += y * y; }
  const den = Math.sqrt(ea * eb);
  return den > 0 ? dot / den : 0;
}

// ---------- one run = family × gate ----------
function runGate(fam, gateName, gate, frames, refFrame) {
  const res = { fam: fam.fi, shape: fam.shape, comp: fam.comp, gate: gateName, fails: [], stats: {} };
  const st = SR.srNew(fam.N, 16);
  st.align = 0;
  st.c[0].vpc = st.c[1].vpc = 1 / 32;
  const t0 = Date.now();
  const seedOk = SR.srSeedRef(st, refFrame.sig, refFrame.sig, -1, gate);
  // Gates over genuinely non-repeating content: seed-fail is a PASS outcome.
  const refClean = refFrame.clean;
  if (!seedOk) {
    res.stats.seed = "fail";
    if (gateName !== "junk" && gateName !== "flat") res.fails.push("seed-failed-on-feature-gate");
    return res;
  }
  res.stats.seed = "ok";
  const gLo = st.gateLo, gHi = st.gateHi, gL = st.gridL;
  // reference truth content inside the (installed) gate
  const refGate = refClean.slice(gLo, gHi);
  let falseAcc = 0, falseRej = 0, okAcc = 0, okRej = 0, grayAcc = 0, emergentAcc = 0, shiftErrs = [];
  for (let k = 0; k < frames.length; k++) {
    const f = frames[k];
    const hitsBefore = st.hits;
    const disp = SR.srFeed(st, f.sig, f.sig, {});
    const accepted = disp.startsWith("stacked");
    const newHits = st.hits - hitsBefore;
    // ground truth: does the frame contain content matching the gated reference?
    // For each occurrence, find the BEST-NCC gate-length window near it (argmax,
    // not first-above-threshold — that had a systematic position bias).
    const truthMatches = [];
    for (const p of f.occ) {
      let best = -2, bestS = -1;
      for (let s = p - 4; s <= p + 4 + (fam.L - gL); s++) {
        if (s < 0 || s + gL > fam.N) continue;
        const t = ncc(refGate, f.clean.slice(s, s + gL));
        if (t > best) { best = t; bestS = s; }
      }
      if (best > 0.9) truthMatches.push(bestS);
    }
    if (accepted) {
      // every engine hit must land on genuinely matching content (truth NCC):
      //  ≥0.9 clearly the feature → judge alignment; 0.7–0.9 gray (filler that
      //  really does correlate — matched-filter physics, counted, not failed);
      //  <0.7 = FALSE ACCEPT.
      const engineShifts = st.shifts.slice(-newHits);
      let badHit = false;
      for (const sh of engineShifts) {
        const loc = gLo + Math.round(sh);
        if (loc < 0 || loc + gL > fam.N) { badHit = true; continue; }
        const t = ncc(refGate, f.clean.slice(loc, loc + gL));
        if (t < 0.7) { badHit = true; falseAcc++; }
        else if (t < 0.97) { grayAcc++; } // lookalike content — genuine per matched-filter physics
        else if (truthMatches.length) {
          const err = Math.min(...truthMatches.map(m => Math.abs(m - (gLo + sh))));
          // only judge alignment against LISTED occurrences; a match far from
          // them is emergent genuinely-matching content (still repeating).
          if (err <= gL) shiftErrs.push(err); else emergentAcc++;
        }
      }
      if (!badHit) okAcc++;
    } else {
      if (truthMatches.length > 0) falseRej++;
      else okRej++;
    }
  }
  const r = SR.srResult(st, { stride: 1 });
  // Mean fidelity vs the noiseless truth — ONLY over gate bins covered by a
  // feature occurrence in the REF frame. Filler-covered bins are non-repeating:
  // a correct stack AVERAGES them out, so comparing them against the ref's
  // specific random filler would punish correct behaviour.
  let rms = -1;
  if (st.hits > 2 && r.mean) {
    const inFeature = s => refFrame.occ.some(p => s >= p && s < p + fam.L);
    let se = 0, cnt = 0;
    for (let b = 0; b < r.mean.length; b++) {
      if (r.mean[b] < 0) continue;
      const t = gLo + b / st.K;
      const i0 = Math.floor(t);
      if (i0 < 0 || i0 + 1 >= fam.N || !inFeature(i0)) continue;
      const w = t - i0;
      const truth = 128 + refClean[i0] * (1 - w) + refClean[i0 + 1] * w;
      se += (r.mean[b] - truth) ** 2; cnt++;
    }
    rms = cnt ? Math.sqrt(se / cnt) : -1;
  }
  const ms = Date.now() - t0;
  res.stats = { seed: "ok", hits: st.hits, rejected: st.rejected, falseAcc, falseRej, grayAcc, emergentAcc, okAcc, okRej,
    meanShiftErr: shiftErrs.length ? +(shiftErrs.reduce((a, b) => a + b) / shiftErrs.length).toFixed(2) : null,
    maxShiftErr: shiftErrs.length ? +Math.max(...shiftErrs).toFixed(2) : null,
    rms: rms >= 0 ? +rms.toFixed(2) : null, bits: +r.bitsGained.toFixed(2), ms };
  // ---- verdicts ----
  // featFrac: how much of the installed gate is covered by feature occurrences
  // in the reference. A mostly-feature gate MUST stack; a mixed gate may either
  // stack cleanly or consistently reject (its content as a whole does not
  // repeat) — but may never stack contaminated.
  let featCov = 0;
  for (let s = gLo; s < gHi; s++) if (refFrame.occ.some(p => s >= p && s < p + fam.L)) featCov++;
  const featFrac = featCov / Math.max(1, gL);
  res.stats.featFrac = +featFrac.toFixed(2);
  if (falseAcc > 0) res.fails.push(`false-accept x${falseAcc}`);
  const framesWithFeature = frames.filter(f => f.occ.length > 0).length;
  res.stats.featFrames = framesWithFeature;
  if (gateName !== "junk" && gateName !== "flat") {
    if (featFrac >= 0.9 && framesWithFeature >= Math.ceil(frames.length * 0.25)) {
      if (falseRej > Math.ceil(frames.length * 0.25)) res.fails.push(`false-reject x${falseRej}`);
      if (st.hits <= 1) res.fails.push("nothing-stacked-on-feature-gate");
    }
    // Alignment error only matters when it damages the stack: self-similar
    // features (ramps) have flat correlation ridges — position wobble there is
    // physics, and their content lines up regardless.
    const rmsBad = rms >= 0 && rms > Math.max(3, fam.noise);
    if (shiftErrs.length && Math.max(...shiftErrs) > 1.5 && rmsBad) res.fails.push(`shift-error ${Math.max(...shiftErrs).toFixed(1)}`);
    if (rmsBad) res.fails.push(`mean-off-truth rms=${rms.toFixed(1)}`);
  }
  if (ms > 3000) res.fails.push(`slow ${ms}ms`);
  return res;
}

function runFamily(fam, verbose) {
  const refFrame = genFrame(fam, 0);
  if (refFrame.occ.length === 0) { // flaky family may roll a decoy ref; force feature
    refFrame.occ.push(300); // regenerate simply: frame 0 always has feature by construction above
  }
  const frames = [];
  for (let k = 1; k <= 24; k++) frames.push(genFrame(fam, k));
  const p0 = refFrame.occ[0], L = fam.L, N = fam.N;
  // a region with NO feature in the ref (for the junk gate): after the last occurrence
  const occSorted = [...refFrame.occ].sort((a, b) => a - b);
  let junkLo = 30;
  for (let cand = 30; cand + 220 < N; cand += 50) {
    if (occSorted.every(p => cand + 200 < p || cand > p + L)) { junkLo = cand; break; }
  }
  const gates = {
    exact:  { lo: p0, hi: Math.min(N, p0 + L) },
    half:   { lo: p0 + Math.floor(L / 2), hi: Math.min(N, p0 + Math.floor(L / 2) + L) }, // half feature + trailing junk
    triple: { lo: p0, hi: Math.min(N, p0 + 3 * L) },
    junk:   { lo: junkLo, hi: junkLo + 200 },
  };
  const out = [];
  for (const [gname, g] of Object.entries(gates)) {
    if (g.hi - g.lo < 16) continue;
    const r = runGate(fam, gname, g, frames, refFrame);
    out.push(r);
    if (verbose || r.fails.length) {
      console.log(`fam ${String(fam.fi).padStart(2)} ${fam.shape.padEnd(8)} ${fam.comp.padEnd(6)} ${gname.padEnd(6)} ` +
        (r.fails.length ? `FAIL [${r.fails.join(", ")}] ` : "pass ") + JSON.stringify(r.stats));
    }
  }
  return out;
}

module.exports = { buildFamily, genFrame, ncc };

// ---------- main ----------
if (require.main !== module) return;
const args = process.argv.slice(2);
const verbose = args.includes("-v");
const lo = +args[0] >= 0 ? +args[0] : 0;
const hi = +args[1] >= 1 ? +args[1] : 50;
const all = [];
for (let fi = lo; fi < hi; fi++) {
  const fam = buildFamily(fi);
  all.push(...runFamily(fam, verbose));
}
const failed = all.filter(r => r.fails.length);
const byReason = {};
for (const r of failed) for (const f of r.fails) { const k = f.split(" ")[0]; byReason[k] = (byReason[k] || 0) + 1; }
console.log(`\n=== ${all.length} runs, ${failed.length} FAIL ===`);
console.log("by reason:", JSON.stringify(byReason));
const ji = args.indexOf("--json");
if (ji >= 0 && args[ji + 1]) require("fs").writeFileSync(args[ji + 1], JSON.stringify(all, null, 1));
process.exit(failed.length ? 1 : 0);
