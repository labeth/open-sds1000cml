// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// packets.test.cjs — BUS's packet grouping and the still-standing packet list
// (packets.js), on records decoded by the real decoders: UART bursts, I²C
// transactions and SPI bursts, with packets cut by the record edges.
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
  // TRLC-LINKS: REQ-SDS-214
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
  // TRLC-LINKS: REQ-SDS-214
  const seg = (n, s, d) => { for (let k = 0; k < n; k++) { scl.push(s ? 210 : 40); sda.push(d ? 210 : 40); } };
  // TRLC-LINKS: REQ-SDS-214
  const bit = (b) => { seg(half, false, b); seg(half, true, b); };
  // TRLC-LINKS: REQ-SDS-214
  const byte = (v) => { for (let i = 7; i >= 0; i--) bit((v >> i) & 1); };
  // TRLC-LINKS: REQ-SDS-214
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
  // TRLC-LINKS: REQ-SDS-214
  const idle = (n) => { for (let i = 0; i < n; i++) { clk.push(40); dat.push(40); } };
  // TRLC-LINKS: REQ-SDS-214
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
  // TRLC-LINKS: REQ-SDS-214
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
// addresses in each bus's own terms, as the scope's list writes them (Go analysis.AddrText)
{
  // TRLC-LINKS: REQ-SDS-214
  const a = (proto, addr, read) => P.pkAddrText(proto, { addr, hasAddr: true, read: !!read });
  assert.equal(a("i2c", 0x24), "24 W");
  assert.equal(a("can", 0x123), "123", "a CAN data frame: no direction");
  assert.equal(a("can", 0x100, true), "100 R", "a remote frame");
  assert.equal(a("arinc429", 0o310), "310", "an ARINC label in octal");
  assert.equal(a("mil1553", 5, true), "RT5 T");
  assert.equal(P.pkAddrText("uart", { hasAddr: false }), "");
  // the decoders' address spans by value: a CAN identifier (kind id), an ARINC label
  const can = P.decodePackets({ ok: true, proto: "canfd", spans: [
    { i0: 100, i1: 101, kind: "sof", text: "SOF" }, { i0: 102, i1: 120, kind: "id", text: "123", val: 0x123 },
    { i0: 121, i1: 122, kind: "rtr", text: "DATA", val: 0 }, { i0: 130, i1: 140, kind: "data", text: "55", val: 0x55 },
    { i0: 141, i1: 150, kind: "crc", text: "CRC:1234", val: 0x1234 }, { i0: 151, i1: 152, kind: "ack", text: "ACK" }] }, 400);
  assert.ok(can.length && can[0].hasAddr && can[0].addr === 0x123 && !can[0].read, "CAN id read into the packet: " + JSON.stringify(can[0]));
  // MIL-1553: a command word and its data word are one message, words as two
  // bytes each, the RT address from the command word (as the scope lists it)
  // TRLC-LINKS: REQ-SDS-214
  const w = (i0, sync, val, bad) => [{ i0, i1: i0 + 2, kind: "start", text: sync }, { i0: i0 + 3, i1: i0 + 18, kind: "data", text: (bad ? "!" : "") + val.toString(16), val }];
  const mil = P.decodePackets({ ok: true, proto: "mil1553", spans: [...w(100, "csync", 0xA55A), ...w(120, "dsync", 0x5AA5), ...w(300, "csync", 0xA55A)] }, 600);
  assert.equal(mil.length, 2, "two messages: " + JSON.stringify(mil.map(P.pkHex)));
  assert.equal(P.pkHex(mil[0]), "A5 5A 5A A5");
  assert.equal(P.pkAddrText("mil1553", mil[0]), "RT20 T");
  // ARINC 429: each word a packet of its own, its 19-bit field as three bytes
  // TRLC-LINKS: REQ-SDS-214
  const ar = (i0, label, val) => [{ i0, i1: i0 + 8, kind: "addr", text: label.toString(8), val: label }, { i0: i0 + 10, i1: i0 + 28, kind: "data", text: val.toString(16), val }];
  const arinc = P.decodePackets({ ok: true, proto: "arinc429", spans: [...ar(100, 0o310, 0xABCD), ...ar(136, 0o205, 0x12345)] }, 400);
  assert.deepEqual(arinc.map(P.pkHex), ["00 AB CD", "01 23 45"], "ARINC words");
  assert.deepEqual(arinc.map((q) => P.pkAddrText("arinc429", q)), ["310", "205"]);
  // the frame-start matches AUTO in BUS arms: SOF ?? any packet with a unit,
  // SOF then units the packet's opening ones, CMD a message opened by its
  // command word (as the scope's tokMatch)
  // TRLC-LINKS: REQ-SDS-214
  const man = { bytes: [0x12, 0x34, 0x56, 0x78], byteErr: [false, false, false, false] };
  assert.ok(P.pkMatch(man, ["SOF", "??"]) && P.pkMatch(man, ["SOF", "12"]), "SOF ?? / SOF 12");
  assert.ok(!P.pkMatch(man, ["SOF", "34"]) && P.pkMatch(man, ["34", "56"]), "34 does not open the frame");
  assert.ok(P.pkMatch(mil[0], ["CMD"]) && P.pkMatch(mil[0], ["CMD", "5A", "A5"]), "CMD on a message");
  assert.ok(!P.pkMatch({ bytes: [0x5A, 0xA5], byteErr: [false, false], hasAddr: false }, ["CMD"]), "data words only are no command");
}
console.log("ALL PASS");
