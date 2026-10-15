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

// pkAddrText is a packet's address as the BUS lists write it in each bus's
// own terms (Go analysis.AddrText): I2C "24 W", CAN "123" ("123 R" remote),
// an ARINC 429 label in octal, a MIL-1553 "RT5 T", else hex.
// TRLC-LINKS: REQ-SDS-214
function pkAddrText(proto, p) {
  if (!p.hasAddr) return "";
  // TRLC-LINKS: REQ-SDS-214
  const hx = (v, n) => v.toString(16).toUpperCase().padStart(n, "0");
  if (proto === "i2c") return hx(p.addr, 2) + " " + (p.read ? "R" : "W");
  if (proto === "canfd" || proto === "can") return hx(p.addr, 3) + (p.read ? " R" : "");
  if (proto === "arinc429") return p.addr.toString(8).padStart(3, "0");
  if (proto === "mil1553") return "RT" + p.addr + " " + (p.read ? "T" : "R");
  return hx(p.addr, 2);
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
  const i2c = r.proto === "i2c", mil = r.proto === "mil1553", arinc = r.proto === "arinc429";
  const framed = !i2c && (arinc || spans.some((s) => PK_OPEN.has(s.kind)));
  // bytes a word takes, most significant first (Go analysis wordWidth)
  const width = mil ? 2 : arinc ? 3 : 1;
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
    // an ARINC 429 word opens with its label; a MIL-1553 data word continues
    // the message its command word opened (Go analysis framed)
    const dsync = mil && s.kind === "start" && s.text === "dsync";
    const opens = !framed ? false : arinc ? s.kind === "addr" : PK_OPEN.has(s.kind) && !(dsync && cur);
    if (cur && (opens || gapBefore > gapThr)) close(opens);
    if (!cur) {
      cur = pkNew(s.i0);
      // complete at the start: it opens on a delimiter, or idle precedes it inside the record
      cur.opened = framed ? opens && !dsync : (s.i0 >= gapThr && !lastWasGap && (lastEnd === -Infinity || gapBefore > gapThr));
    }
    lastWasGap = false;
    if (s.kind === "data" || PK_ERR.has(s.kind)) {
      if (r.proto === "mil1553" && s.kind === "data" && !cur.bytes.length && cur.opened && Number.isFinite(+s.val)) {
        // the command (or status) word: RT address and T/R bit (Go analysis)
        cur.addr = (+s.val >> 11) & 0x1f; cur.hasAddr = true; cur.read = ((+s.val >> 10) & 1) === 1;
      }
      if (s.val !== undefined && s.val !== null && Number.isFinite(+s.val)) {
        const bad = s.kind !== "data" || String(s.text || "").startsWith("!");
        for (let k = width - 1; k >= 0; k--) {
          cur.bytes.push(width > 1 ? (+s.val >> (8 * k)) & 0xff : +s.val);
          cur.byteErr.push(bad);
        }
        if (bad && !cur.err) cur.err = s.kind;
      } else if (PK_ERR.has(s.kind)) {
        if (mil) for (let k = Math.max(0, cur.byteErr.length - width); k < cur.byteErr.length; k++) cur.byteErr[k] = true; // the last word (Go markLast)
        if (!cur.err) cur.err = s.kind;
      }
    } else if (s.kind === "addr" || s.kind === "id") { // an ARINC 429 label, a CAN identifier: by value
      const a = s.val !== undefined && s.val !== null && Number.isFinite(+s.val) ? +s.val : parseInt(s.text, 16);
      if (Number.isFinite(a)) { cur.addr = a; cur.hasAddr = true; }
    } else if (s.kind === "rtr") cur.read = s.val === 1;
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
// hex units, "??" any unit, "A?" a nibble wildcard, "ERR" an errored unit;
// "SOF" the packet's start, the units after it opening the packet; "CMD" a
// MIL-1553 message's command word, its first two bytes) - as the scope's
// tokMatch.
// TRLC-LINKS: REQ-SDS-214
function pkMatch(p, tokens) {
  if (tokens && tokens.length && /^(sof|cmd)$/i.test(tokens[0])) {
    const skip = /^cmd$/i.test(tokens[0]) ? 2 : 0;
    if (skip && (!p.hasAddr || p.bytes.length < 2)) return false;
    if (tokens.length === 1) return p.bytes.length > 0;
    const q = { bytes: p.bytes.slice(skip), byteErr: p.byteErr.slice(skip) };
    return tokens.length - 1 <= q.bytes.length && pkMatchAt(q, tokens.slice(1), 0);
  }
  if (!tokens || !tokens.length || tokens.length > p.bytes.length) return false;
  for (let i = 0; i + tokens.length <= p.bytes.length; i++) if (pkMatchAt(p, tokens, i)) return true;
  return false;
}

// pkMatchAt reports whether the tokens hold at byte i of a packet.
// TRLC-LINKS: REQ-SDS-214
function pkMatchAt(p, tokens, i) {
  // TRLC-LINKS: REQ-SDS-214
  const one = (tok, k) => {
    if (/^err(or)?$/i.test(tok)) return !!p.byteErr[k];
    if (p.byteErr[k]) return false;
    const h = p.bytes[k].toString(16).toUpperCase().padStart(tok.length, "0");
    if (h.length !== tok.length) return false;
    for (let d = 0; d < tok.length; d++) if (tok[d] !== "?" && tok[d].toUpperCase() !== h[d]) return false;
    return true;
  };
  for (let j = 0; j < tokens.length; j++) if (!one(tokens[j], i + j)) return false;
  return true;
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
  module.exports = { pkAddrText, decodePackets, PacketList, pkHex, pkString, pkKey, pkMatch };
}
