// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE-CANFD
// CAN / CAN-FD decoder — the JS twin of Go internal/decode/decode_canfd.go.
// Kept algorithm-faithful (same slicing/destuffing/CRC) so the web overlay and
// the on-device LCD agree byte-for-byte. Classic CAN is decoded fully; CAN-FD
// base frames are best-effort (ID + control + DLC + data with dynamic
// destuffing, flagged FD). A single sliced logic line: dominant = logic 0,
// recessive = logic 1; dominantLow (default true) maps dominant to the LOW rail.
//
// Depends on sliceChannel / logicAt / fmtByte from decode.js (loaded first in
// the browser; pulled from the module under node).
if (typeof sliceChannel === "undefined" && typeof require !== "undefined") {
  const _d = require("./decode.js");
  globalThis.sliceChannel = _d.sliceChannel;
  globalThis.logicAt = _d.logicAt;
  globalThis.fmtByte = _d.fmtByte;
}

// The recessive run (bits) a SOF must follow: longer than bit stuffing lets a
// frame hold one (Go canSOFIdleBits).
const CAN_SOF_IDLE_BITS = 5.5;

// The common CAN bit rates an inferred rate snaps to (Go canStandardBauds).
const CAN_STANDARD_BAUDS = [10000, 20000, 50000, 83333, 100000, 125000, 250000, 500000, 800000, 1000000];

// canSnapBaud moves an inferred samples-per-bit onto the nearest standard CAN
// rate within 1.5%; otherwise the estimate stands (Go snapCANBaud).
// TRLC-LINKS: REQ-SDS-018
function canSnapBaud(spb, colTimeS) {
  if (!(spb > 0) || !(colTimeS > 0)) return spb;
  const baud = 1 / (spb * colTimeS);
  for (const std of CAN_STANDARD_BAUDS) if (Math.abs(baud - std) <= 0.015 * std) return 1 / (std * colTimeS);
  return spb;
}

// TRLC-LINKS: REQ-SDS-018
function canFail(reason) {
  return { ok: false, error: reason, proto: "canfd", spans: [], text: "", bytes: [] };
}

// canCRC15: classic-CAN CRC-15 (poly 0x4599) over the destuffed SOF..data bits.
// TRLC-LINKS: REQ-SDS-018
function canCRC15(bits) {
  let crc = 0;
  for (let k = 0; k < bits.length; k++) {
    const next = ((crc >> 14) & 1) ^ (bits[k] & 1);
    crc = (crc << 1) & 0x7fff;
    if (next) crc ^= 0x4599;
  }
  return crc & 0x7fff;
}

// fdDataLen maps a CAN-FD DLC (0..15) to its byte count.
// TRLC-LINKS: REQ-SDS-018
function fdDataLen(dlc) {
  if (dlc <= 8) return dlc;
  return { 9: 12, 10: 16, 11: 20, 12: 24, 13: 32, 14: 48, 15: 64 }[dlc] || 64;
}

// canReadRaw samples one wire bit at pos+0.5*spb (0=dominant,1=recessive) and
// advances pos by spb. Returns -1 out of range.
// TRLC-LINKS: REQ-SDS-018
function canResync(r) {
  // Re-synchronize as a CAN controller does: an edge within 0.4 bit of the
  // expected boundary becomes the boundary (free-running from SOF, a 2 %
  // bit-time error slips bits at 8 samples a bit).
  const E = r.S.edges, w = 0.4 * r.spb;
  if (r.ei === undefined) r.ei = 0;
  while (r.ei < E.length && E[r.ei].x < r.pos - w) r.ei++;
  if (r.ei < E.length && Math.abs(E[r.ei].x - r.pos) <= w) r.pos = E[r.ei].x;
}

