// ENGMODEL-OWNER-UNIT: FU-WEB-PACKETS
// packets.js — groups a decoder Result (decode.js / decode_*.js) into packets
// (messages) for BUS's packet table, and keeps the list still while the same
// messages repeat. Pure: no DOM, no globals; loaded by ui.html and require()d
// by packets.test.cjs. The Go twin for the LCD is analysis.Packets/PacketList.
//
// Grouping, per protocol family:
//   * I²C: START .. STOP (a repeated START closes the packet and opens the next),
//     with the address and R/W from the addr/rw spans;
//   * framed protocols (CAN sof, SENT sync, USB/Manchester/MIL/FlexRay start):
//     a delimiter span opens a packet; the next delimiter or an idle gap ends it;
//   * everything else (UART, SPI without chip select, ARINC 429 words): bursts of
//     data separated by an idle gap longer than 1.5 unit times.
// A packet is COMPLETE when nothing of it can lie outside the record: it opens
// on its delimiter (or after an idle gap inside the record), and closes on its
// terminator (or is followed by an idle gap inside the record). Spans the
// decoder marked "gap" (cut by the record edge) make their packet incomplete.
"use strict";

const PK_OPEN = new Set(["start", "sof", "sync"]);
const PK_ERR = new Set(["frame-error", "parity-error", "crc-error", "nak", "error"]);

// pkUnit: a typical unit (byte/word) length in samples — the median data span.
// TRLC-LINKS: REQ-SDS-214
function pkUnit(spans) {
  const d = spans.filter((s) => s.kind === "data" && s.i1 > s.i0).map((s) => s.i1 - s.i0).sort((a, b) => a - b);
  if (d.length) return d[d.length >> 1];
  const all = spans.filter((s) => s.i1 > s.i0).map((s) => s.i1 - s.i0).sort((a, b) => a - b);
  return all.length ? all[all.length >> 1] : 1;
}

// TRLC-LINKS: REQ-SDS-214
function pkNew(i0) {
  return { i0, i1: i0, bytes: [], byteErr: [], addr: -1, hasAddr: false, read: false, complete: false, err: "", opened: false, closed: false };
}

// decodePackets groups r.spans into packets. recordLen is the sample count of
// the decoded record (the margins the completeness test checks against).
// TRLC-LINKS: REQ-SDS-214
function decodePackets(r, recordLen) {
  if (!r || !r.ok || !r.spans || !r.spans.length) return [];
  const spans = r.spans.slice().sort((a, b) => a.i0 - b.i0);
  const n = recordLen > 0 ? recordLen : spans[spans.length - 1].i1 + 1;
  const unit = Math.max(1, pkUnit(spans));
  const gapThr = 1.5 * unit;
  const out = [];
  let cur = null, lastEnd = -Infinity, lastWasGap = false;
  // TRLC-LINKS: REQ-SDS-214
  const close = (closedByDelim) => {
    if (!cur) return;
    cur.closed = cur.closed || closedByDelim;
    out.push(cur);
    cur = null;
  };
  const i2c = r.proto === "i2c";
  const framed = !i2c && spans.some((s) => PK_OPEN.has(s.kind));
  for (const s of spans) {
    if (s.kind === "gap") { // the decoder could not complete this unit
      if (cur) cur.cut = true;
      lastWasGap = true; lastEnd = s.i1;
      continue;
    }
    if (s.kind === "idle") continue;
    if (i2c) {
      if (s.kind === "start") {
        if (cur) { cur.closed = true; cur.i1 = s.i0; close(true); } // repeated START
        cur = pkNew(s.i0); cur.opened = true;
        continue;
      }
      if (s.kind === "stop") {
        if (cur) { cur.i1 = s.i1; close(true); }
        continue;
      }
      if (!cur) { cur = pkNew(s.i0); } // data without its START: cut by the record start
      if (s.kind === "addr") { cur.addr = parseInt(s.text, 16); cur.hasAddr = Number.isFinite(cur.addr); }
      else if (s.kind === "rw") cur.read = s.text === "R";
      else if (s.kind === "data") { cur.bytes.push(s.val & 0xff); cur.byteErr.push(false); }
      else if (s.kind === "nak" && cur.bytes.length === 0 && cur.hasAddr) cur.err = "address NAK";
      else if (s.kind === "frame-error") cur.cut = true; // "(no STOP)" at the record end
      cur.i1 = Math.max(cur.i1, s.i1);
      continue;
    }
    const gapBefore = s.i0 - lastEnd;
    const opens = framed ? PK_OPEN.has(s.kind) : false;
    if (cur && (opens || gapBefore > gapThr)) close(opens);
    if (!cur) {
      cur = pkNew(s.i0);
      // complete at the start: it opens on a delimiter, or idle precedes it inside the record
      cur.opened = framed ? opens : (s.i0 >= gapThr && !lastWasGap && (lastEnd === -Infinity || gapBefore > gapThr));
    }
    lastWasGap = false;
    if (s.kind === "data" || PK_ERR.has(s.kind)) {
      if (s.val !== undefined && s.val !== null && Number.isFinite(+s.val)) {
        cur.bytes.push(+s.val);
        const bad = s.kind !== "data" || String(s.text || "").startsWith("!");
        cur.byteErr.push(bad);
        if (bad && !cur.err) cur.err = s.kind;
      } else if (PK_ERR.has(s.kind) && !cur.err) cur.err = s.kind;
    } else if (s.kind === "addr") {
      const a = parseInt(s.text, 16);
      if (Number.isFinite(a)) { cur.addr = a; cur.hasAddr = true; }
    }
    cur.i1 = Math.max(cur.i1, s.i1);
    lastEnd = s.i1;
  }
  if (cur) close(false);
  // closing edge: a terminator closed it, or idle follows it inside the record
  for (let k = 0; k < out.length; k++) {
    const p = out[k], next = out[k + 1];
    // (a packet followed by another was closed by its delimiter or an idle gap;
    // the last one needs idle before the record ends)
    if (i2c) p.complete = p.opened && p.closed && !p.cut;
    else p.complete = p.opened && !p.cut && (next ? true : n - 1 - p.i1 >= gapThr);
    delete p.opened; delete p.closed; delete p.cut;
  }
  return out;
}

