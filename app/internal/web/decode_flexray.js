// FlexRay decoder — the JS twin of Go internal/decode/decode_flexray.go. Kept
// algorithm-faithful (same TSS detection, BSS-stripped byte recovery and header
// split) so the web overlay and the on-device LCD agree byte-for-byte.
//
// The bus is shown as a single logic line (the BP/BM pair collapsed to two
// levels): idle sits HIGH. A frame is framed like a stretched async word:
//   TSS  Transmission Start Sequence — a long LOW run (>= ~5 bit-times).
//   FSS  Frame Start Sequence        — one HIGH bit right after the TSS.
//   then repeatedly, one per byte:
//     BSS Byte Start Sequence        — one HIGH bit then one LOW bit (1,0).
//     8 data bits, MSB-first.
//   FES  Frame End Sequence          — a LOW then HIGH; or the line returns idle.
// The BSS in front of every byte re-establishes a HIGH->LOW falling edge (used
// to re-lock phase against clock drift), and its shape distinguishes "another
// byte follows" (high,low) from "frame over" (FES low,high, or idle high,high).
//
// Depends on sliceChannel / logicAt from decode.js (loaded first in the browser;
// pulled from the module under node).
if (typeof sliceChannel === "undefined" && typeof require !== "undefined") {
  const _d = require("./decode.js");
  globalThis.sliceChannel = _d.sliceChannel;
  globalThis.logicAt = _d.logicAt;
}

function fxHex2(v) { return (v & 0xff).toString(16).toUpperCase().padStart(2, "0"); }
function fxFail(reason) {
  return { ok: false, error: reason, proto: "flexray", spans: [], text: "", bytes: [] };
}

