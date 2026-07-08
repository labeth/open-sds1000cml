

// superres.js — stack-and-crunch super-resolution for a repetitive waveform.
//
// Many raw acquisitions of the same repeating signal are combined into one
// waveform with more vertical resolution and a finer time grid, the way
// astro stackers align+drizzle+stack frames:
//   1. ALIGN  — each frame is aligned to a reference with sub-sample
//               precision (normalized cross-correlation; for edgy signals the
//               sub-sample refinement averages interpolated mid-level
//               crossing offsets, which is unbiased where a parabolic peak
//               fit "peak-locks" toward integer lags).
//   2. LUCKY  — frames that correlate poorly (glitch, missed trigger) or
//               clip are rejected/down-weighted before they blur the stack.
//   3. DRIZZLE— because asynchronous trigger vs sample clocks land every
//               frame on a different sub-sample phase, samples are binned
//               onto a K× finer time grid.
//   4. STACK  — per-bin averaging drops noise ~sqrt(N) → extra effective
//               bits (needs >~0.5 LSB of real noise for dither; this front
//               end has it).
//   5. DRIFT  — per-frame gain/offset is fit against the reference and
//               normalized out so slow drift doesn't blur long captures.
//   6. MODEL  — (optional) a sum-of-sinusoids LSQ fit over the stack gives
//               an arbitrarily dense analytic reconstruction.
//
// Pure functions over typed arrays; classic script + CJS guard so node can
// unit-test the math (superres.test.cjs) exactly like peaks.js/decode.js.









// srSeedRef adopts a SPECIFIC frame as the LOCKED alignment reference (e.g. a
// frame frozen with SINGLE), instead of auto-adopting the first live frame. Sets
// userRef so srFeed never re-seeds off it: only frames whose PATTERN matches this
// reference get stacked, the rest rejected (a burst stays a burst; the slow-stuff
// majority is thrown away instead of taking over). Returns true, or false if the
// frame is unusable (flat/clipped) — a bad locked reference poisons the capture.
"use strict";

// node: pull the sibling modules onto globalThis so bare calls resolve
// (the browser loads them as classic scripts before this one — same globals).
if (typeof require !== "undefined") { Object.assign(globalThis, require("./superres_math.js"), require("./superres_template.js"), require("./superres_gate.js"), require("./superres_measure.js")); }

function srSeedRef(st, sig1, sig2, edgeX, gate) {
  const sigs = [sig1, sig2];
  const alignSig = sigs[st.align];
  if (!alignSig || alignSig.length < st.n) return false;
  if (srClipped(alignSig)) return false;
  let lo = 255, hi = 0;
  for (let i = 0; i < st.n; i++) { const v = alignSig[i]; if (v < lo) lo = v; if (v > hi) hi = v; }
  if (hi - lo < 12) return false; // flat/untriggered — not a usable reference
  st.refEdgeX = edgeX != null ? edgeX : -1;
  st.edgeX = st.refEdgeX; // the stack lives on the reference's timeline
  for (let ch = 0; ch < 2; ch++) {
    const s = sigs[ch];
    if (!s || !(s.length >= st.n)) continue;
    st.c[ch].ref = Float32Array.from(s.subarray ? s.subarray(0, st.n) : s.slice(0, st.n));
  }
  // Auto-gate: the reference's active region (srBuildTemplate), narrowed to ONE
  // period when the region is clearly periodic so a repetitive waveform stacks
  // every cycle (multi-hit). A caller-supplied gate {lo,hi} overrides (manual).
  let gLo, gHi;
  if (gate && gate.hi > gate.lo) {
    // MANUAL gate (dragged markers): use it EXACTLY — the user picked this region,
    // do not narrow it. A wide gate stays cheap (srGateFeed bounds the search) and
    // a repetitive one still multi-hits (it matches at every period offset).
    gLo = Math.max(0, gate.lo | 0); gHi = Math.min(st.n, gate.hi | 0);
  } else {
    // AUTO default: active region, narrowed to ONE period when clearly periodic —
    // a fast, sensible default (multi-hit, cheap search).
    const active = srBuildTemplate(st.c[st.align].ref, st.n, st.refEdgeX, st.n);
    if (!active) return false; // no distinguishing content to gate
    gLo = active.lo; gHi = active.hi + 1;
    const period = srDetectPeriod(st.c[st.align].ref, gLo, gHi);
    if (period >= 16 && period < gHi - gLo) gHi = gLo + period;
  }
  if (gHi - gLo < 4) return false;
  return srGateInstall(st, gLo, gHi);
}


