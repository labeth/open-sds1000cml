// superres_gate.js — gate detection, install, drizzle-hit, multi-hit find/feed.

// srDetectPeriod returns the fundamental period (samples) of ref[lo:hi] via
// normalized autocorrelation — the first local peak above 0.5 — or 0 if the
// window isn't clearly periodic. Used to narrow the auto-gate to ONE period so
// a repetitive waveform stacks every cycle (multi-hit) instead of once.
// node: pull the sibling modules onto globalThis so bare calls resolve
// (the browser loads them as classic scripts before this one — same globals).
if (typeof require !== "undefined") { Object.assign(globalThis, require("./superres_math.js"), require("./superres_template.js")); }

function srDetectPeriod(ref, lo, hi) {
  const W = hi - lo;
  if (W < 32) return 0;
  let mean = 0;
  for (let i = lo; i < hi; i++) mean += ref[i];
  mean /= W;
  const x = new Float64Array(W);
  for (let i = 0; i < W; i++) x[i] = ref[lo + i] - mean;
  const minLag = 8, maxLag = W >> 1;
  let prev = -2, rising = false, dipped = false;
  for (let lag = minLag; lag <= maxLag; lag++) {
    let dot = 0, ea = 0, eb = 0;
    const m = W - lag;
    for (let i = 0; i < m; i++) {
      const a = x[i], b = x[i + lag];
      const p = a * b; dot += p;
      const qa = a * a; ea += qa;
      const qb = b * b; eb += qb;
    }
    const den = Math.sqrt(ea * eb);
    const r = den > 0 ? dot / den : 0;
    // A SQUARE stays highly self-correlated across its flat tops, so the raw
    // "first peak > 0.5" fires at lag ~8 (the main lobe) → a bogus tiny period.
    // Require the autocorrelation to DIP below 0.3 first (proof we crossed a
    // half-period); the first strong peak AFTER that is the true fundamental.
    if (r < 0.3) dipped = true;
    if (dipped && rising && r < prev && prev > 0.5) return lag - 1;
    rising = r > prev;
    prev = r;
  }
  return 0;
}

// srGateInstall resizes the stack to an L*K gate grid, builds the gate template,
// and seeds the reference's own gate at offset 0. gate = [gLo, gHi).
function srGateInstall(st, gLo, gHi) {
  const L = gHi - gLo;
  st.gated = true; st.userRef = true;
  st.gateLo = gLo; st.gateHi = gHi; st.gridL = L;
  st.nbins = L * st.K;
  st.statLo = 0; st.statHi = L;
  st.hits = 0;
  for (let ch = 0; ch < 2; ch++) {
    const C = st.c[ch];
    C.sum = new Float64Array(st.nbins); C.sum2 = new Float64Array(st.nbins);
    C.cnt = new Float64Array(st.nbins); C.sumA = new Float64Array(st.nbins); C.cntA = new Float64Array(st.nbins);
  }
  st.gtpl = srGateTemplate(st.c[st.align].ref, gLo, gHi);
  // SELF-CALIBRATED floor: scan the reference's own record off-gate. Ambient
  // content that already resembles the gate (filler humps against a smooth
  // single-bump template — a LOW-INFORMATION template) sets how selective the
  // matcher must be: the floor sits above the strongest ambient lookalike, so
  // junk that merely resembles the feature can't stack. Matches ≥0.93 in the
  // reference are taken to be the feature itself (periodic repeats) and do NOT
  // raise the floor — a repetitive signal keeps multi-hitting at the base floor.
  if (st.gtpl) {
    const amb = srAmbientMax(st, st.c[st.align].ref);
    const base = st.minMatch || 0.8;
    st.adaptFloor = Math.max(base, Math.min(0.92, amb + 0.06));
  }
  // Seed: drizzle each channel's own reference gate at fractional offset 0.
  st.hits = 1;
  for (let ch = 0; ch < 2; ch++) if (st.c[ch].ref) srDrizzleHit(st, ch, st.c[ch].ref, gLo, true);
  st.frames = 1;
  st.scores = [1]; st.shifts = [0];
  return !!st.gtpl;
}