function decodeFlexRay(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const minSPB = 4;
  const tssMinLowBits = 4; // "~5 bit-times of LOW" — accept a little short for jitter
  const S = sliceChannel(codes, { threshold: cfg.haveThr ? cfg.threshold : undefined });
  if (!S.ok) return fxFail(S.reason);
  if (S.edges.length < 2) return fxFail("too few edges");

  // Bit period T (samples/bit). cfg.bitrate pins it; otherwise infer from the
  // edge gaps: the shortest FlexRay pulses are exactly one bit wide (the BSS
  // high/low bits and isolated data bits), so the smallest gaps cluster at T.
  let T;
  if (cfg.bitrate > 0) {
    T = (1 / cfg.bitrate) / colTimeS;
  } else {
    const gaps = [];
    for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].i - S.edges[k - 1].i; if (g >= 1) gaps.push(g); }
    if (gaps.length < 3) return fxFail("too few edges / cannot infer bitrate");
    gaps.sort((a, b) => a - b);
    let bp = gaps[Math.floor(gaps.length * 0.1)];
    let sum = 0, cnt = 0;
    for (const g of gaps) if (Math.abs(g - bp) <= 0.35 * bp) { sum += g; cnt++; }
    if (cnt) bp = sum / cnt;
    T = bp;
  }
  if (!isFinite(T) || !(T >= minSPB)) return fxFail(T.toFixed(1) + " samples/bit; need >= " + minSPB);
  const tol = 0.4 * T;
  const maxBytes = Math.floor(S.n / (10 * T)) + 4; // safety cap; BSS/FES/EOF break normally

  // resync snaps a byte's BSS anchor onto the guaranteed HIGH->LOW falling edge
  // in the middle of its BSS (at anchor+T), correcting accumulated clock drift.
  // ei is a monotonic pointer: anchors only ever increase across the record.
  let ei = 0;
  const resync = (anchor) => {
    const target = anchor + T;
    while (ei < S.edges.length && S.edges[ei].x < target - tol) ei++;
    let best = Infinity, bestX = 0, found = false;
    for (let j = ei; j < S.edges.length && S.edges[j].x <= target + tol; j++) {
      if (S.edges[j].dir < 0) { // BSS mid transition is HIGH->LOW
        const d = Math.abs(S.edges[j].x - target);
        if (d < best) { best = d; bestX = S.edges[j].x; found = true; }
      }
    }
    return found ? bestX - T : anchor;
  };

  const spans = [], bytes = [], toks = [];
  let frames = 0, consumedUntil = -1;

  // Scan rising edges for a TSS->FSS boundary: a rising edge preceded by a LOW
  // run of >= tssMinLowBits bit-times. Requiring a preceding falling edge means
  // the record captured the idle->TSS transition, so a frame truncated at the
  // record start is dropped. Each accepted frame is decoded whole and skipped
  // past (consumedUntil), so the long LOW runs an all-zero data byte can produce
  // are never mistaken for a second TSS.
  for (let k = 1; k < S.edges.length; k++) {
    const e = S.edges[k];
    if (e.dir <= 0 || e.i < consumedUntil) continue;
    const prev = S.edges[k - 1];
    if (prev.dir >= 0) continue;              // the run before a rising edge must be LOW
    if (e.x - prev.x < tssMinLowBits * T) continue;

    // TSS found. The FSS (one HIGH bit) begins at this rising edge; the first
    // BSS begins one bit later.
    const anchorFSS = e.x;
    const frameSpans = [{ i0: prev.i, i1: e.i, text: "TSS", kind: "start" }];
    const frameBytes = [], frameToks = [];
    let anchor = anchorFSS + T, lastByteEnd = e.i;
    for (let b = 0; b < maxBytes; b++) {
      anchor = resync(anchor);
      // A valid BSS is HIGH then LOW. Anything else — FES (low,high), idle
      // (high,high), or running off the end — ends the frame.
      if (logicAt(S, anchor + 0.5 * T) !== 1 || logicAt(S, anchor + 1.5 * T) !== 0) break;
      let val = 0, eof = false;
      for (let d = 0; d < 8; d++) {           // 8 data bits, MSB-first
        const bit = logicAt(S, anchor + (2.5 + d) * T);
        if (bit < 0) { eof = true; break; }
        val = (val << 1) | bit;
      }
      if (eof) break;                         // trailing byte truncated by the record end — drop it
      let i0 = Math.round(anchor), i1 = Math.round(anchor + 10 * T) - 1;
      if (i0 < 0) i0 = 0;
      if (i1 >= S.n) i1 = S.n - 1;
      if (i1 < i0) i1 = i0;
      frameSpans.push({ i0, i1, text: fxHex2(val), kind: "data", val });
      frameToks.push(fxHex2(val));
      frameBytes.push(val);
      lastByteEnd = i1;
      anchor += 10 * T;
    }

    if (frameBytes.length === 0) { consumedUntil = e.i + 1; continue; }

    // Header bonus: the first 5 bytes are the FlexRay header —
    // flags(5) frameID(11) payloadLen(7) headerCRC(11) cycle(6) = 40 bits,
    // MSB-first. Emit a note span (kind "addr") right after the TSS.
    if (frameBytes.length >= 5) {
      let hdr = 0;
      for (let hb = 0; hb < 5; hb++) hdr = hdr * 256 + (frameBytes[hb] & 0xff); // 40-bit value
      // Use 2**n (not 1<<n): JS shift counts are masked mod 32, so 1<<36 == 1<<4.
      const frameID = Math.floor(hdr / 2 ** 24) & 0x7FF;
      const payloadLen = Math.floor(hdr / 2 ** 17) & 0x7F;
      const cycle = hdr % 64;
      const sync = Math.floor(hdr / 2 ** 36) & 1;
      const startup = Math.floor(hdr / 2 ** 35) & 1;
      let note = "ID=" + frameID + " LEN=" + payloadLen + " CYC=" + cycle;
      if (sync === 1) note += " SYNC";
      if (startup === 1) note += " STARTUP";
      frameSpans.splice(1, 0, { i0: frameSpans[1].i0, i1: frameSpans[5].i1, text: note, kind: "addr", val: frameID });
      frameToks.unshift("[" + note + "]");
    }

    if (frames > 0) { // separate frames in the transcript
      spans.push({ i0: frameSpans[0].i0, i1: frameSpans[0].i0, text: "", kind: "gap" });
      toks.push("|");
    }
    frames++;
    for (const s of frameSpans) spans.push(s);
    for (const t of frameToks) toks.push(t);
    for (const bb of frameBytes) bytes.push(bb);
    consumedUntil = lastByteEnd + 1;
  }

  if (frames === 0) return fxFail("no FlexRay frame (TSS) found");
  let baud = cfg.bitrate || 0;
  if (colTimeS > 0) baud = Math.round(1 / (T * colTimeS));
  return { ok: true, error: null, proto: "flexray", spans, text: toks.join(" "), bytes,
    meta: { bitrate: baud, samplesPerBit: T, threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail, frames } };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeFlexRay };
