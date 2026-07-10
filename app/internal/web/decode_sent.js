// decode_sent.js — SENT (SAE J2716) single-wire decoder, a classic script that
// mirrors decode_sent.go step for step so the web UI and the on-device LCD agree
// byte-for-byte. Reuses the shared sliceChannel() from decode.js (a global when
// loaded via <script>, or require()d under node). SENT is pulse-width / tick
// encoded, measured falling-edge to falling-edge: each pulse period / tick gives a
// value. A frame opens with a 56-tick SYNC (used to derive the tick), followed by
// nibble pulses (12..27 ticks => 0..15), optionally closed by a pause pulse.

function sentHex1(v) { return (v & 0xf).toString(16).toUpperCase(); }

function decodeSENT(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const slice = (typeof sliceChannel === "function")
    ? sliceChannel
    : (typeof require === "function" ? require("./decode.js").sliceChannel : null);
  const SYNC = 56, NMIN = 12, NMAX = 27, TOL = 0.20;
  const fail = (error) => ({ ok: false, error, proto: "sent", spans: [], text: "", bytes: [], meta: {} });

  let nib = cfg.nibbles | 0;
  if (nib <= 0) nib = 8;        // status + 6 data + CRC
  if (nib > 64) nib = 64;       // bound the per-frame loop

  const S = slice(codes, cfg);
  if (!S.ok) return fail(S.reason);
  const n = S.n;

  // Falling edges (high->low) delimit pulse periods; x is the interpolated
  // crossing (sub-sample precision), i anchors the span.
  const fallX = [], fallI = [];
  for (const e of S.edges) if (e.dir < 0 && e.i < n) { fallX.push(e.x); fallI.push(e.i); }
  if (fallX.length < 2) return fail("no SENT pulses (need >= 2 falling edges)");

  const np = fallX.length - 1;
  const period = new Array(np);
  for (let k = 0; k < np; k++) period[k] = fallX[k + 1] - fallX[k];

  const haveTick = cfg.tickNs > 0 && colTimeS > 0;
  let seedTick = haveTick ? (cfg.tickNs * 1e-9) / colTimeS : 0;

  // Locate the first SYNC (~56 ticks) and, when not overridden, derive the tick
  // from it. A real SYNC is followed by valid nibbles; a nibble mistaken for the
  // SYNC yields a tiny tick that blows every follower out of the 12..27 range.
  let firstSync = -1;
  if (haveTick) {
    for (let k = 0; k < np; k++)
      if (Math.abs(period[k] / seedTick - SYNC) <= TOL * SYNC) { firstSync = k; break; }
  } else {
    for (let k = 0; k < np; k++) {
      if (period[k] <= 0) continue;
      const tickHyp = period[k] / SYNC;
      if (tickHyp <= 0) continue;
      let valid = 0, checked = 0;
      for (let j = 1; j <= nib && k + j < np; j++) {
        checked++;
        const r = period[k + j] / tickHyp;
        if (r >= NMIN * (1 - TOL) && r <= NMAX * (1 + TOL)) valid++;
      }
      if (checked > 0 && valid >= Math.ceil(0.6 * checked)) { firstSync = k; seedTick = tickHyp; break; }
    }
    // Fallback for a short capture: take the longest pulse as the SYNC.
    if (firstSync < 0) {
      let maxK = -1, maxV = 0;
      for (let k = 0; k < np; k++) if (period[k] > maxV) { maxV = period[k]; maxK = k; }
      if (maxK >= 0 && maxV > 0) { firstSync = maxK; seedTick = maxV / SYNC; }
    }
  }
  if (firstSync < 0 || seedTick <= 0) return fail("no SENT SYNC (~56-tick) pulse found");

  const looksSync = (P, tick) => tick > 0 && Math.abs(P / tick - SYNC) <= TOL * SYNC;

  const spans = [], bytes = [], toks = [];
  let curTick = seedTick, k = firstSync, guard = 0;
  const maxIter = np + 4; // paranoia: k strictly advances, so this never trips
  while (k < np) {
    if (++guard > maxIter) break;
    if (!looksSync(period[k], curTick)) { k++; continue; }
    spans.push({ i0: fallI[k], i1: fallI[k + 1], text: "SYNC", kind: "sync", val: 0 });
    toks.push("SYNC");
    if (!haveTick) curTick = period[k] / SYNC; // recalibrate the tick every frame
    k++;

    for (let nbIdx = 0; nbIdx < nib && k < np; nbIdx++) {
      const P = period[k];
      if (looksSync(P, curTick)) break; // SYNC mid-frame => truncated; re-handle it
      const val = Math.round(P / curTick) - 12;
      const kind = (nbIdx === nib - 1) ? "crc" : "data";
      if (val < 0 || val > 15) {
        const cv = val < 0 ? 0 : (val > 15 ? 15 : val);
        spans.push({ i0: fallI[k], i1: fallI[k + 1], text: "!" + sentHex1(cv), kind: "frame-error", val: cv });
        toks.push("!" + sentHex1(cv));
      } else {
        spans.push({ i0: fallI[k], i1: fallI[k + 1], text: sentHex1(val), kind, val });
        toks.push(sentHex1(val));
        bytes.push(val);
      }
      k++;
    }

    if (cfg.pausePulse && k < np && !looksSync(period[k], curTick)) {
      spans.push({ i0: fallI[k], i1: fallI[k + 1], text: "PAUSE", kind: "pause", val: 0 });
      toks.push("PAUSE");
      k++;
    }
  }
  if (spans.length === 0) return fail("no SENT SYNC (~56-tick) pulse found");
  return { ok: true, error: null, proto: "sent", spans, text: toks.join(" "), bytes,
    meta: { tickSamples: curTick, threshold: S.threshold, nibbles: nib } };
}

if (typeof module !== "undefined" && module.exports) module.exports = { decodeSENT, sentHex1 };