// TRLC-LINKS: REQ-SDS-018
function canReadRaw(r) {
  canResync(r);
  const center = r.pos + 0.5 * r.spb;
  r.li0 = Math.round(r.pos);
  let end = Math.round(r.pos + r.spb) - 1;
  if (end >= r.S.n) end = r.S.n - 1;
  if (end < r.li0) end = r.li0;
  r.li1 = end;
  r.pos += r.spb;
  const S = r.S, ci = Math.round(center); // logicAt inline
  if (ci < 0 || ci >= S.n || S.codes[ci] < 0) return -1;
  const lvl = S.codes[ci] >= S.threshold ? 1 : 0;
  return r.dominantLow ? lvl : (1 - lvl);
}

// canNext returns the next destuffed bit, dropping a stuff bit after 5 identical.
// TRLC-LINKS: REQ-SDS-018
function canNext(r) {
  if (r.stuffOn && r.runLen >= 5) {
    const sv = canReadRaw(r);
    if (sv < 0) return -1;
    if (sv === r.runVal) r.stuffErr = true; // 6th identical bit = a stuff violation
    r.stuffed++;
    r.runVal = sv; r.runLen = 1;
  }
  const v = canReadRaw(r);
  if (v < 0) return -1;
  if (v === r.runVal) r.runLen++;
  else { r.runVal = v; r.runLen = 1; }
  if (r.record) r.bits.push(v);
  return v;
}

// canReadField reads nbits destuffed bits MSB-first. Returns {val,i0,i1,ok}.
// TRLC-LINKS: REQ-SDS-018
function canReadField(r, nbits) {
  let val = 0, i0 = -1, i1 = -1;
  for (let k = 0; k < nbits; k++) {
    const b = canNext(r);
    if (b < 0) return { val: 0, i0, i1, ok: false };
    if (k === 0) i0 = r.li0;
    i1 = r.li1;
    val = (val << 1) | b;
  }
  return { val, i0, i1, ok: true };
}

// canInferSPB — deterministic cluster walk (mirrors decode_canfd.go's
// inferCANspb, which mirrors inferUARTspb): a low-transition payload (0x33/
// 0xCC patterns) can leave ONE single-bit gap in a sea of 2-bit ones, and
// the old blind low-percentile seeded on the 2-bit cluster and halved the
// rate (found by the sigrok oracle). Each ascending gap cluster is tried as
// the 1-bit hypothesis, refined by re-centered mean, validated by the
// fraction of gaps explained as integer bit multiples; ties go to the larger
// period. Gaps beyond ~16 candidate bits are idle spacing, not evidence.
// TRLC-LINKS: REQ-SDS-018
function canInferSPB(S) {
  const gaps = [];
  for (let k = 1; k < S.edges.length; k++) {
    const g = S.edges[k].i - S.edges[k - 1].i;
    if (g >= 2) gaps.push(g);
  }
  if (gaps.length < 3) return { spb: 0, reason: "too few edges / cannot infer baud" };
  gaps.sort((a, b) => a - b);
  const cands = [];
  for (let i = 0; i < gaps.length; ) {
    const seed = gaps[i];
    let sum = 0, j = i;
    for (; j < gaps.length && gaps[j] <= 1.5 * seed; j++) sum += gaps[j];
    cands.push(sum / (j - i));
    i = j;
  }
  let best = 0, bestFrac = -1;
  for (const cand of cands) {
    if (cand < 2.5) continue; // below the samples/bit floor: a spur cluster
    const kg = gaps.filter((g) => g <= 16 * cand);
    if (kg.length < 3) continue;
    let ref = cand;
    for (let pass = 0; pass < 2; pass++) {
      let sum = 0, cnt = 0;
      for (const g of kg) if (Math.abs(g - ref) <= 0.35 * ref) { sum += g; cnt++; }
      if (cnt) ref = sum / cnt;
    }
    let good = 0;
    for (const g of kg) {
      const m = Math.round(g / ref);
      if (m >= 1 && Math.abs(g - m * ref) <= 0.35 * ref) good++;
    }
    const frac = good / kg.length;
    // >= keeps the LARGER candidate on an exact tie (candidates ascend).
    if (frac >= 0.7 && frac >= bestFrac) { best = ref; bestFrac = frac; }
  }
  if (best > 0) return { spb: best, reason: "" };
  return { spb: 0, reason: "baud ambiguous — set it explicitly" };
}

// canOneFrame decodes a single frame starting at sofStart (fractional samples).
// Returns { ok, spans, toks, bytes, endI }.
// TRLC-LINKS: REQ-SDS-018
function canOneFrame(S, dominantLow, sofStart, spb, dataSpb) {
  // Every field from the start (ei too): a field added later changes the
  // object's shape under the per-bit reads.
  const r = { S, dominantLow, pos: sofStart, spb, runVal: -1, runLen: 0,
    stuffOn: true, record: true, stuffed: 0, stuffErr: false, bits: [], li0: -1, li1: -1, ei: 0 };
  const fail = { ok: false };
  const spans = [], toks = [];

  // SOF — one dominant bit.
  const sof = canReadField(r, 1);
  if (!sof.ok || sof.val !== 0) return fail;
  spans.push({ i0: sof.i0, i1: sof.i1, text: "SOF", kind: "sof", val: 0 });

  // 11-bit base identifier.
  const base = canReadField(r, 11);
  if (!base.ok) return fail;
  const idI0 = base.i0;
  let baseI1 = base.i1;

  const b1 = canReadField(r, 1);   // RTR (standard) or SRR (extended)
  if (!b1.ok) return fail;
  const ideF = canReadField(r, 1); // IDE
  if (!ideF.ok) return fail;

  const extended = ideF.val === 1;
  let remote = false, fd = false;
  let id = base.val, idEnd = baseI1;

  if (!extended) {
    const rtr = b1.val;
    const fdf = canReadField(r, 1); // r0 (classic) or FDF/EDL (FD)
    if (!fdf.ok) return fail;
    if (fdf.val === 1) {
      fd = true;
      if (!canReadField(r, 1).ok) return fail; // res (r0)
      const brs = canReadField(r, 1);
      if (!brs.ok) return fail;
      if (brs.val === 1 && dataSpb !== spb) r.spb = dataSpb;
      if (!canReadField(r, 1).ok) return fail; // ESI
    } else {
      remote = rtr === 1;
    }
  } else {
    const ext = canReadField(r, 18);
    if (!ext.ok) return fail;
    id = (base.val << 18) | ext.val;
    idEnd = ext.i1;
    const rtr = canReadField(r, 1);
    if (!rtr.ok) return fail;
    if (!canReadField(r, 1).ok) return fail; // r1
    if (!canReadField(r, 1).ok) return fail; // r0
    remote = rtr.val === 1;
  }

  const idText = (id >>> 0).toString(16).toUpperCase();
  spans.push({ i0: idI0, i1: idEnd, text: idText, kind: "id", val: id });
  spans.push({ i0: idI0, i1: idEnd, text: extended ? "EXT" : "STD", kind: "ide", val: extended ? 1 : 0 });
  spans.push({ i0: idI0, i1: idEnd, text: remote ? "RTR" : "DATA", kind: "rtr", val: remote ? 1 : 0 });
  toks.push((extended ? "XID:" : "ID:") + idText);
  if (remote) toks.push("RTR");
  if (fd) { spans.push({ i0: idI0, i1: idEnd, text: "FD", kind: "fd", val: 1 }); toks.push("FD"); }

  // DLC.
  const dlcF = canReadField(r, 4);
  if (!dlcF.ok) return fail;
  const dlc = dlcF.val;
  let nBytes = fd ? fdDataLen(dlc) : Math.min(dlc, 8);
  if (remote) nBytes = 0;
  spans.push({ i0: dlcF.i0, i1: dlcF.i1, text: "DLC:" + dlc, kind: "dlc", val: dlc });
  toks.push("DLC:" + dlc);

  // Data.
  const bytes = [];
  for (let k = 0; k < nBytes; k++) {
    const d = canReadField(r, 8);
    if (!d.ok) return fail;
    spans.push({ i0: d.i0, i1: d.i1, text: fmtByte(d.val, "hex"), kind: "data", val: d.val });
    toks.push(fmtByte(d.val, "hex"));
    bytes.push(d.val);
  }

  if (fd) {
    // Best-effort: FD stuff-count / longer CRC / distinct ACK form are not parsed.
    return { ok: true, spans, toks, bytes, endI: Math.round(r.pos) };
  }

  // Classic CRC-15 over the recorded SOF..data bits.
  const want = canCRC15(r.bits);
  r.record = false;
  const crcF = canReadField(r, 15);
  if (!crcF.ok) return fail;
  let crcTxt = crcF.val.toString(16).toUpperCase().padStart(4, "0");
  let crcKind = "crc";
  if (crcF.val !== want || r.stuffErr) { crcTxt = "!" + crcTxt; crcKind = "frame-error"; } // CRC mismatch OR stuff violation
  spans.push({ i0: crcF.i0, i1: crcF.i1, text: "CRC:" + crcTxt, kind: crcKind, val: crcF.val });
  toks.push("CRC:" + crcTxt);

  // CRC delimiter (stuffing still on to swallow a trailing stuff bit), then ACK.
  const crcOK = crcKind === "crc";
  if (!canReadField(r, 1).ok) return { ok: true, spans, toks, bytes, endI: Math.round(r.pos), crcOK };
  r.stuffOn = false;
  const ackF = canReadField(r, 1);
  if (ackF.ok) {
    if (ackF.val === 0) { spans.push({ i0: ackF.i0, i1: ackF.i1, text: "ACK", kind: "ack", val: 0 }); toks.push("ACK"); }
    else { spans.push({ i0: ackF.i0, i1: ackF.i1, text: "NAK", kind: "nak", val: 1 }); toks.push("NAK"); }
  }

  return { ok: true, spans, toks, bytes, endI: Math.round(r.pos), crcOK };
}

