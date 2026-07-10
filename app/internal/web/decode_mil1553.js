// MIL-STD-1553B decoder — JS twin of decode_mil1553.go, kept algorithm-faithful
// so the web overlay and the on-device LCD agree byte-for-byte. Classic script:
// no imports; reuses sliceChannel / logicAt / popcount / fail from decode.js and
// recoverManchester from decode_manchester.js (same global browser script scope).
//
// 1553 is a 1 Mbit/s bi-phase (Manchester II) bus; the DATA bits reuse
// recoverManchester. A 20-bit-time WORD = a 3-bit-time SYNC + 16 data bits (MSB
// first) + 1 odd-parity bit. The SYNC is a Manchester CODING VIOLATION spanning
// 3 bit-times: a command/status sync holds HIGH 1.5 bit-times then LOW 1.5; a
// data sync is the inverse (LOW then HIGH). 1553 maps "1 = high-then-low", i.e.
// a falling mid-cell transition = 1 and rising = 0 — the Thomas convention
// (ieee=false) of recoverManchester. cfg.bitrate>0 pins T; 0 auto-infers.
function decodeMIL1553(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const minSPB = 4;
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("mil1553", S.reason);
  if (S.edges.length < 2) return fail("mil1553", "too few edges");

  // Bit period T (samples/bit). cfg.bitrate pins it; otherwise infer from the edge
  // gaps as Manchester does, and resolve the T/2-vs-T ambiguity the same
  // deterministic way: mixed data has gaps at both base(=T/2) and 2·base(=T), while
  // alternating data (a 0xAAAA payload) has only base(=T) gaps. Choose from the gap
  // distribution and decode ONCE — never re-read at a half period.
  let T;
  if (cfg.bitrate > 0) {
    T = (1 / cfg.bitrate) / colTimeS;
  } else {
    const gaps = [];
    // sub-sample crossings: at small T the integer index quantizes a T/2 gap low.
    for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].x - S.edges[k - 1].x; if (g >= 1) gaps.push(g); }
    if (gaps.length < 3) return fail("mil1553", "too few edges / cannot infer bitrate");
    gaps.sort((a, b) => a - b);
    let base = gaps[Math.floor(gaps.length * 0.1)];
    let sum = 0, cnt = 0;
    for (const g of gaps) if (Math.abs(g - base) <= 0.5 * base) { sum += g; cnt++; } // ±0.5 window
    if (cnt) base = sum / cnt;
    let nBase = 0, n2 = 0;
    for (const g of gaps) {
      if (Math.abs(g - base) <= 0.5 * base) nBase++;
      else if (Math.abs(g - 2 * base) <= 0.5 * base) n2++;
    }
    T = (n2 > 0 && n2 * 8 >= nBase) ? 2 * base : base;
  }
  if (!isFinite(T) || !(T >= minSPB)) return fail("mil1553", T.toFixed(1) + " samples/bit; need >= " + minSPB);
  return decodeMIL1553At(S, T, colTimeS).res;
}

// decodeMIL1553At finds each word's SYNC and decodes it at bit period T, returning
// { res, words } so the caller can score competing T hypotheses.
function decodeMIL1553At(S, T, colTimeS) {
  const spans = [], bytes = [], toks = [];
  let words = 0;
  const hex4 = v => (v & 0xffff).toString(16).toUpperCase().padStart(4, "0");

  // Find each word's SYNC by its coding violation. In clean Manchester data no
  // level is ever held longer than one bit-time (T): a level held ~1.5T (up to
  // 2T when an equal-level neighbouring half-cell merges in) is only possible
  // inside a SYNC. Its mid transition is therefore the UNIQUE edge with a >1.25T
  // hold on BOTH sides — its two ~1.5T half-holds. That two-sided test rejects
  // ordinary data edges (gaps <=T) and the sync-start edge after an inter-message
  // idle (a huge hold on one side only). Words repeat; scan every interior edge.
  for (let k = 1; k < S.edges.length - 1; k++) {
    const gapBefore = S.edges[k].x - S.edges[k - 1].x;
    const gapAfter = S.edges[k + 1].x - S.edges[k].x;
    if (gapBefore < 1.25 * T || gapBefore > 2.5 * T) continue;
    if (gapAfter < 1.25 * T || gapAfter > 2.5 * T) continue;
    const syncMid = S.edges[k].x, syncStart = syncMid - 1.5 * T, syncEnd = syncMid + 1.5 * T;
    const firstHalf = logicAt(S, syncMid - 0.75 * T); // level of the sync's first 1.5T hold
    if (firstHalf < 0) continue;                      // sync clipped by the record start
    const isCmd = firstHalf === 1;                    // command sync high first; data sync low first

    // Recover the 17 cells (16 data + parity). ieee=false is the 1553 mapping
    // (rising mid = 0, falling = 1). Cap just past cell 17 so the sampler never
    // wanders into the next word.
    const rec = recoverManchester(S, syncEnd, T, false, syncEnd + 17.2 * T);
    const cells = rec.cells;
    if (cells.length < 17) continue;                  // word truncated by the record end
    let word = 0, bad = false;
    for (let c = 0; c < 16; c++) { if (cells[c].bit < 0) { bad = true; break; } word = (word << 1) | cells[c].bit; }
    if (bad) continue;
    // Odd parity: count of 1s across the 16 data bits + the parity bit is odd.
    const parityBit = cells[16].bit;
    const expect = 1 - (popcount(word) & 1);
    const parityOK = parityBit >= 0 && parityBit === expect;

    const label = isCmd ? "csync" : "dsync";
    let syncI0 = Math.round(syncStart); if (syncI0 < 0) syncI0 = 0;
    let syncI1 = Math.round(syncEnd) - 1; if (syncI1 >= S.n) syncI1 = S.n - 1; if (syncI1 < syncI0) syncI1 = syncI0;
    spans.push({ i0: syncI0, i1: syncI1, text: label, kind: "start" });
    const hw = hex4(word);
    spans.push({ i0: cells[0].i0, i1: cells[15].i1, text: hw, kind: "data", val: word });
    let pfx = "";
    if (!parityOK) { pfx = "!"; spans.push({ i0: cells[16].i0, i1: cells[16].i1, text: "!par", kind: "frame-error" }); }
    toks.push(label, pfx + hw);
    bytes.push(word);
    words++;
  }
  if (words === 0) return { res: fail("mil1553", "no MIL-STD-1553 sync found"), words: 0 };

  let baud = 0;
  if (colTimeS > 0) baud = Math.round(1 / (T * colTimeS));
  const res = { ok: true, error: null, proto: "mil1553", spans, text: toks.join(" "), bytes,
    meta: { bitrate: baud, samplesPerBit: T, threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail } };
  return { res, words };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeMIL1553 };
