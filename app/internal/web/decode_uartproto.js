// ENGMODEL-OWNER-UNIT: FU-WEB-DECODE
// decode_uartproto.js — the protocols framed on UART characters (LIN, DMX512,
// MIDI, Modbus RTU), a classic script mirroring decode_uartproto.go step for
// step so the web and the LCD agree byte for byte. Uses sliceChannel, logicAt,
// popcount, hex2, fmtByte, fail and inferUARTspb/snapStandardBaud from
// decode.js (globals in the browser's shared script scope).

// uartChars reads the characters of a line idling high (decode_uartproto.go
// uartChars): parity "even"/"odd"/"none", stops 1 or 2, breaks of breakBits.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function uartChars(S, spb, parity, stops, breakBits) {
  const out = [];
  const pc = parity !== "none" ? 1 : 0;
  let next = -Infinity;
  for (let k = 0; k < S.edges.length; k++) {
    const e = S.edges[k];
    if (e.dir >= 0 || e.x < next) continue;
    const x0 = e.x;
    if (logicAt(S, x0 + 0.5 * spb) !== 0) continue;
    let val = 0, off = false;
    for (let c = 0; c < 8; c++) {
      const b = logicAt(S, x0 + (1.5 + c) * spb);
      if (b < 0) { off = true; break; }
      val |= b << c;
    }
    if (off) break;
    let kind = "data";
    if (pc === 1) {
      const pb = logicAt(S, x0 + 9.5 * spb);
      if (pb < 0) break;
      let want = popcount(val) & 1;
      if (parity === "odd") want = 1 - want;
      if (pb !== want) kind = "parity-error";
    }
    const sb = logicAt(S, x0 + (9.5 + pc) * spb);
    if (sb < 0) break;
    const x1 = x0 + (9 + pc + stops) * spb;
    const ch = { x0, x1, val, kind, low: 0 };
    if (sb !== 1) {
      ch.kind = "frame-error";
      if (val === 0 && breakBits > 0) {
        const rise = k + 1 < S.edges.length ? S.edges[k + 1].x : S.n;
        const low = (rise - x0) / spb;
        if (low >= breakBits) {
          if (k + 1 >= S.edges.length) break;
          ch.kind = "break"; ch.low = low; ch.x1 = rise;
        }
      }
    }
    out.push(ch);
    next = ch.kind === "break" ? ch.x1 : ch.x1 - 0.5 * spb;
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function uiClamp(x, n) {
  const i = Math.round(x);
  return i < 0 ? 0 : (i > n - 1 ? n - 1 : i);
}

const LIN_RATES = [1200, 2400, 4800, 9600, 10417, 19200, 20000];
const LIN_BREAK_BITS = 11;

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function linPIDParity(id) {
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
  const b = (k) => (id >> k) & 1;
  const p0 = b(0) ^ b(1) ^ b(2) ^ b(4);
  const p1 = 1 - (b(1) ^ b(3) ^ b(4) ^ b(5));
  return (id & 0x3f) | (p0 << 6) | (p1 << 7);
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function linChecksum(pid, data, enhanced) {
  let s = enhanced ? pid : 0;
  for (const d of data) { s += d; if (s > 0xff) s -= 0xff; }
  return 0xff - s;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function linSyncSPB(S) {
  const cands = [];
  const E = S.edges;
  for (let k = 0; k + 11 < E.length; k++) {
    if (E[k].dir >= 0) continue;
    const f = E[k + 2].x, r9 = E[k + 11].x;
    const spb = (r9 - f) / 9;
    if (!(spb > 0) || E[k + 1].x - E[k].x < LIN_BREAK_BITS * spb || E[k + 2].dir >= 0) continue;
    let ok = true;
    for (let j = k + 2; j < k + 11; j++) {
      const g = E[j + 1].x - E[j].x;
      if (g < 0.6 * spb || g > 1.4 * spb) { ok = false; break; }
    }
    if (ok) cands.push(spb);
  }
  if (!cands.length) return 0;
  cands.sort((a, b) => a - b);
  return cands[cands.length >> 1];
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function snapSPB(spb, colTimeS, rates, tol) {
  if (!(spb > 0) || !(colTimeS > 0)) return spb;
  const baud = 1 / (spb * colTimeS);
  for (const r of rates) if (Math.abs(baud - r) <= tol * r) return 1 / (r * colTimeS);
  return spb;
}

// decodeLIN mirrors decode_uartproto.go DecodeLIN.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function decodeLIN(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("lin", S.reason);
  let spb = cfg.baud > 0 && colTimeS > 0 ? 1 / (cfg.baud * colTimeS) : snapSPB(linSyncSPB(S), colTimeS, LIN_RATES, 0.02);
  if (!(spb >= 3)) return fail("lin", "no LIN break and sync field");
  const chars = uartChars(S, spb, "none", 1, LIN_BREAK_BITS);
  const n = S.n, spans = [], toks = [], units = [];
  let good = false;
  // TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
  const unit = (c, v, txt, kind) => {
    if (kind !== "data") txt = "!" + txt;
    spans.push({ i0: uiClamp(c.x0, n), i1: uiClamp(c.x1, n), text: txt, kind, val: v });
    toks.push(txt); units.push(v);
  };
  for (let k = 0; k < chars.length; k++) {
    if (chars[k].kind !== "break") continue;
    const b = chars[k];
    spans.push({ i0: uiClamp(b.x0, n), i1: uiClamp(b.x1, n), text: "BRK", kind: "sof", val: 0 });
    toks.push("BRK");
    if (k + 1 >= chars.length || chars[k + 1].kind === "break") continue;
    const s = chars[k + 1];
    if (s.kind !== "data" || s.val !== 0x55) {
      spans.push({ i0: uiClamp(s.x0, n), i1: uiClamp(s.x1, n), text: "!SYNC", kind: "frame-error", val: s.val });
      toks.push("!SYNC");
      continue;
    }
    spans.push({ i0: uiClamp(s.x0, n), i1: uiClamp(s.x1, n), text: "SYNC", kind: "rw", val: 0x55 });
    toks.push("SYNC");
    k++;
    if (k + 1 >= chars.length || chars[k + 1].kind === "break" || chars[k + 1].x0 - s.x1 > 20 * spb) continue;
    k++;
    const p = chars[k];
    let pidKind = p.kind;
    if (pidKind === "data" && linPIDParity(p.val & 0x3f) !== p.val) pidKind = "parity-error";
    unit(p, p.val, "ID" + hex2(p.val & 0x3f), pidKind);
    const resp = [];
    let last = p.x1;
    while (k + 1 < chars.length && chars[k + 1].kind !== "break" && chars[k + 1].x0 - last <= 20 * spb) {
      k++; resp.push(chars[k]); last = chars[k].x1;
    }
    if (!resp.length) { good = good || pidKind === "data"; continue; }
    const data = [];
    for (const c of resp.slice(0, resp.length - 1)) { unit(c, c.val, fmtByte(c.val, cfg.format), c.kind); data.push(c.val); }
    const ck = resp[resp.length - 1];
    let kind = ck.kind;
    if (kind === "data") {
      const id = p.val & 0x3f;
      const enh = id !== 0x3c && id !== 0x3d && linChecksum(p.val, data, true) === ck.val;
      if (!enh && linChecksum(p.val, data, false) !== ck.val) kind = "frame-error";
    }
    unit(ck, ck.val, "CK" + hex2(ck.val), kind);
    good = good || (pidKind === "data" && kind === "data");
  }
  if (!spans.length) return fail("lin", "no LIN break");
  const r = { ok: good, proto: "lin", spans, text: toks.join(" "), bytes: units,
    meta: { baud: Math.round(1 / (spb * colTimeS)), spb, threshold: S.threshold } };
  if (!good) r.error = "no LIN frame with a valid identifier";
  return r;
}

const DMX_BREAK_BITS = 22;

// decodeDMX512 mirrors decode_uartproto.go DecodeDMX512.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function decodeDMX512(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("dmx512", S.reason);
  const baud = cfg.baud > 0 ? cfg.baud : 250000;
  if (!(colTimeS > 0)) return fail("dmx512", "no sample time");
  const spb = 1 / (baud * colTimeS);
  if (!(spb >= 3)) return fail("dmx512", spb.toFixed(1) + " samples/bit; need >= 3");
  const chars = uartChars(S, spb, "none", 2, DMX_BREAK_BITS);
  const n = S.n, spans = [], toks = [], units = [];
  let good = false;
  for (let k = 0; k < chars.length; k++) {
    if (chars[k].kind !== "break") continue;
    const b = chars[k];
    spans.push({ i0: uiClamp(b.x0, n), i1: uiClamp(b.x1, n), text: "BRK", kind: "sof", val: 0 });
    toks.push("BRK");
    if (k + 1 >= chars.length || chars[k + 1].kind === "break") continue;
    if (chars[k + 1].x0 - b.x1 >= 1.5 * spb) {
      spans.push({ i0: uiClamp(b.x1, n), i1: uiClamp(chars[k + 1].x0, n), text: "MAB", kind: "rw", val: 0 });
      toks.push("MAB");
    }
    let slot = 0, clean = true, last = b.x1;
    while (k + 1 < chars.length && chars[k + 1].kind !== "break" && chars[k + 1].x0 - last <= 44 * spb && slot <= 512) {
      k++;
      const c = chars[k];
      let txt = slot === 0 ? "SC" + hex2(c.val) : fmtByte(c.val, cfg.format);
      if (c.kind !== "data") { txt = "!" + txt; clean = false; }
      spans.push({ i0: uiClamp(c.x0, n), i1: uiClamp(c.x1, n), text: txt, kind: c.kind, val: c.val });
      toks.push(txt); units.push(c.val);
      last = c.x1;
      slot++;
    }
    if (slot >= 2 && clean) good = true;
  }
  if (!spans.length) return fail("dmx512", "no DMX512 break");
  const r = { ok: good, proto: "dmx512", spans, text: toks.join(" "), bytes: units, meta: { baud, spb, threshold: S.threshold } };
  if (!good) r.error = "no complete DMX512 packet";
  return r;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function midiDataLen(st) {
  switch (st >> 4) {
    case 0x8: case 0x9: case 0xA: case 0xB: case 0xE: return 2;
    case 0xC: case 0xD: return 1;
  }
  if (st === 0xF0) return -1;
  if (st === 0xF1 || st === 0xF3) return 1;
  if (st === 0xF2) return 2;
  return 0;
}

const MIDI_SYS = { 0xF0: "SYX", 0xF1: "MTC", 0xF2: "SPP", 0xF3: "SNG", 0xF6: "TUN", 0xF7: "EOX",
  0xF8: "CLK", 0xFA: "START", 0xFB: "CONT", 0xFC: "STOP", 0xFE: "SENS", 0xFF: "RST" };

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function midiName(st) {
  const ch = ["OFF", "ON", "PAT", "CC", "PC", "CAT", "PB"];
  if (st < 0xF0) return ch[(st >> 4) - 8] + ((st & 0xf) + 1);
  if (MIDI_SYS[st] !== undefined) return MIDI_SYS[st];
  return "F" + (st & 0xf).toString(16).toUpperCase();
}

// decodeMIDI mirrors decode_uartproto.go DecodeMIDI.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function decodeMIDI(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("midi", S.reason);
  const baud = cfg.baud > 0 ? cfg.baud : 31250;
  if (!(colTimeS > 0)) return fail("midi", "no sample time");
  const spb = 1 / (baud * colTimeS);
  if (!(spb >= 3)) return fail("midi", spb.toFixed(1) + " samples/bit; need >= 3");
  const chars = uartChars(S, spb, "none", 1, 0);
  const n = S.n, spans = [], toks = [], units = [];
  let run = 0, need = 0, got = 0, complete = 0;
  for (const c of chars) {
    const i0 = uiClamp(c.x0, n), i1 = uiClamp(c.x1, n);
    if (c.kind !== "data") {
      spans.push({ i0, i1, text: "!" + hex2(c.val), kind: "frame-error", val: c.val });
      toks.push("!" + hex2(c.val)); units.push(c.val);
      run = 0; need = 0; got = 0;
      continue;
    }
    const v = c.val;
    if (v >= 0xF8) {
      spans.push({ i0, i1, text: midiName(v), kind: "data", val: v });
      toks.push(midiName(v)); units.push(v);
      complete++;
      continue;
    }
    if (v >= 0x80) {
      const wasSyx = run === 0xF0;
      spans.push({ i0, i1: i0, text: "", kind: "sof", val: 0 });
      spans.push({ i0, i1, text: midiName(v), kind: "data", val: v });
      toks.push(midiName(v)); units.push(v);
      need = midiDataLen(v); got = 0;
      if (v < 0xF0 || v === 0xF0 || need > 0) run = v;
      else {
        if (v !== 0xF7 || wasSyx) complete++;
        run = 0;
      }
      continue;
    }
    units.push(v);
    if (run === 0 || (need === 0 && run !== 0xF0)) {
      spans.push({ i0, i1, text: "!" + hex2(v), kind: "frame-error", val: v });
      toks.push("!" + hex2(v));
      continue;
    }
    const txt = fmtByte(v, cfg.format);
    spans.push({ i0, i1, text: txt, kind: "data", val: v });
    toks.push(txt);
    if (need > 0) {
      got++;
      if (got === need) {
        complete++; got = 0;
        if (run >= 0xF0) { run = 0; need = 0; }
      }
    }
  }
  if (!spans.length) return fail("midi", "no MIDI bytes");
  const r = { ok: complete > 0, proto: "midi", spans, text: toks.join(" "), bytes: units, meta: { baud, spb, threshold: S.threshold } };
  if (!complete) r.error = "no complete MIDI message";
  return r;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function modbusCRC(b) {
  let c = 0xFFFF;
  for (const v of b) c = (c >>> 8) ^ MODBUS_CRC_TABLE[(c ^ v) & 0xff]; // a byte a step
  return c;
}

// MODBUS_CRC_TABLE is the reflected CRC-16 (0xA001) of each byte value: the
// eight shift steps of a byte done once, at load.
const MODBUS_CRC_TABLE = (() => {
  const t = new Uint16Array(256);
  for (let v = 0; v < 256; v++) {
    let c = v;
    for (let i = 0; i < 8; i++) c = (c & 1) ? (c >>> 1) ^ 0xA001 : c >>> 1;
    t[v] = c;
  }
  return t;
})();

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function modbusFrames(chars, spb) {
  const out = [];
  let cur = [];
  for (const c of chars) {
    if (cur.length && c.x0 - cur[cur.length - 1].x1 > 22 * spb) { out.push(cur); cur = []; }
    cur.push(c);
  }
  if (cur.length) out.push(cur);
  return out;
}

// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function modbusGood(frames) {
  let good = 0, bad = 0;
  for (const f of frames) {
    let ok = f.length >= 4;
    const vals = f.map((c) => c.val);
    for (const c of f) if (c.kind !== "data") { bad++; ok = false; }
    if (ok && modbusCRC(vals.slice(0, f.length - 2)) === (vals[f.length - 2] | (vals[f.length - 1] << 8))) good++;
  }
  return { good, bad };
}

// decodeModbus mirrors decode_uartproto.go DecodeModbus.
// TRLC-LINKS: REQ-SDS-018, REQ-SDS-232
function decodeModbus(codes, colTimeS, cfg) {
  cfg = cfg || {};
  const S = sliceChannel(codes, cfg);
  if (!S.ok) return fail("modbus", S.reason);
  let spb;
  if (cfg.baud > 0 && colTimeS > 0) spb = 1 / (cfg.baud * colTimeS);
  else {
    const inf = inferUARTspb(S);
    if (inf.reason) return fail("modbus", inf.reason);
    spb = snapStandardBaud(inf.spb, colTimeS);
  }
  if (!(spb >= 3)) return fail("modbus", spb.toFixed(1) + " samples/bit; need >= 3");
  let tries = [["even", 1], ["none", 2], ["odd", 1], ["none", 1]];
  if (cfg.parity === "even" || cfg.parity === "odd") tries = [[cfg.parity, 1]];
  else if (cfg.parity === "none") tries = [["none", 2]];
  let frames = [], parity = "", bestGood = -1, bestBad = 0;
  for (const [par, stops] of tries) {
    const f = modbusFrames(uartChars(S, spb, par, stops, 0), spb);
    const g = modbusGood(f);
    if (g.good > bestGood || (g.good === bestGood && g.bad < bestBad)) { frames = f; parity = par; bestGood = g.good; bestBad = g.bad; }
  }
  const n = S.n, spans = [], toks = [], units = [];
  for (const f of frames) {
    spans.push({ i0: uiClamp(f[0].x0, n), i1: uiClamp(f[0].x0, n), text: "", kind: "sof", val: 0 });
    const vals = f.map((c) => c.val);
    let clean = f.length >= 4;
    for (const c of f) clean = clean && c.kind === "data";
    const crcOK = clean && modbusCRC(vals.slice(0, f.length - 2)) === (vals[f.length - 2] | (vals[f.length - 1] << 8));
    f.forEach((c, i) => {
      let txt = fmtByte(c.val, cfg.format), kind = c.kind;
      if (i === 0) txt = "A" + hex2(c.val);
      else if (i === 1) { txt = "F" + hex2(c.val); if (c.val >= 0x80) txt += "!"; }
      else if (i >= f.length - 2 && f.length >= 4) { txt = hex2(c.val); if (kind === "data" && !crcOK) kind = "frame-error"; }
      if (kind !== "data") txt = "!" + txt;
      spans.push({ i0: uiClamp(c.x0, n), i1: uiClamp(c.x1, n), text: txt, kind, val: c.val });
      toks.push(txt); units.push(c.val);
    });
  }
  if (!spans.length) return fail("modbus", "no characters");
  const r = { ok: bestGood > 0, proto: "modbus", spans, text: toks.join(" "), bytes: units,
    meta: { baud: Math.round(1 / (spb * colTimeS)), spb, threshold: S.threshold, parity } };
  if (!(bestGood > 0)) r.error = "no CRC-valid Modbus RTU frame";
  return r;
}

if (typeof module !== "undefined" && module.exports)
  module.exports = { decodeLIN, decodeDMX512, decodeMIDI, decodeModbus };