// srDrizzleHit stacks one aligned occurrence onto the L*K grid by INTERP-
// resampling — the SAME full-quality kernel srAccumCh uses (interp default,
// cubic option, edge fallback): grid bin b reads the frame at sub-sample
// position p + b/K. Every fine bin gets a contribution from every hit, so the
// stack is gap-free and staircase-free — a deposit kernel instead leaves the
// between-sample bins empty when occurrences share a sub-sample phase (a
// periodic signal at ~integer samples/cycle). `odd` → the A half-stack.
function srDrizzleHit(st, ch, sig, p, odd) {
  const K = st.K, G = st.nbins, n = st.n;
  const C = st.c[ch];
  const sum = C.sum, sum2 = C.sum2, cnt = C.cnt, sumA = C.sumA, cntA = C.cntA;
  if (st.kernel === "drizzle") {
    // DEPOSIT real samples at their sub-sample grid position — preserves
    // near-Nyquist content (the 250 MHz path) at a higher noise floor. Each hit
    // deposits its ~L real samples; diverse sub-sample phases across hits fill
    // the fine grid. gate-relative position = (i − p)·K.
    const iStart = Math.max(0, Math.floor(p)), iEnd = Math.min(n, Math.ceil(p + st.gridL) + 1);
    for (let i = iStart; i < iEnd; i++) {
      const pos = (i - p) * K;
      const b0 = Math.floor(pos);
      const w1 = pos - b0, w0 = 1 - w1;
      const v = sig[i];
      if (b0 >= 0 && b0 < G) { sum[b0] += w0 * v; sum2[b0] += w0 * v * v; cnt[b0] += w0; if (odd) { sumA[b0] += w0 * v; cntA[b0] += w0; } }
      const b1 = b0 + 1;
      if (w1 > 0 && b1 >= 0 && b1 < G) { sum[b1] += w1 * v; sum2[b1] += w1 * v * v; cnt[b1] += w1; if (odd) { sumA[b1] += w1 * v; cntA[b1] += w1; } }
    }
    return;
  }
  const invK = 1 / K;
  const cubic = st.kernel === "cubic";
  for (let b = 0; b < G; b++) {
    const t = p + b * invK;
    const i0 = Math.floor(t);
    if (i0 < 1 || i0 + 2 >= n) {
      if (i0 < 0 || i0 + 1 >= n) continue;
      const w = t - i0;                             // record edge: linear
      const v = sig[i0] * (1 - w) + sig[i0 + 1] * w;
      sum[b] += v; sum2[b] += v * v; cnt[b]++;
      if (odd) { sumA[b] += v; cntA[b]++; }
      continue;
    }
    const w = t - i0;
    let v;
    if (cubic) {
      const p0 = sig[i0 - 1], p1 = sig[i0], p2 = sig[i0 + 1], p3 = sig[i0 + 2];
      v = p1 + 0.5 * w * (p2 - p0 + w * (2 * p0 - 5 * p1 + 4 * p2 - p3 + w * (3 * (p1 - p2) + p3 - p0)));
    } else {
      v = sig[i0] * (1 - w) + sig[i0 + 1] * w;
    }
    sum[b] += v; sum2[b] += v * v; cnt[b]++;
    if (odd) { sumA[b] += v; cntA[b]++; }
  }
}