// ===== Gated multi-hit sub-sample stacker (reference-lock v2) ==============
// The frozen reference defines a GATE [gateLo, gateHi): the deterministic
// feature of interest. Each frame is matched-filtered for ALL occurrences of
// the gate pattern; every occurrence is sub-sample aligned (parabolic peak) and
// DRIZZLED onto the L*K grid at its fractional offset. Content outside the gate
// is ignored — never correlated, never stacked, never a reason to reject. So a
// repetitive frame yields MANY hits (fast convergence, real sub-sample
// diversity = super-resolution, not just averaging), while a one-shot glitch
// yields one hit — or a reject only when the frame contains zero occurrences.









function srFeed(st, sig1, sig2, opts) {
  opts = opts || {};
  const maxLag = opts.maxLag || 8;
  const sigs = [sig1, sig2];
  const alignSig = sigs[st.align];
  if (!alignSig || alignSig.length < st.n) return "rejected:short";
  if (srClipped(alignSig)) { st.clipped++; st.rejected++; return "rejected:clip"; }
  // REFERENCE RE-SEED: if most frames refuse to align to the current
  // reference, the reference itself is the outlier (this hardware's deep
  // drains come in populations — a reference from the minority rejects the
  // majority forever). Drop the stack and re-adopt from the incoming flow;
  // within a couple of re-seeds the reference lands in the dominant
  // population and acceptance recovers.
  st.attempts++;
  // Re-seed recovery is for AUTO references (a deep-drain minority-population
  // first frame). A USER reference (srSeedRef, e.g. a frame frozen with SINGLE)
  // is deliberately chosen and LOCKED — never drift off it, so a burst stays the
  // reference and the slow-stuff majority is rejected instead of taking over.
  if (st.c[st.align].ref && !st.userRef && st.attempts >= 30 && st.frames / st.attempts < 0.3) {
    const keep = { sampleS: st.sampleS, align: st.align, kernel: st.kernel, reseeds: st.reseeds + 1 };
    const scales = st.c.map(c => ({ vpc: c.vpc, offV: c.offV }));
    const fresh = srNew(st.n, st.K);
    Object.assign(st, fresh, keep);
    st.c.forEach((c, i) => { c.vpc = scales[i].vpc; c.offV = scales[i].offV; });
  }
  if (!st.c[st.align].ref) {
    // Reference quality gate: a flat/untriggered first frame would zero the
    // NCC denominator and poison the whole capture (everything after scores
    // 0 and is rejected). Wait for a frame with real signal.
    let lo = 255, hi = 0;
    for (let i = 0; i < st.n; i++) { const v = alignSig[i]; if (v < lo) lo = v; if (v > hi) hi = v; }
    if (hi - lo < 12) { st.rejected++; return "rejected:flat"; }
    st.refEdgeX = opts.edgeX != null ? opts.edgeX : -1;
    st.edgeX = st.refEdgeX; // the stack lives on the reference's timeline
    for (let ch = 0; ch < 2; ch++) {
      const s = sigs[ch];
      if (!s || !(s.length >= st.n)) continue;
      st.c[ch].ref = Float32Array.from(s.subarray ? s.subarray(0, st.n) : s.slice(0, st.n));
      srAccumCh(st, ch, st.c[ch].ref, 0); // the reference stacks at shift 0
    }
    st.frames++;
    st.scores.push(1); st.shifts.push(0);
    return "stacked";
  }
  // LOCKED USER REFERENCE (R3/R4/R5): match the frame against the reference's
  // distinguishing template, not the shared trigger edge. Reject non-matches
  // (slow stuff), find a burst wherever it sits, align to it, and stack. A FIXED
  // cut (0.62) — the adaptive ratchet over-rejects genuine weak/displaced bursts
  // once the reference is locked (it exists only for the auto deep-drain case).
  if (st.gated) return srGateFeed(st, sig1, sig2, opts); // reference-lock v2
  // Coarse alignment from the trigger-edge headers: on decimated deep bands
  // the trigger wanders through the drained record far beyond the NCC search
  // window, and a periodic signal is NCC-ambiguous modulo its period — the
  // shared trigger edge disambiguates both. NCC then refines the residual.
  let base = 0;
  if (st.refEdgeX >= 0 && opts.edgeX != null && opts.edgeX >= 0) {
    base = Math.round(opts.edgeX - st.refEdgeX);
  }
  // Align + score over a window around the reference's trigger edge: that
  // region is guaranteed real signal, whereas a deep drain's dead tail sits
  // at a different position each frame and would fail genuinely good frames.
  // ±2048 raw samples: comfortably inside the coherent capture on this
  // hardware's deep drains (real content ≈ first ~55% of a 20480 drain; the
  // dead tail mixes at per-frame boundaries and must stay out of the score).
  const center = st.refEdgeX >= 0 ? Math.round(st.refEdgeX) : st.n >> 1;
  const half = Math.min(st.n >> 1, opts.winHalf || 2048);
  const wLo = Math.max(0, center - half), wHi = Math.min(st.n, center + half);
  st.statLo = wLo; st.statHi = wHi;
  const al = srAlign(st.c[st.align].ref, alignSig, maxLag, base, wLo, wHi);
  if (!al) { st.rejected++; return "rejected:align"; }
  // Adaptive lucky threshold: after warm-up, cut at median − 3·MAD — but
  // never closer than 0.05 below the median. When all frames are good the
  // score distribution is razor thin (MAD ≈ 0) and a bare 3·MAD cut would
  // reject the healthy tail; real junk (glitch/flatline) scores FAR lower.
  let thr = opts.minScore != null ? opts.minScore : 0.6;
  if (st.scores.length >= 10) {
    const s = [...st.scores].sort((a, b) => a - b);
    const med = s[s.length >> 1];
    const mad = s.map(x => Math.abs(x - med)).sort((a, b) => a - b)[s.length >> 1];
    thr = Math.max(thr, med - Math.max(3 * 1.4826 * mad, 0.05));
  }
  if (al.score < thr) { st.rejected++; return "rejected:score"; }
  const shiftInt = Math.round(al.shift);
  for (let ch = 0; ch < 2; ch++) {
    let s = sigs[ch];
    if (!s || !(s.length >= st.n) || !st.c[ch].ref) continue;
    // Companion-channel clipping only excludes THAT channel's data for this
    // frame (the align channel was already vetted above).
    if (ch !== st.align && srClipped(s)) { st.c[ch].clipSkips++; continue; }
    if (opts.normalize !== false) {
      const { g, b } = srGainOffset(st.c[ch].ref, s, shiftInt, wLo, wHi);
      if (g !== 1 || b !== 0) {
        const f = new Float32Array(st.n);
        for (let i = 0; i < st.n; i++) {
          const v = (s[i] - b) / g;
          f[i] = v < 0 ? 0 : v; // codes can't go negative; -1 is the gap sentinel
        }
        s = f;
      }
    }
    srAccumCh(st, ch, s, al.shift);
  }
  st.frames++;
  st.scores.push(al.score);
  st.shifts.push(al.shift);
  return "stacked";
}

