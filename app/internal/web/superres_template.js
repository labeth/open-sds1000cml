// superres_template.js — reference/gate template build + matching.

// srFeed runs the full per-frame pipeline: align → lucky-select → drift
// normalize → drizzle, for BOTH channels (they are sampled simultaneously,
// so the align channel's shift serves both; each channel gets its own drift
// fit against its own reference). sig2 may be null (single-channel feed).
// Returns a disposition string for the UI ("stacked" | "rejected:<why>").
// srBuildTemplate isolates the frozen reference's DISTINGUISHING content — a
// zero-mean template the locate matched-filters against every frame. GENERAL
// PURPOSE: the "signal" is whatever you froze (a burst, a specific UART byte
// pattern, a glitch, a slow ramp — anything). It is the reference's active
// region AFTER the trigger transition: the trigger edge is the ONE feature every
// triggered frame shares (it fired on the same level-crossing), so it carries no
// match information and is excluded — otherwise a plain NCC over it false-accepts
// everything. The active span is trimmed to where the reference deviates from its
// pre-trigger flat baseline (deviation, not high-frequency energy, so a slow
// reference is kept too). NCC (mean- and scale-invariant) then matches shape.
"use strict";

function srBuildTemplate(ref, n, edgeX, valid) {
  const hi0 = valid > 0 && valid <= n ? valid : n;
  const lo0 = edgeX >= 0 ? Math.min(hi0 - 1, Math.round(edgeX) + 16) : 0; // skip the shared trigger transition
  if (hi0 - lo0 < 16) return null;
  // Local variation = moving range over a short window. The active region is
  // where it rises above the flat-region noise floor — this trims constant/idle
  // stretches at ANY level (so a burst packet sitting at a variable delay after
  // the trigger is isolated and found; UART bytes, a ramp, a glitch likewise),
  // while keeping genuinely varying content. General, no signal-type assumption.
  const W = 12, h = W >> 1;
  const mr = new Float64Array(hi0);
  for (let i = lo0; i < hi0; i++) {
    let mn = 255, mx = 0;
    const a = i - h < lo0 ? lo0 : i - h, b = i + h + 1 > hi0 ? hi0 : i + h + 1;
    for (let j = a; j < b; j++) { const v = ref[j]; if (v < mn) mn = v; if (v > mx) mx = v; }
    mr[i] = mx - mn;
  }
  const sorted = Array.from(mr.subarray(lo0, hi0)).sort((a, b) => a - b);
  const floor = sorted[Math.floor(sorted.length * 0.2)] || 0;
  const peak = sorted[sorted.length - 1] || 0;
  if (peak - floor < 6) return null; // no distinguishing variation
  const thr = floor + Math.max(4, 0.2 * (peak - floor));
  let lo = -1, hi = -1;
  for (let i = lo0; i < hi0; i++) {
    if (mr[i] < thr) continue;
    if (lo < 0) lo = i;
    hi = i;
  }
  if (lo < 0 || hi - lo < 8) { lo = lo0; hi = hi0 - 1; } // fall back to the whole post-edge region
  else { lo = lo - h < lo0 ? lo0 : lo - h; hi = hi + h >= hi0 ? hi0 - 1 : hi + h; } // pad, don't clip a feature
  const L = hi - lo + 1;
  const data = new Float64Array(L);
  let mean = 0;
  for (let i = 0; i < L; i++) mean += ref[lo + i];
  mean /= L;
  let ss = 0;
  for (let i = 0; i < L; i++) { data[i] = ref[lo + i] - mean; ss += data[i] * data[i]; }
  const norm = Math.sqrt(ss);
  if (!(norm > 0)) return null;
  return { data, lo, hi, L, norm };
}

// srMatchLocate slides the reference's distinguishing TEMPLATE (srBuildTemplate)
// over the high-passed frame and returns the best sub-window match: {shift, score,
// ambig}. score = normalized correlation at the best location; ambig = a rival
// peak (≥0.9·best, well separated) exists → the pattern is period-ambiguous or
// absent, reject. shift aligns the frame's found pattern back onto the reference's
// (so srAccumCh stacks the matched content, wherever it sat vs the trigger). This
// is R4 (reject non-matches) + R5 (find a pattern displaced from the trigger).
function srMatchLocate(st, sig, base, R) {
  const t = st.tpl;
  if (!t) return null;
  const L = t.L, data = t.data, tnorm = t.norm;
  base = base | 0;
  // Search the template's expected position (trigger-predicted `base`) ± R, NOT
  // the whole record: an unbounded search lets a DIFFERENT pattern reach for a
  // favorable far partial-overlap and false-accept. R is the translation budget —
  // how far the pattern may sit from where the trigger predicts (R5).
  const center = t.lo + base;
  const lo = Math.max(0, center - R), hi = Math.min(st.n - L, center + R);
  if (hi < lo) return null;
  let best = -2, bestLoc = center, second = -2;
  for (let loc = lo; loc <= hi; loc++) {
    let mean = 0;
    for (let i = 0; i < L; i++) mean += sig[loc + i];
    mean /= L;
    let dot = 0, ss = 0;
    for (let i = 0; i < L; i++) { const s = sig[loc + i] - mean; dot += data[i] * s; ss += s * s; }
    const den = tnorm * Math.sqrt(ss);
    const sc = den > 0 ? dot / den : 0;
    if (sc > best) { if (Math.abs(loc - bestLoc) > (L >> 1)) second = best; best = sc; bestLoc = loc; }
    else if (sc > second && Math.abs(loc - bestLoc) > (L >> 1)) second = sc;
  }
  return { shift: bestLoc - t.lo, score: best, ambig: second > 0.9 * best };
}