// srGateFind matched-filters the gate template across the frame and returns every
// occurrence {loc, score, delta}: local NCC maxima above the floor, min-separated
// by L/2, each with a parabolic sub-sample offset. R>0 bounds the search to the
// trigger-predicted position ±R; R=0 searches the whole frame (finds the feature
// wherever it sits AND catches every repeat).
function srGateFind(st, sig, base, R) {
  const t = st.gtpl;
  if (!t) return [];
  const L = t.L, data = t.data, tnorm = t.norm, n = st.n;
  let lo = 0, hi = n - L;
  if (R > 0) { const c = st.gateLo + (base | 0); lo = Math.max(0, c - R); hi = Math.min(n - L, c + R); }
  if (hi < lo) return [];
  const M = hi - lo + 1;
  const ncc = new Float64Array(M);
  for (let loc = lo; loc <= hi; loc++) {
    let mean = 0;
    for (let i = 0; i < L; i++) mean += sig[loc + i];
    mean /= L;
    let dot = 0, ss = 0;
    for (let i = 0; i < L; i++) { const s = sig[loc + i] - mean; const p = data[i] * s; dot += p; const q = s * s; ss += q; }
    const den = tnorm * Math.sqrt(ss);
    ncc[loc - lo] = den > 0 ? dot / den : 0;
  }
  const floor = st.adaptFloor || st.minMatch || 0.8;
  const minSep = Math.max(1, L >> 1);
  const hits = [];
  let lastLoc = -1e9;
  for (let k = 0; k < M; k++) {
    const sc = ncc[k];
    if (sc < floor) continue;
    if (k > 0 && ncc[k - 1] >= sc) continue;      // strictly greater than left
    if (k + 1 < M && ncc[k + 1] > sc) continue;    // and ≥ right (plateau → first)
    const loc = lo + k;
    if (loc - lastLoc < minSep) continue;
    if (!srSegMatch(t, sig, loc)) continue;        // partial/mixed match → not a hit
    let delta = 0;
    if (k > 0 && k + 1 < M) {
      const yl = ncc[k - 1], y0 = sc, yr = ncc[k + 1];
      const den2 = yl - 2 * y0 + yr;
      if (den2 < 0) { delta = 0.5 * (yl - yr) / den2; if (delta > 0.5) delta = 0.5; else if (delta < -0.5) delta = -0.5; }
    }
    hits.push({ loc, score: sc, delta });
    lastLoc = loc;
  }
  return hits;
}

// srGateFeed is the gated multi-hit pipeline for one frame: find every gate
// occurrence, then sub-sample align + drift-normalize + drizzle each onto the
// L*K grid (both channels at the align channel's positions). Zero occurrences →
// the frame is rejected. Returns "stacked:<nhits>" | "rejected:<why>".
function srGateFeed(st, sig1, sig2, opts) {
  opts = opts || {};
  const sigs = [sig1, sig2];
  const alignSig = sigs[st.align];
  if (!alignSig || alignSig.length < st.n) return "rejected:short";
  if (srClipped(alignSig)) { st.clipped++; st.rejected++; return "rejected:clip"; }
  let base = 0;
  if (st.refEdgeX >= 0 && opts.edgeX != null && opts.edgeX >= 0) base = Math.round(opts.edgeX - st.refEdgeX);
  // Whole-frame multi-hit is O((N−L)·L). A one-period gate searches the whole
  // frame cheaply; bound a wide aperiodic gate to trigger-predicted ±R (it occurs
  // once per frame at the aligned position) so it never becomes seconds/frame.
  let R = opts.maxShift != null ? opts.maxShift : (st.searchR || 0);
  if (R === 0) {
    const L = st.gtpl.L, maxWork = 12000000;
    if ((st.n - L) * L > maxWork) { R = Math.floor(maxWork / (2 * L)); if (R < 64) R = 64; }
  }
  const hits = srGateFind(st, alignSig, base, R);
  if (!hits.length) { st.rejected++; return "rejected:nomatch"; }
  for (let hIdx = 0; hIdx < hits.length; hIdx++) {
    const h = hits[hIdx];
    const p = h.loc + h.delta;
    const lag = h.loc - st.gateLo;
    st.hits++;
    const odd = (st.hits & 1) === 1;
    for (let ch = 0; ch < 2; ch++) {
      let s = sigs[ch];
      if (!s || !(s.length >= st.n) || !st.c[ch].ref) continue;
      if (ch !== st.align && srClipped(s)) { st.c[ch].clipSkips++; continue; }
      if (opts.normalize !== false) {
        const { g, b } = srGainOffset(st.c[ch].ref, s, lag, st.gateLo, st.gateHi);
        if (g !== 1 || b !== 0) {
          const f = new Float32Array(st.n);
          for (let i = 0; i < st.n; i++) { const v = (s[i] - b) / g; f[i] = v < 0 ? 0 : v; }
          s = f;
        }
      }
      srDrizzleHit(st, ch, s, p, odd);
    }
    st.scores.push(h.score);
    st.shifts.push(lag + h.delta);
  }
  st.frames++;
  return "stacked:" + hits.length;
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { srDetectPeriod, srGateInstall, srDrizzleHit, srGateFind, srGateFeed };
}
