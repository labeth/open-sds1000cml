// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// packets.test.cjs — BUS's packet grouping and the still-standing packet list
// (packets.js), on records decoded by the real decoders: UART bursts, I²C
// transactions and SPI bursts, with packets cut by the record edges.
// TRLC-LINKS: REQ-SDS-214
"use strict";
const assert = require("node:assert/strict");
const D = require("./decode.js");
const P = require("./packets.js");

// uartRecord: 8N1 frames of `msgs` at spb samples/bit, `gap` idle samples
// between messages, opening `lead` samples after the record start (negative =
// the record opens inside the first message) and ending `tail` samples after.
// TRLC-LINKS: REQ-SDS-214
function uartRecord(msgs, spb, gap, lead, tail) {
  const out = [];
  const push = (v, k) => { for (let i = 0; i < k; i++) out.push(v ? 200 : 50); };
  for (let m = 0; m < msgs.length; m++) {
    for (const b of Buffer.from(msgs[m], "latin1")) {
      push(0, spb);
      for (let k = 0; k < 8; k++) push((b >> k) & 1, spb);
      push(1, spb);
    }
    if (m + 1 < msgs.length) push(1, gap);
  }
  push(1, tail);
  const pre = [];
  for (let i = 0; i < Math.max(0, lead); i++) pre.push(200);
  const rec = pre.concat(out);
  return lead < 0 ? rec.slice(-lead) : rec;
}

// ---- UART: whole bursts are complete; bursts cut by the record edges are not ----
{
  const spb = 10, dt = 1 / (115200 * spb);
  const rec = uartRecord(["Hello", "World!", "OK"], spb, 400, 300, 300);
  const r = D.decodeUART(rec, dt, { baud: 115200, bits: 8, parity: "none" });
  assert.ok(r.ok, "uart decodes");
  const ps = P.decodePackets(r, rec.length);
  assert.equal(ps.length, 3, "three bursts → three packets");
  assert.deepEqual(ps.map((p) => P.pkString(p)), ["Hello", "World!", "OK"]);
  assert.ok(ps.every((p) => p.complete), "idle around every burst: all complete");
  assert.equal(P.pkHex(ps[0]), "48 65 6C 6C 6F");
  // the record opens inside "Hello" and closes inside "OK"
  const cut = uartRecord(["Hello", "World!", "OK"], spb, 400, -150, 0).slice(0, -12 * spb);
  const rc = D.decodeUART(cut, dt, { baud: 115200, bits: 8, parity: "none" });
  const pc = P.decodePackets(rc, cut.length);
  assert.deepEqual(pc.filter((p) => p.complete).map((p) => P.pkString(p)), ["World!"], "only the whole burst is complete");
}

// ---- I²C: START..STOP with the address; a transaction cut at the end is incomplete ----
{
  const scl = [], sda = [];
  const SPB = 40, half = SPB / 2;
  const seg = (n, s, d) => { for (let k = 0; k < n; k++) { scl.push(s ? 210 : 40); sda.push(d ? 210 : 40); } };
  const bit = (b) => { seg(half, false, b); seg(half, true, b); };
  const byte = (v) => { for (let i = 7; i >= 0; i--) bit((v >> i) & 1); };
  const txn = (addr, rd, data, stop) => {
    seg(half, true, true); seg(half, true, false);        // START
    byte((addr << 1) | (rd ? 1 : 0)); bit(0);             // address + ACK
    for (const d of data) { byte(d); bit(0); }
    // (idle-high after the last ACK would itself be a STOP, so the cut one ends here)
    if (stop) { seg(half, false, false); seg(half, true, false); seg(half, true, true); seg(SPB * 3, true, true); }
  };
  seg(SPB * 2, true, true);
  txn(0x50, false, [0x00, 0x10], true);
  txn(0x50, true, [0xab], true);
  txn(0x27, false, [0x01, 0x02], false);                  // no STOP before the record ends
  const r = D.decodeI2C(scl, sda, 1e-6, {});
  assert.ok(r.ok, "i2c decodes");
  const ps = P.decodePackets(r, scl.length);
  assert.equal(ps.length, 3);
  assert.deepEqual(ps.map((p) => p.complete), [true, true, false], "the unterminated transaction is incomplete");
  assert.equal(ps[0].addr, 0x50); assert.equal(ps[0].read, false); assert.equal(P.pkHex(ps[0]), "00 10");
  assert.equal(ps[1].read, true); assert.equal(P.pkHex(ps[1]), "AB");
  assert.equal(P.pkKey(ps[1]), "80R:AB");
}

// ---- SPI: bursts split by clock idle ----
{
  const clk = [], dat = [];
  const P0 = 8;
  const idle = (n) => { for (let i = 0; i < n; i++) { clk.push(40); dat.push(40); } };
  const burst = (bytes) => {
    for (const v of bytes) for (let i = 7; i >= 0; i--) {
      const b = (v >> i) & 1;
      for (let k = 0; k < P0; k++) { clk.push(k < P0 / 2 ? 40 : 210); dat.push(b ? 210 : 40); }
    }
  };
  idle(200); burst([0xde, 0xad]); idle(300); burst([0xbe, 0xef, 0x01]); idle(300);
  const r = D.decodeSPI(clk, dat, 1e-7, { cpol: 0, cpha: 0 });
  assert.ok(r.ok, "spi decodes");
  const ps = P.decodePackets(r, clk.length);
  assert.deepEqual(ps.map((p) => P.pkHex(p)), ["DE AD", "BE EF 01"]);
  assert.ok(ps.every((p) => p.complete));
}

// ---- PacketList: holds still while the same messages repeat ----
{
  const mk = (s, i0, complete = true) => ({ i0, i1: i0 + 50, bytes: [...Buffer.from(s, "latin1")], byteErr: [...s].map(() => false), addr: -1, hasAddr: false, read: false, complete, err: "" });
  const L = new P.PacketList();
  assert.equal(L.update([mk("A1", 10), mk("B2", 200), mk("C3", 400)]), true, "first content lists");
  const rows = L.rows, ids = rows.map((r) => r.id);
  assert.equal(L.update([mk("B2", 30), mk("C3", 230), mk("A1", 430)]), false, "the same messages, shifted: no change");
  assert.equal(L.rows, rows, "the rows are the same objects");
  assert.equal(L.rows[1].i0, 30, "positions follow the new capture");
  assert.equal(L.update([mk("A1", 10), mk("B2", 200, false)]), false, "a subset (one cut by the edge) holds still too");
  assert.equal(L.update([mk("Z", 5, false)]), false, "nothing complete: keep the list");
  L.select(2);
  assert.equal(L.selected().key, P.pkKey(mk("C3", 0)));
  assert.equal(L.update([mk("A1", 10), mk("D4", 99), mk("C3", 300)]), true, "a new message replaces the rows");
  assert.deepEqual(L.rows.map((r) => r.id), [ids[0], 4, ids[2]], "rows keep their id while their content stays listed");
  assert.equal(L.selected().i0, 300, "the selection follows its content");
  assert.equal(L.findMatch(["44"], -1, 1), 1, "findMatch finds a byte");
  assert.equal(L.findMatch(["4?"], 1, 1), 2, "nibble wildcard: the next row holding 4x");
  assert.equal(L.findMatch(["4?"], 2, 1), 0, "wrapping around");
  assert.equal(L.findMatch(["??", "34"], 0, 1), 1);
  assert.equal(L.findMatch(["FF"], 0, 1), -1);
}
console.log("ALL PASS");