// srGateTemplate builds the zero-mean, unit-referenced matched filter for the
// gate window [lo,hi) of the reference. Returns null if the window is flat.
// It also precomputes SEGMENT sub-templates (each segment re-zero-meaned with
// its own norm + share of the total energy) for the per-hit consistency check:
// a genuine occurrence matches the template EVERYWHERE it has energy, whereas a
// partial overlap / lookalike matches only where the energy is concentrated —
// the global energy-weighted NCC cannot tell those apart.
function srGateTemplate(ref, lo, hi) {
  const L = hi - lo;
  if (L < 4) return null;
  const data = new Float64Array(L);
  let mean = 0;
  for (let i = 0; i < L; i++) mean += ref[lo + i];
  mean /= L;
  let ss = 0;
  for (let i = 0; i < L; i++) { const d = ref[lo + i] - mean; data[i] = d; const p = d * d; ss += p; }
  const norm = Math.sqrt(ss);
  if (!(norm > 0)) return null;
  // Segments: ~8 across the gate, each ≥24 samples (shorter is too noisy to judge).
  // relMean = the segment's raw level RELATIVE to the window mean: dead (flat)
  // segments have no shape to correlate but their LEVEL still discriminates — a
  // lookalike whose extra pulse sits in the template's flat plateau shifts it.
  const nseg = Math.max(1, Math.min(8, Math.floor(L / 24)));
  const segs = [];
  for (let s = 0; s < nseg; s++) {
    const a = Math.floor(s * L / nseg), b = Math.floor((s + 1) * L / nseg);
    const sl = b - a;
    if (sl < 8) continue;
    let sm = 0;
    for (let i = a; i < b; i++) sm += ref[lo + i];
    sm /= sl;
    const sdata = new Float64Array(sl);
    let sss = 0;
    for (let i = 0; i < sl; i++) { const d = ref[lo + a + i] - sm; sdata[i] = d; const p = d * d; sss += p; }
    segs.push({ a, len: sl, data: sdata, norm: Math.sqrt(sss), share: sss / (ss || 1), relMean: sm - mean });
  }
  return { data, L, norm, rms: Math.sqrt(ss / L), segs };
}

// srSegMatch verifies one candidate hit against the template's energetic
// segments: every segment carrying ≥8% of the template energy must correlate
// ≥0.5 with the frame content it lands on. Returns false for partial overlaps
// (the non-overlapping segment fails) and mixed feature+junk windows (the junk
// segment fails), which the global NCC accepts whenever the matching part
// carries the energy — the root cause of stack contamination.
function srSegMatch(t, sig, loc) {
  const segs = t.segs;
  if (!segs || segs.length < 2) return true; // nothing to cross-check
  const L = t.L;
  // window mean + rms → gain estimate for the level check
  let wm = 0;
  for (let i = 0; i < L; i++) wm += sig[loc + i];
  wm /= L;
  let wss = 0;
  for (let i = 0; i < L; i++) { const d = sig[loc + i] - wm; const p = d * d; wss += p; }
  let gGain = t.rms > 0 ? Math.sqrt(wss / L) / t.rms : 1;
  if (gGain < 0.5) gGain = 0.5; else if (gGain > 2) gGain = 2;
  const lvlTol = Math.max(5, 0.25 * gGain * t.rms);
  for (const g of segs) {
    let sm = 0;
    for (let i = 0; i < g.len; i++) sm += sig[loc + g.a + i];
    sm /= g.len;
    // LEVEL check (all segments, dead ones especially): the segment must sit at
    // the template's level relative to the window mean, gain-scaled. Catches
    // lookalikes whose differences live in the template's flat plateaus, where
    // the shape check below has nothing to correlate against.
    if (Math.abs((sm - wm) - gGain * g.relMean) > lvlTol) return false;
    // SHAPE check: skip only truly dead segments (constant in the reference).
    // Even a few-%-share segment discriminates: genuine hits match ~0.99 there,
    // impostors ~0 (measured on the adversarial corpus).
    if (g.share < 0.02) continue;
    let dot = 0, ss = 0;
    for (let i = 0; i < g.len; i++) {
      const s = sig[loc + g.a + i] - sm;
      const p = g.data[i] * s; dot += p;
      const q = s * s; ss += q;
    }
    const den = g.norm * Math.sqrt(ss);
    if (!(den > 0) || dot / den < 0.65) return false;
  }
  return true;
}

// srAmbientMax measures the reference record's own ambient similarity to the
// gate template: the maximum off-gate local-maximum NCC BELOW 0.93 (values at
// or above that are genuine periodic repeats of the feature, not lookalikes).
function srAmbientMax(st, ref) {
  const t = st.gtpl, L = t.L, n = st.n;
  let best = 0, prev = -2, prev2 = -2;
  for (let loc = 0; loc <= n - L; loc++) {
    let mean = 0;
    for (let i = 0; i < L; i++) mean += ref[loc + i];
    mean /= L;
    let dot = 0, ss = 0;
    for (let i = 0; i < L; i++) { const s = ref[loc + i] - mean; const p = t.data[i] * s; dot += p; const q = s * s; ss += q; }
    const den = t.norm * Math.sqrt(ss);
    const sc = den > 0 ? dot / den : 0;
    // local maximum at the PREVIOUS position, outside the gate's own span
    if (prev > prev2 && prev >= sc) {
      const ploc = loc - 1;
      if (Math.abs(ploc - st.gateLo) >= (L >> 1) && prev < 0.93 && prev > best) best = prev;
    }
    prev2 = prev; prev = sc;
  }
  return best;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { srBuildTemplate, srMatchLocate, srGateTemplate, srSegMatch, srAmbientMax };
}
