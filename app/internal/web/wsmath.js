// ENGMODEL-OWNER-UNIT: FU-WEB-WSMATH
// wsmath.js — the workspaces' small numeric helpers: running statistics
// (MEASURE), the reference difference (COMPARE), the "does the signal repeat"
// test that gates a stacked master, and the mask dilation shared by the mask
// build and the master. Pure: no DOM, no globals; loaded by ui.html and
// require()d by wsmath.test.cjs. Go twins for the LCD live in internal/analysis.
"use strict";

// RunStats accumulates a measurement's statistics with Welford's update (no
// catastrophic cancellation on long runs): count, mean, min, max, sample
// standard deviation. Non-finite values are skipped.
// TRLC-LINKS: REQ-SDS-217
class RunStats {
  constructor() { this.reset(); }
  reset() { this.n = 0; this.mean = 0; this.m2 = 0; this.min = Infinity; this.max = -Infinity; this.last = NaN; }
  add(v) {
    this.last = typeof v === "number" ? v : NaN;
    if (!Number.isFinite(this.last)) return;
    this.n++;
    const d = v - this.mean;
    this.mean += d / this.n;
    this.m2 += d * (v - this.mean);
    if (v < this.min) this.min = v;
    if (v > this.max) this.max = v;
  }
  sdev() { return this.n > 1 ? Math.sqrt(this.m2 / (this.n - 1)) : NaN; }
}

// refDiff compares a live trace with a reference sample by sample (both in
// volts on one time grid; NaN = no sample there) and reports the largest
// absolute difference, the RMS difference and the percentage of samples within
// tol (half a division in the readout).
// TRLC-LINKS: REQ-SDS-218
function refDiff(live, ref, tol) {
  let mx = 0, s2 = 0, n = 0, inside = 0;
  const len = Math.min(live.length, ref.length);
  for (let i = 0; i < len; i++) {
    const a = live[i], b = ref[i];
    if (!Number.isFinite(a) || !Number.isFinite(b)) continue;
    const d = Math.abs(a - b);
    if (d > mx) mx = d;
    s2 += d * d; n++;
    if (d <= tol) inside++;
  }
  return n ? { maxAbs: mx, rms: Math.sqrt(s2 / n), pctWithin: 100 * inside / n, n } : { maxAbs: NaN, rms: NaN, pctWithin: NaN, n: 0 };
}

// envelope returns the ±slack-sample running min and max of a record (gaps,
// values < 0 or NaN, are skipped; a window of only gaps gives min > max).
// TRLC-LINKS: REQ-SDS-218
function envelope(x, slack) {
  const n = x.length, lo = new Float64Array(n), hi = new Float64Array(n);
  const qLo = new Int32Array(n), qHi = new Int32Array(n);
  let lh = 0, lt = 0, hh = 0, ht = 0, next = 0;
  // TRLC-LINKS: REQ-SDS-218
  const ok = (v) => v >= 0 && Number.isFinite(v);
  for (let j = 0; j < n; j++) {
    const end = Math.min(n - 1, j + slack), start = Math.max(0, j - slack);
    while (next <= end) {
      if (ok(x[next])) {
        while (lt > lh && x[qLo[lt - 1]] >= x[next]) lt--;
        while (ht > hh && x[qHi[ht - 1]] <= x[next]) ht--;
        qLo[lt++] = next; qHi[ht++] = next;
      }
      next++;
    }
    while (lh < lt && qLo[lh] < start) lh++;
    while (hh < ht && qHi[hh] < start) hh++;
    lo[j] = lh < lt ? x[qLo[lh]] : Infinity;
    hi[j] = hh < ht ? x[qHi[hh]] : -Infinity;
  }
  return { lo, hi };
}

// repeats is the master's gate, the same rule as the mask build: every sample
// of each record lies within ±tol of the OTHER record's ±slack-sample min/max
// envelope. Two consecutive captures of a repeating, well-triggered signal pass;
// a free-running or changing one does not. Gaps and the slack-wide ends are
// skipped; a pair with less than half its samples comparable does not count.
// TRLC-LINKS: REQ-SDS-218
function repeats(prev, cur, tol, slack) {
  const n = Math.min(prev.length, cur.length);
  if (n < 8) return false;
  const a = prev.length === n ? prev : prev.slice(0, n), b = cur.length === n ? cur : cur.slice(0, n);
  const ea = envelope(a, slack), eb = envelope(b, slack);
  // the first and last `slack` samples have a truncated envelope: a shifted edge
  // there has no partner inside the record, so they are not judged
  let compared = 0;
  for (let i = slack; i < n - slack; i++) {
    const va = a[i], vb = b[i];
    if (vb >= 0 && Number.isFinite(vb) && ea.hi[i] >= ea.lo[i]) {
      compared++;
      if (vb < ea.lo[i] - tol || vb > ea.hi[i] + tol) return false;
    }
    if (va >= 0 && Number.isFinite(va) && eb.hi[i] >= eb.lo[i]) {
      if (va < eb.lo[i] - tol || va > eb.hi[i] + tol) return false;
    }
  }
  return compared >= (n - 2 * slack) / 2;
}

// maskDilate widens a min/max envelope by ±tolT samples (a sliding min/max,
// linear even for full SRAM records and wide tolerances) and ±tolV codes,
// clamped to the 8-bit code range. Unobserved columns (lo > hi) become
// always-pass (0..255) first: dilating inverted bounds would fail every sample.
// TRLC-LINKS: REQ-SDS-014, REQ-SDS-218
function maskDilate(lo, hi, tolT, tolV) {
  const win = lo.length;
  lo = Array.from(lo); hi = Array.from(hi);
  for (let j = 0; j < win; j++) if (lo[j] > hi[j]) { lo[j] = 0; hi[j] = 255; }
  const dLo = new Array(win), dHi = new Array(win);
  const qLo = new Int32Array(win), qHi = new Int32Array(win);
  let lh = 0, lt = 0, hh = 0, ht = 0, next = 0;
  for (let j = 0; j < win; j++) {
    const end = Math.min(win - 1, j + tolT), start = Math.max(0, j - tolT);
    while (next <= end) {
      while (lt > lh && lo[qLo[lt - 1]] >= lo[next]) lt--;
      while (ht > hh && hi[qHi[ht - 1]] <= hi[next]) ht--;
      qLo[lt++] = next; qHi[ht++] = next++;
    }
    while (lh < lt && qLo[lh] < start) lh++;
    while (hh < ht && qHi[hh] < start) hh++;
    dLo[j] = Math.max(0, Math.floor(lo[qLo[lh]] - tolV));
    dHi[j] = Math.min(255, Math.ceil(hi[qHi[hh]] + tolV));
  }
  return { lo: dLo, hi: dHi };
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { RunStats, refDiff, envelope, repeats, maskDilate };
}
