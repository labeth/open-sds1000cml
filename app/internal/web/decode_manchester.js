// Manchester decoder — JS twin of decode_manchester.go, kept algorithm-faithful
// so the web overlay and the on-device LCD agree byte-for-byte. Classic script:
// no imports/exports; reuses sliceChannel / logicAt / fmtByte / fail from
// decode.js (same global script scope in the browser).
//
// Manchester is self-clocking: every bit cell carries a mid-cell transition and
// its DIRECTION encodes the bit. cfg.ieee=true => IEEE 802.3 (rising@mid = 1,
// falling = 0); false => Thomas/G.E. (opposite). cfg.bitrate>0 pins the bit
// period; 0 auto-infers it from the edge gaps. cfg.msb / cfg.bits control byte
// packing. This bit-recovery core is the basis for MIL-STD-1553B later.

// recoverManchester samples the mid-cell transition direction at each bit cell,
// laying cells at s0, s0+T, s0+2T, ... (T = samples/bit). Mirrors the Go core:
// returns { cells:[{i0,i1,bit}], good, viol } where bit is 0/1 or -1 (a coding
// violation — the two half-cells sampled to the same level).
function recoverManchester(S, s0, T, ieee, lastEdgeX) {
  const cells = [];
  let good = 0, viol = 0;
  if (!(T >= 2) || S.n === 0) return { cells, good, viol };
  const limit = lastEdgeX + 0.25 * T;          // stop once cells fall into trailing idle
  const maxCells = Math.floor(S.n / T) + 4;    // safety cap; the limit/gap breaks normally
  let consecViol = 0;                          // a valid cell always has a mid transition, so a
  //                                              run of missing ones is an inter-frame IDLE gap:
  //                                              stop there (real captures hold several frames whose
  //                                              gaps are non-integer T apart, so one phase cannot
  //                                              align them all — decode the first frame cleanly).
  for (let k = 0; k < maxCells; k++) {
    const centre = s0 + (k + 0.5) * T;
    if (centre > limit) break;
    // First and third quarter straddle the mid-cell transition.
    const l1 = logicAt(S, s0 + (k + 0.25) * T);
    const l2 = logicAt(S, s0 + (k + 0.75) * T);
    if (l1 < 0 || l2 < 0) break;               // ran off the captured region
    let i0 = Math.round(s0 + k * T), i1 = Math.round(s0 + (k + 1) * T) - 1;
    if (i0 < 0) i0 = 0;
    if (i1 >= S.n) i1 = S.n - 1;
    if (i1 < i0) i1 = i0;
    if (l1 === l2) {                           // no mid transition
      if (++consecViol >= 2) {                 // idle run => frame boundary: drop the
        const n = cells.length;                // lone idle cell we just added, then stop
        if (n > 0 && cells[n - 1].bit < 0) { cells.pop(); viol--; }
        break;
      }
      cells.push({ i0, i1, bit: -1 }); viol++; continue; // isolated violation: keep as an error mark
    }
    consecViol = 0;
    const rising = l2 > l1;
    const bit = (rising === ieee) ? 1 : 0;     // IEEE: rising@mid = 1; Thomas: = 0
    cells.push({ i0, i1, bit });
    good++;
  }
  return { cells, good, viol };
}

function decodeManchester(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const bits = cfg.bits || 8;
  if (bits < 1 || bits > 16) return fail("manchester", "data bits out of range (1..16)");
  const minSPB = 4;
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("manchester", S.reason);
  if (S.edges.length < 2) return fail("manchester", "too few edges");

  // Bit period T (samples/bit). cfg.bitrate pins it; otherwise infer from edge
  // gaps: consecutive edges are T/2 apart when adjacent bits are EQUAL, or T apart
  // when they DIFFER. Any real Manchester frame (preamble + varied data) has its
  // SHORTEST gaps at T/2, so T = 2·(shortest-gap cluster). We deliberately do NOT
  // try the half period T/2: for a degraded signal the true period can't frame, a
  // T/2 hypothesis cherry-picks alternating runs into confident all-0x55 GARBAGE
  // (a false positive). If the true period can't frame, we report no-decode.
  let T;
  if (cfg.bitrate > 0) {
    T = (1 / cfg.bitrate) / colTimeS;
  } else {
    const gaps = [];
    // sub-sample crossings: at small T the integer index quantizes a T/2 gap of
    // 2.5 down to 2, mis-scaling the inferred period below the truth.
    for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].x - S.edges[k - 1].x; if (g >= 1) gaps.push(g); }
    if (gaps.length < 3) return fail("manchester", "too few edges / cannot infer bitrate");
    gaps.sort((a, b) => a - b);
    let hp = gaps[Math.floor(gaps.length * 0.1)];
    let sum = 0, cnt = 0;
    // ±0.5 window (not ±0.35): absorb the {2,3} quantization spread at small T.
    for (const g of gaps) if (Math.abs(g - hp) <= 0.5 * hp) { sum += g; cnt++; }
    if (cnt) hp = sum / cnt;
    T = 2 * hp;
  }
  if (!isFinite(T) || !(T >= minSPB)) return fail("manchester", T.toFixed(1) + " samples/bit; need >= " + minSPB);
  return decodeManchesterAt(S, T, cfg, bits, colTimeS).res;
}