// TRLC-LINKS: REQ-SDS-214
function pkHex(p) { return p.bytes.map((b, i) => (p.byteErr[i] ? "!" : "") + (b > 0xff ? b.toString(16).toUpperCase() : b.toString(16).toUpperCase().padStart(2, "0"))).join(" "); }
// pkString renders the bytes as text: printable ASCII as is, anything else as ".".
// TRLC-LINKS: REQ-SDS-214
function pkString(p) { return p.bytes.map((b) => (b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : ".")).join(""); }
// TRLC-LINKS: REQ-SDS-214
function pkKey(p) { return (p.hasAddr ? p.addr + (p.read ? "R" : "W") + ":" : "") + pkHex(p) + (p.err ? "|" + p.err : ""); }

// pkMatch reports whether a packet holds the pattern (serial-trigger tokens:
// hex units, "??" any unit, "A?" a nibble wildcard, "ERR" an errored unit).
// TRLC-LINKS: REQ-SDS-214
function pkMatch(p, tokens) {
  if (!tokens || !tokens.length || tokens.length > p.bytes.length) return false;
  // TRLC-LINKS: REQ-SDS-214
  const one = (tok, i) => {
    if (/^err(or)?$/i.test(tok)) return !!p.byteErr[i];
    if (p.byteErr[i]) return false;
    const h = p.bytes[i].toString(16).toUpperCase().padStart(tok.length, "0");
    if (h.length !== tok.length) return false;
    for (let k = 0; k < tok.length; k++) if (tok[k] !== "?" && tok[k].toUpperCase() !== h[k]) return false;
    return true;
  };
  for (let i = 0; i + tokens.length <= p.bytes.length; i++) {
    let all = true;
    for (let j = 0; j < tokens.length && all; j++) all = one(tokens[j], i + j);
    if (all) return true;
  }
  return false;
}

// PacketList keeps the rows of the packet table. Only complete packets are
// listed. When a new capture brings only messages already listed (the same byte
// sequences, in any number, possibly shifted), the rows are NOT replaced, so
// the table holds still while traffic repeats; their positions follow the new
// capture. New content replaces the rows. Rows keep an id while their content
// stays listed, and the selection follows a packet by content.
// TRLC-LINKS: REQ-SDS-214
class PacketList {
  // TRLC-LINKS: REQ-SDS-214
  constructor(max) { this.rows = []; this.selKey = null; this.nextId = 1; this.max = max || 200; this.version = 0; }

  // update returns true when the rows were replaced.
  // TRLC-LINKS: REQ-SDS-214
  update(ps) {
    const done = (ps || []).filter((p) => p.complete);
    const have = new Map();
    for (const r of this.rows) if (!have.has(r.key)) have.set(r.key, r);
    const keys = done.map(pkKey);
    if (this.rows.length && done.length && keys.every((k) => have.has(k))) {
      // same messages: hold still, refresh the positions of those seen now
      const pools = new Map();
      done.forEach((p, i) => { const k = keys[i]; if (!pools.has(k)) pools.set(k, []); pools.get(k).push(p); });
      for (const r of this.rows) {
        const pool = pools.get(r.key);
        const p = pool && pool.shift();
        r.present = !!p;
        if (p) { r.i0 = p.i0; r.i1 = p.i1; }
      }
      return false;
    }
    if (!done.length) { // nothing complete in this capture: keep what is listed
      for (const r of this.rows) r.present = false;
      return false;
    }
    this.rows = done.slice(0, this.max).map((p, i) => {
      const old = have.get(keys[i]);
      return Object.assign({}, p, { key: keys[i], id: old ? old.id : this.nextId++, present: true });
    });
    this.version++;
    return true;
  }

  // TRLC-LINKS: REQ-SDS-214
  clear() { this.rows = []; this.selKey = null; this.version++; }
  // TRLC-LINKS: REQ-SDS-214
  select(i) { const r = this.rows[i]; this.selKey = r ? r.key : null; return r || null; }
  // TRLC-LINKS: REQ-SDS-214
  selected() { return this.selKey === null ? null : this.rows.find((r) => r.key === this.selKey) || null; }

  // findMatch returns the index of the next row (from `from`, stepping dir) that
  // holds the pattern, wrapping around; -1 when none does.
  // TRLC-LINKS: REQ-SDS-214
  findMatch(tokens, from, dir) {
    const n = this.rows.length;
    if (!n) return -1;
    dir = dir < 0 ? -1 : 1;
    for (let k = 1; k <= n; k++) {
      const i = (((from + dir * k) % n) + n) % n;
      if (pkMatch(this.rows[i], tokens)) return i;
    }
    return -1;
  }
}

if (typeof module !== "undefined" && module.exports) {
  module.exports = { decodePackets, PacketList, pkHex, pkString, pkKey, pkMatch };
}
