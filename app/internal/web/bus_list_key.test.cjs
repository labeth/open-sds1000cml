// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-214
// The BUS packet list belongs to the decoder settings, as the scope's does
// (busListKey): settings that change (another protocol, rate or line) start
// it over, so packets the old settings decoded are never listed as the new
// bus's.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const pk = require('./packets.js');
const src = fs.readFileSync('app_bus.js', 'utf8');
const code = src.slice(src.indexOf('// busListKey is the decoder settings'), src.indexOf('// busTokens:'));
const dec = fs.readFileSync('app_decode.js', 'utf8');
const key = dec.slice(dec.indexOf('// decKey names the bus'), dec.indexOf('// TRLC-LINKS: REQ-SDS-207\nfunction computeDecode'));
// TRLC-LINKS: REQ-SDS-214
const uart = (bytes) => {
  const spans = [];
  let t = 0;
  for (const b of bytes) { spans.push({ kind: 'start', i0: t, i1: t + 10 }); spans.push({ kind: 'data', i0: t + 10, i1: t + 90, val: b }); spans.push({ kind: 'stop', i0: t + 90, i1: t + 100 }); t += 100; }
  spans.push({ kind: 'gap', i0: t + 200, i1: t + 210 });
  return { ok: true, proto: 'uart', spans, bytes, text: '' };
};
const c = { dcfg: { proto: 'uart', line: 1, baud: 115200, result: null }, frame: { c1: new Array(2000) }, frozen: false, busSel: null,
  PacketList: pk.PacketList, decodePackets: pk.decodePackets,
  // TRLC-LINKS: REQ-SDS-214
  busRenderTable() {} };
vm.createContext(c);
vm.runInContext(key + code + '\nvar busPk = new PacketList(200); var busLive = [];', c);
c.dcfg.result = uart([0x48, 0x69]);
c.busOnDecode();
assert.equal(c.busPk.rows.length, 1, 'the UART message is listed');
c.dcfg.baud = 9600; // another rate: what 115200 Bd read is not this bus's
c.dcfg.result = { ok: true, proto: 'uart', spans: [{ kind: 'data', i0: 5, i1: 50, val: 0x33 }], bytes: [0x33], text: '' }; // cut by the record's edge
c.busOnDecode();
assert.equal(c.busPk.rows.length, 0, 'packets the old settings decoded are listed as the new bus\'s: ' + JSON.stringify(c.busPk.rows.map((r) => r.bytes)));
c.dcfg.result = uart([0x4f, 0x4b]);
c.busOnDecode();
assert.equal(c.busPk.rows.length, 1, 'the new bus lists its own');
c.dcfg.result = { ok: true, proto: 'uart', spans: [], bytes: [], text: '' };
c.busOnDecode();
assert.equal(c.busPk.rows.length, 1, 'a capture with no whole message keeps the list (same settings)');
// another timebase: the rows' sample places are another record's
c.frame.tdiv_s = 200e-6;
c.dcfg.result = { ok: true, proto: 'uart', spans: [], bytes: [], text: '' };
c.busOnDecode();
assert.equal(c.busPk.rows.length, 0, 'rows from another timebase kept: ' + c.busPk.rows.length);
console.log('ALL PASS');