// srAccum drizzles one aligned frame onto the fine grid with LINEAR WEIGHTS:
// sample i lands at fine position (i − shift)·K and splits between the two
// adjacent bins by fractional distance — nearest-bin deposit left adjacent
// bins averaging disjoint frame subsets, whose independent errors rendered
// as a zig-zag ribbon; the linear kernel correlates neighbours at no
// information cost. Bins off the grid are dropped. Odd frames also land in
// the A half-stack (see srNew).
// srAccum kept as the single-channel entry (tests, channel 0).
function srAccum(st, sig, shift) { srAccumCh(st, 0, sig, shift); }

function srAccumCh(st, ch, sig, shift) {
  const K = st.K, nb = st.nbins, n = st.n;
  const C = st.c[ch];
  const sum = C.sum, sum2 = C.sum2, cnt = C.cnt;
  const odd = (st.frames & 1) === 1;
  const sumA = C.sumA, cntA = C.cntA;
  if (st.kernel === "interp" || st.kernel === "cubic") {
    // Resample THIS frame at every fine bin: every bin averages every frame,
    // so per-bin noise drops ~√K versus deposit kernels. The frame's raw
    // samples sit at fine position (i − shift)·K, so bin b reads the frame
    // at raw index b/K + shift. "cubic" uses a Catmull-Rom kernel — flatter
    // passband than linear, recovering most of the sinc² edge softening at
    // the same contributor count (lab iteration 5).
    const invK = 1 / K;
    const cubic = st.kernel === "cubic";
    for (let b = 0; b < nb; b++) {
      const t = b * invK + shift;
      const i0 = Math.floor(t);
      if (i0 < 1 || i0 + 2 >= n) {
        if (i0 < 0 || i0 + 1 >= n) continue;
        // record edges: fall back to linear
        const w = t - i0;
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
    return;
  }
  // "drizzle": linear-weighted deposit into the two adjacent fine bins.
  for (let i = 0; i < n; i++) {
    const pos = (i - shift) * K;
    const b0 = Math.floor(pos);
    const w1 = pos - b0, w0 = 1 - w1;
    const v = sig[i];
    if (b0 >= 0 && b0 < nb) {
      sum[b0] += w0 * v; sum2[b0] += w0 * v * v; cnt[b0] += w0;
      if (odd) { sumA[b0] += w0 * v; cntA[b0] += w0; }
    }
    const b1 = b0 + 1;
    if (w1 > 0 && b1 >= 0 && b1 < nb) {
      sum[b1] += w1 * v; sum2[b1] += w1 * v * v; cnt[b1] += w1;
      if (odd) { sumA[b1] += w1 * v; cntA[b1] += w1; }
    }
  }
}

// srResult reduces the accumulators to the stacked waveform + honest stats.
// mean: Float32Array of code-space values, -1 in unfilled bins (pen-up, the
// convention every renderer/exporter already understands).
//
// sigmaStack is MEASURED, not assumed: half the rms difference between the
// odd/even half-stack means. σ/√cnt (reported as sigmaStackTheory) is what
// independent Gaussian noise would give — the measured value also carries
// the quantization floor and correlated alignment error, so bitsGained
// cannot over-claim on a quiet (sub-LSB-noise) signal.
//
// opts.statsOnly skips the mean array; opts.stride subsamples the bins for
// the sigma medians (the live 500 ms stats tick uses both — a full reduction
// over 1.3M bins with two million-element sorts would jank the UI).
function srResult(st, opts) {
  opts = opts || {};
  const nb = st.nbins;
  const stride = Math.max(1, opts.stride | 0 || 1);
  const A = st.c[st.align]; // stats run on the align channel
  const other = st.c[1 - st.align];
  const mean = opts.statsOnly ? null : new Float32Array(nb);
  const mean2 = opts.statsOnly || !other.ref ? null : new Float32Array(nb);
  const statLo = st.statLo * st.K, statHi = st.statHi * st.K;
  const EPS = 0.05; // minimum weight for a bin to count as filled
  // Build the mean arrays in a tight loop (unavoidably O(nbins) — it IS the
  // 1.3M-point output trace) with no per-bin stats branching.
  const Asum = A.sum, Acnt = A.cnt;
  if (mean) for (let b = 0; b < nb; b++) { const c = Acnt[b]; mean[b] = c < EPS ? -1 : Asum[b] / c; }
  if (mean2) { const os = other.sum, oc = other.cnt; for (let b = 0; b < nb; b++) { const c = oc[b]; mean2[b] = c < EPS ? -1 : os[b] / c; } }
  // Stats: SUBSAMPLE to ~4096 bins across the window. The medians of the
  // per-bin sigmas are statistically identical from 4096 points as from a
  // million, but collecting+sorting a million elements cost ~250 ms.
  const span = Math.max(1, statHi - statLo);
  const statStride = Math.max(stride, Math.ceil(span / 4096));
  let filled = 0, scanned = 0;
  const sigSingles = [], halves = [];
  for (let b = statLo; b < statHi; b += statStride) {
    const c = A.cnt[b];
    scanned++;
    if (c < EPS) continue;
    filled++;
    const m = A.sum[b] / c;
    if (c >= 4) {
      const v = Math.max(0, A.sum2[b] / c - m * m);
      sigSingles.push(Math.sqrt(v));
      const ca = A.cntA[b], cb = c - ca;
      if (ca >= 2 && cb >= 2) {
        const ma = A.sumA[b] / ca, mb = (A.sum[b] - A.sumA[b]) / cb;
        halves.push((ma - mb) / 2);
      }
    }
  }
  const med = a => { if (!a.length) return 0; const s = [...a].sort((x, y) => x - y); return s[s.length >> 1]; };
  const sigmaSingle = med(sigSingles);
  // Median-based like sigmaSingle (×1.4826 = gaussian MAD→σ): an RMS here
  // would be dominated by the few EDGE bins, where the half-difference is
  // sub-bin slope aliasing, not noise — the two stats must describe the same
  // (flat-bin noise floor) population or bitsGained compares apples to
  // oranges.
  let sigmaStack = 0, sigmaMeasured = true;
  if (halves.length >= 16) {
    sigmaStack = 1.4826 * med(halves.map(Math.abs));
  }
  const cnts = [];
  for (let b = statLo; b < statHi; b += statStride) if (A.cnt[b] >= EPS) cnts.push(A.cnt[b]);
  const sigmaStackTheory = sigmaSingle > 0 && cnts.length ? sigmaSingle / Math.sqrt(med(cnts)) : 0;
  // The measured half-difference needs dense bins (≥4 frames each). Deposit
  // kernels ("drizzle") spread ~frames/K contributors per fine bin, so on a fine
  // grid most bins are too sparse to pair odd/even and `halves` stays empty —
  // report the theory estimate (σ_single/√cnt) instead of a bogus 0, flagged so
  // the UI can mark it as not-measured. It slightly over-claims (ignores
  // correlated error) but is the honest low-frames-per-bin figure.
  if (sigmaStack === 0 && sigmaStackTheory > 0) { sigmaStack = sigmaStackTheory; sigmaMeasured = false; }
  const bitsGained = sigmaStack > 0 && sigmaSingle > 0 ? Math.log2(sigmaSingle / sigmaStack) : 0;
  return {
    mean, mean2,
    clipSkips: [st.c[0].clipSkips, st.c[1].clipSkips],
    fill: filled / Math.max(1, scanned),
    frames: st.frames, hits: st.hits || 0, gated: !!st.gated, gridL: st.gridL || 0,
    rejected: st.rejected, clipped: st.clipped, reseeds: st.reseeds,
    sigmaSingle, sigmaStack, sigmaStackTheory, sigmaMeasured, bitsGained,
    effBits: 8 + bitsGained,
    fineDtS: st.sampleS > 0 ? st.sampleS / st.K : 0,
    effRateSa: st.sampleS > 0 ? st.K / st.sampleS : 0,
  };
}



if (typeof module !== "undefined") {
  module.exports = { srAlign, srCrossings, srGainOffset, srNew, srSeedRef, srFeed, srAccum, srResult, srModelFit, srClipped, srMeanStd, srMeasure };
}