// TRLC-LINKS: REQ-SDS-018
function decodeCANFD(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const dominantLow = cfg.dominantLow !== false; // default true
  const S = sliceChannel(codes, { threshold: cfg.haveThr ? cfg.threshold : undefined });
  if (!S.ok) return canFail(S.reason);

  let spb;
  if (cfg.nominalBaud > 0) {
    if (!(colTimeS > 0)) return canFail("invalid colTimeS for explicit baud");
    spb = (1 / cfg.nominalBaud) / colTimeS;
  } else {
    const inf = canInferSPB(S);
    if (inf.reason) return canFail(inf.reason);
    spb = canSnapBaud(inf.spb, colTimeS);
  }
  if (!(spb >= 3)) return canFail(spb.toFixed(1) + " samples/bit; need >= 3");
  let dataSpb = spb;
  if (cfg.dataBaud > 0 && colTimeS > 0) {
    const ds = (1 / cfg.dataBaud) / colTimeS;
    if (ds >= 3) dataSpb = ds;
  }

  const sofDir = dominantLow ? -1 : 1;
  const spans = [], toks = [], bytes = [];
  let frames = 0, nextAllowed = 0;
  for (let k = 0; k < S.edges.length; k++) {
    const e = S.edges[k];
    if (e.dir !== sofDir || e.i < nextAllowed) continue;
    // a SOF follows a recessive run longer than stuffing allows (5 bits): a
    // record opening inside a frame reads no SOF there; at the record's start
    // a shorter run keeps only a frame whose CRC checks (Go canIdleBefore)
    const idle = e.x - (k > 0 ? S.edges[k - 1].x : 0) >= CAN_SOF_IDLE_BITS * spb;
    if (!idle && k > 0) continue;
    if (frames >= 4096) break;
    const fr = canOneFrame(S, dominantLow, e.x, spb, dataSpb);
    if (!fr.ok || (!idle && !fr.crcOK)) continue;
    frames++;
    for (const s of fr.spans) spans.push(s);
    for (const t of fr.toks) toks.push(t);
    for (const b of fr.bytes) bytes.push(b);
    nextAllowed = fr.endI + 1;
  }
  if (frames === 0) return canFail("no CAN frame found");
  return { ok: true, error: null, proto: "canfd", spans, text: toks.join(" "), bytes,
    meta: { threshold: S.threshold, samplesPerBit: spb, frames } };
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeCANFD, canCRC15, fdDataLen };
