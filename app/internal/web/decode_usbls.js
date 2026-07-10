// USB low/full-speed decoder — JS twin of decode_usbls.go, kept algorithm-
// faithful so the web overlay and the on-device LCD agree byte-for-byte. Classic
// script: no imports/exports; reuses sliceChannel / logicAt / hex2 / fail from
// decode.js (same global script scope in the browser).
//
// Single channel = the D+ single-ended line, where the J/K bus states appear as
// the two logic levels. Line coding is NRZI: a 0 bit is a level TRANSITION, a 1
// bit is NO transition. Bit stuffing inserts a 0 after six consecutive 1s
// (dropped on decode). A packet is SYNC (00000001 = KJKJKJKK) + PID (4 bits +
// 4-bit complement) + optional data/CRC + EOP (~2 bit-times of SE0, which on the
// single D+ line reads as an extended idle that, with the inter-packet idle,
// bounds the packet). cfg.bitrate>0 pins the bit period; 0 auto-infers it.

// usbPIDName maps a 4-bit PID value (PID3..PID0, the low nibble sent LSB-first)
// to its packet name. The 8-bit PID byte is this nibble then its ones-complement.
const usbPIDName = {
  0x1: "OUT", 0x9: "IN", 0x5: "SOF", 0xD: "SETUP",
  0x3: "DATA0", 0xB: "DATA1", 0x7: "DATA2", 0xF: "MDATA",
  0x2: "ACK", 0xA: "NAK", 0xE: "STALL", 0x6: "NYET",
  0xC: "PRE", 0x8: "SPLIT", 0x4: "PING",
};

