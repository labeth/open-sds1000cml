// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// wsmath.test.cjs — the workspaces' numeric helpers (wsmath.js): Welford
// statistics, the reference difference, the master's repeat gate and the
// shared mask dilation.
"use strict";
const assert = require("node:assert/strict");
const W = require("./wsmath.js");
// TRLC-LINKS: REQ-SDS-217, REQ-SDS-218
const near = (a, b, tol, m) => assert.ok(Math.abs(a - b) <= tol, `${m}: ${a} vs ${b}`);

// ---- RunStats: mean/min/max/sdev, NaN skipped, reset ----
{
  const s = new W.RunStats();
  for (const v of [2, 4, 4, 4, 5, 5, 7, 9]) s.add(v);
  s.add(NaN); s.add(undefined);
  assert.equal(s.n, 8, "non-finite values are skipped");
  near(s.mean, 5, 1e-12, "mean");
  assert.equal(s.min, 2); assert.equal(s.max, 9);
  near(s.sdev(), Math.sqrt(32 / 7), 1e-12, "sample standard deviation");
  assert.ok(Number.isNaN(s.last), "last keeps the latest value, even NaN");
  // a long run of a large offset: no cancellation
  const t = new W.RunStats();
  for (let i = 0; i < 100000; i++) t.add(1e9 + (i % 2 ? 1 : -1));
  near(t.sdev(), Math.sqrt(100000 / 99999), 1e-6, "Welford stays exact at a large offset (sample sdev of ±1)");
  t.reset();
  assert.equal(t.n, 0); assert.ok(Number.isNaN(t.sdev()));
}

// ---- refDiff: max |d|, RMS, % within tol, gaps skipped ----
{
  const ref = [0, 0, 0, 0, NaN];
  const live = [0.1, -0.2, 0.3, 0.6, 5];
  const d = W.refDiff(live, ref, 0.25);
  assert.equal(d.n, 4, "the gap is skipped");
  near(d.maxAbs, 0.6, 1e-12, "max |d|");
  near(d.rms, Math.sqrt((0.01 + 0.04 + 0.09 + 0.36) / 4), 1e-12, "rms");
  near(d.pctWithin, 50, 1e-12, "two of four within 0.25");
  assert.ok(Number.isNaN(W.refDiff([NaN], [1], 1).rms), "nothing comparable → NaN");
}

// ---- repeats: a jittered square repeats; free-running phases do not ----
{
  // TRLC-LINKS: REQ-SDS-217, REQ-SDS-218
  const sq = (n, ph, noise) => Array.from({ length: n }, (_, i) => (((i + ph) % 100) < 50 ? 180 : 70) + (noise ? (((i * 7919 + ph * 31) % 7) - 3) : 0));
  const a = sq(2000, 0, true), b = sq(2000, 2, true);         // 2 samples of trigger jitter, ±3 codes noise
  assert.ok(W.repeats(a, b, 8, 5), "a repeating, jittered capture passes the mask build's rule");
  assert.ok(!W.repeats(a, sq(2000, 37, true), 8, 5), "a capture at another phase does not repeat");
  assert.ok(!W.repeats(a, a.map((v) => v + 20), 8, 5), "an offset level does not repeat");
  const gappy = a.map((v, i) => (i < 1500 ? -1 : v));
  assert.ok(!W.repeats(a, gappy, 8, 5), "too little to compare is not a repeat");
}

// ---- maskDilate: ±t sliding min/max, ±V, clamps, unobserved columns pass ----
{
  const lo = [100, 100, 50, 100, 100, 255], hi = [110, 110, 60, 110, 110, 0];
  const d = W.maskDilate(lo, hi, 1, 2);
  assert.deepEqual(d.lo, [98, 48, 48, 48, 0, 0], "lows: min over ±1 sample, −2 codes; the unobserved column passes everything");
  assert.deepEqual(d.hi, [112, 112, 112, 112, 255, 255], "highs: max over ±1 sample, +2 codes, clamped");
  const e = W.maskDilate([0, 255], [3, 255], 0, 10);
  assert.deepEqual([e.lo, e.hi], [[0, 245], [13, 255]], "clamped to the code range");
}
console.log("ALL PASS");