// decodeManchesterAt segments the edges into frames and decodes each at bit
// period T, returning { res, frames, totalGood } so the caller can score
// competing T hypotheses. Mirrors the Go helper of the same name.
function decodeManchesterAt(S, T, cfg, bits, colTimeS) {
  const ieee = !!cfg.ieee, msb = !!cfg.msb, fmt = cfg.fmt || "hex";
  // Split on an inter-frame idle. Use 2.5·T (not 1.5·T): a single flattened cell
  // (coding violation) opens a ~2·T gap that 1.5·T would split, orphaning the
  // corrupted tail unreported. Keeping it in-frame lets recoverManchester mark it.
  const segs = [];
  let segStart = 0;
  for (let k = 1; k < S.edges.length; k++) {
    if (S.edges[k].i - S.edges[k - 1].i > 2.5 * T) { segs.push([segStart, k - 1]); segStart = k; }
  }
  segs.push([segStart, S.edges.length - 1]);

  const spans = [], bytes = [], toks = [];
  let frames = 0, totalGood = 0;
  for (let sgIdx = 0; sgIdx < segs.length; sgIdx++) {
    const sg = segs[sgIdx];
    if (sg[1] <= sg[0]) continue;              // a lone edge carries no cell
    // Phase lock: the segment's first edge is a cell boundary (phase A) or a
    // mid-cell transition (phase B, boundary half a period earlier). Score each.
    const s0e = S.edges[sg[0]].x, lastE = S.edges[sg[1]].x;
    const rA = recoverManchester(S, s0e, T, ieee, lastE);
    const rB = recoverManchester(S, s0e - 0.5 * T, T, ieee, lastE);
    const scA = rA.good - 4 * rA.viol, scB = rB.good - 4 * rB.viol;
    let cells, bestGood;
    const bestScore = Math.max(scA, scB);
    if (scA > scB) { cells = rA.cells; bestGood = rA.good; }
    else if (scB > scA) { cells = rB.cells; bestGood = rB.good; }
    else {
      // A bare constant-bit frame is a pure square wave: both phases decode
      // cleanly but into complementary bits — an inherent ±half-cell ambiguity.
      // Resolve it by idle SYMMETRY: the bit whose leading half-cell equals the
      // idle leaves an extra ~half-period of idle on the LEAD side (first edge is
      // a mid, not a boundary), so the leading idle run outlasts the trailing one.
      let leadRun = s0e;
      if (sg[0] > 0) leadRun = s0e - S.edges[sg[0] - 1].x;
      let trailRun = S.n - lastE;
      if (sg[1] < S.edges.length - 1) trailRun = S.edges[sg[1] + 1].x - lastE;
      if (leadRun > trailRun) { cells = rB.cells; bestGood = rB.good; }
      else { cells = rA.cells; bestGood = rA.good; }
    }
    if (!cells.length || bestScore <= 0 || bestGood < bits) continue; // need one whole byte of clean cells
    // Leading alternating run = preamble. A FIRST/LAST segment of a free-running
    // capture may be truncated by the record edge; require a preamble there to
    // drop that partial. Interior segments are whole frames — accept as-is.
    let run = 0;
    if (cells[0].bit >= 0) {
      run = 1;
      for (let i = 1; i < cells.length && cells[i].bit >= 0 && cells[i].bit !== cells[i - 1].bit; i++) run++;
    }
    const atEdge = segs.length > 1 && (sgIdx === 0 || sgIdx === segs.length - 1);
    if (run < 3 && atEdge) continue;
    if (frames > 0) {                          // separate frames in the transcript
      spans.push({ i0: cells[0].i0, i1: cells[0].i0, text: "", kind: "gap" });
      toks.push("|");
    }
    frames++;
    totalGood += bestGood;
    // run==0 (an interior frame whose FIRST cell is a coding violation) has no
    // preamble to span — anchor the SYNC marker on the first cell instead of
    // reading cells[-1] (this exact shape crashed on a real SENT capture fed
    // through autodetect's Manchester hypothesis; mirrors decode_manchester.go).
    spans.push({ i0: cells[0].i0, i1: run > 0 ? cells[run - 1].i1 : cells[0].i0, text: "SYNC", kind: "start" });
    // Pack this frame's cells into words; a coding violation flushes the word.
    let curVal = 0, curBits = 0, byteStart = 0, lastI1 = 0;
    for (const c of cells) {
      if (c.bit < 0) {
        spans.push({ i0: c.i0, i1: c.i1, text: "!", kind: "frame-error" });
        curVal = 0; curBits = 0;
        continue;
      }
      if (curBits === 0) { byteStart = c.i0; curVal = 0; }
      lastI1 = c.i1;
      if (msb) curVal = (curVal << 1) | c.bit; else curVal |= c.bit << curBits;
      curBits++;
      if (curBits === bits) {
        spans.push({ i0: byteStart, i1: c.i1, text: fmtByte(curVal, fmt), kind: "data", val: curVal });
        toks.push(fmtByte(curVal, fmt)); bytes.push(curVal);
        curVal = 0; curBits = 0;
      }
    }
    // A dangling partial word means a coding violation ate the frame's tail (the
    // erased mid-transition erased the last edge, so the violated cell fell just
    // outside recoverManchester's window) — flag the stub, don't drop it silently.
    if (curBits > 0) {
      spans.push({ i0: byteStart, i1: lastI1, text: "!", kind: "frame-error" });
      toks.push("!");
    }
  }
  if (frames === 0) return { res: fail("manchester", "no Manchester frame (preamble) found"), frames: 0, totalGood: 0 };

  let baud = cfg.bitrate || 0;
  if (colTimeS > 0) baud = Math.round(1 / (T * colTimeS));
  const res = { ok: true, error: null, proto: "manchester", spans, text: toks.join(" "), bytes,
    meta: { bitrate: baud, samplesPerBit: T, ieee, bitOrder: msb ? "msb" : "lsb", threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail } };
  return { res, frames, totalGood };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { recoverManchester, decodeManchester, decodeManchesterAt };