function decodeUSBLS(dp, colTimeS, cfg) {
  cfg = cfg || {};
  const minSPB = 4;       // samples per bit floor
  const eopCells = 2;     // EOP ~ 2 bit-times of SE0 trailing every packet
  const splitK = 10;      // inter-packet idle: a gap wider than this many bit periods
  const S = sliceChannel(dp, cfg);
  if (!S.ok) return fail("usbls", S.reason);
  if (S.edges.length < 8) return fail("usbls", "too few edges"); // SYNC carries 7 transitions

  // Bit period T (samples/bit). cfg.bitrate pins it; otherwise infer it as the
  // shortest level-run: NRZI edge gaps are integer multiples of one bit period
  // (1..7, capped by bit stuffing), so the shortest cluster is T.
  let T;
  if (cfg.bitrate > 0) {
    T = (1 / cfg.bitrate) / colTimeS;
  } else {
    const gaps = [];
    for (let k = 1; k < S.edges.length; k++) { const g = S.edges[k].i - S.edges[k - 1].i; if (g >= 1) gaps.push(g); }
    if (gaps.length < 3) return fail("usbls", "too few edges / cannot infer bitrate");
    gaps.sort((a, b) => a - b);
    let est = gaps[Math.floor(gaps.length * 0.1)];
    let sum = 0, cnt = 0;
    for (const g of gaps) if (Math.abs(g - est) <= 0.4 * est) { sum += g; cnt++; }
    if (cnt) est = sum / cnt;
    T = est;
  }
  if (!isFinite(T) || !(T >= minSPB)) return fail("usbls", T.toFixed(1) + " samples/bit; need >= " + minSPB);

  // Segment edges into PACKETS on the inter-packet idle. Within a packet bit
  // stuffing guarantees a transition at least every ~7 bit periods; the EOP then
  // the idle line open a much wider gap, so split where consecutive edges are
  // more than splitK bit periods apart.
  const segs = [];
  let segStart = 0;
  for (let k = 1; k < S.edges.length; k++) {
    if (S.edges[k].i - S.edges[k - 1].i > splitK * T) { segs.push([segStart, k - 1]); segStart = k; }
  }
  segs.push([segStart, S.edges.length - 1]);

  const n = S.n;
  const clampI = i => (i < 0 ? 0 : (i >= n ? n - 1 : i));
  const cellStart = (x0, k) => clampI(Math.round(x0 + k * T));
  const cellEnd = (x0, k) => clampI(Math.round(x0 + (k + 1) * T) - 1);

  const spans = [], bytes = [], toks = [];
  let packets = 0;
  for (let sgIdx = 0; sgIdx < segs.length; sgIdx++) {
    const sg = segs[sgIdx];
    if (sg[1] <= sg[0]) continue;              // a lone edge carries no packet
    const x0 = S.edges[sg[0]].x, x1 = S.edges[sg[1]].x;
    let nCells = Math.round((x1 - x0) / T);
    if (nCells < 8 + 8 + eopCells) continue;   // SYNC + PID + EOP minimum
    if (nCells > n) nCells = n;                // safety cap
    // A record that ends mid-transmission leaves the trailing packet with no
    // EOP/idle to close it — require trailing idle on the last segment.
    if (sgIdx === segs.length - 1 && (n - 1 - S.edges[sg[1]].i) < 2 * T) continue;

    // NRZI-decode the cells: idle level seeds the first comparison, then a level
    // change vs the previous cell = 0, no change = 1.
    let idleX = x0 - 0.5 * T;
    if (idleX < 0) idleX = 0;
    if (idleX > n - 1) idleX = n - 1;
    let prev = logicAt(S, idleX);
    if (prev < 0) prev = 0;
    const rawBits = [], rawCell = [];
    for (let k = 0; k < nCells; k++) {
      const lv = logicAt(S, x0 + (k + 0.5) * T);
      if (lv < 0) break;                       // ran off the captured region
      const b = (lv !== prev) ? 0 : 1;
      prev = lv;
      rawBits.push(b); rawCell.push(k);
    }
    // Strip the trailing EOP cells (SE0 reads as an extended idle bound here).
    if (rawBits.length < eopCells + 16) continue;
    rawBits.length -= eopCells;
    rawCell.length -= eopCells;

    // De-stuff: the 0 inserted after six consecutive 1s is dropped. Track each
    // kept bit's raw cell so spans map back to sample indices.
    const bitsArr = [], cellOf = [];
    let ones = 0;
    for (let i = 0; i < rawBits.length; i++) {
      if (ones === 6) {                    // a 0 is stuffed after six 1s; a SEVENTH 1
        if (rawBits[i] !== 0) break;       // is a stuff violation = idle after EOP: stop
        ones = 0; continue;                // drop the stuffed 0
      }
      bitsArr.push(rawBits[i]); cellOf.push(rawCell[i]);
      if (rawBits[i] === 1) ones++; else ones = 0;
    }
    if (bitsArr.length < 16) continue;
    // SYNC must be 00000001; anything else is a partial/garbage frame — drop it.
    let syncOK = bitsArr[7] === 1;
    for (let i = 0; i < 7; i++) if (bitsArr[i] !== 0) syncOK = false;
    if (!syncOK) continue;
    // PID = 8 bits LSB-first: low nibble = PID, high nibble = its complement.
    let pidByte = 0;
    for (let i = 0; i < 8; i++) pidByte |= bitsArr[8 + i] << i;
    const pid4 = pidByte & 0xF, check = (pidByte >> 4) & 0xF;
    const name = usbPIDName[pid4] || ("PID" + pid4.toString(16).toUpperCase());
    const pidBad = check !== (~pid4 & 0xF);

    if (packets > 0) {                         // separate packets in the transcript
      spans.push({ i0: cellStart(x0, cellOf[0]), i1: cellStart(x0, cellOf[0]), text: "", kind: "gap" });
      toks.push("|");
    }
    packets++;
    spans.push({ i0: cellStart(x0, cellOf[0]), i1: cellEnd(x0, cellOf[7]), text: "SYNC", kind: "start" });
    const pidText = pidBad ? "!" + name : name, pidKind = pidBad ? "frame-error" : "addr";
    spans.push({ i0: cellStart(x0, cellOf[8]), i1: cellEnd(x0, cellOf[15]), text: pidText, kind: pidKind, val: pid4 });
    toks.push(pidText);

    // Payload bytes (data + CRC) after the PID, LSB-first.
    const nBytes = (bitsArr.length - 16) >> 3;
    for (let b = 0; b < nBytes; b++) {
      const base = 16 + b * 8;
      let val = 0;
      for (let i = 0; i < 8; i++) val |= bitsArr[base + i] << i;
      spans.push({ i0: cellStart(x0, cellOf[base]), i1: cellEnd(x0, cellOf[base + 7]), text: hex2(val), kind: "data", val });
      toks.push(hex2(val)); bytes.push(val);
    }
  }
  if (packets === 0) return fail("usbls", "no USB packet (SYNC+PID) found");

  let baud = cfg.bitrate || 0;
  if (colTimeS > 0) baud = Math.round(1 / (T * colTimeS));
  return { ok: true, error: null, proto: "usbls", spans, text: toks.join(" "), bytes,
    meta: { bitrate: baud, samplesPerBit: T, threshold: S.threshold, lowRail: S.lowRail, highRail: S.highRail } };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { usbPIDName, decodeUSBLS };
