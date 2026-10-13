// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-018
// One byte is no bus: part of a MIL-1553 word at 1 us/div read as "UART
// 2 MBd, 1 byte" or "Manchester, 1 byte"; auto-detect finds nothing there and
// BUS looks again at a slower timebase (decode_autodetect.go, the real
// capture mil1553_raw_1us). A UART reading with a fifth of its bytes
// misframed is another protocol at any rate.
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const c = {}; vm.createContext(c);
vm.runInContext(fs.readFileSync('decode.js', 'utf8'), c);
const f = JSON.parse(fs.readFileSync('../decode/testdata/mil1553_raw_1us.json', 'utf8'));
const r = c.autodetect({ c1: f.c1, c2: f.c1, dt_s: f.sample_s, cols: f.c1.length }, { fmt: 'hex' });
assert.equal(r.proto, 'off', 'a 1-byte reading named a bus: ' + r.proto);
// the UART evidence rules, as the scope's (TestUARTScoreNeedsEvidence)
// TRLC-LINKS: REQ-SDS-018
const uart = (n, bad, baud) => ({ ok: true, proto: 'uart', bytes: Array.from({ length: n }, (_, i) => i),
  spans: Array.from({ length: n }, (_, i) => ({ kind: i < bad ? 'frame-error' : 'data' })), meta: { baud, samplesPerBit: 20 } });
assert.ok(c.scoreResult(uart(1, 0, 115200)) < 0, 'one byte');
assert.ok(c.scoreResult(uart(3, 1, 500000)) < 0, '3 bytes, 1 misframed, standard rate');
assert.ok(c.scoreResult(uart(3, 0, 115200)) > 0, '3 clean bytes');
assert.ok(c.scoreResult(uart(5, 1, 115200)) > 0, '5 bytes, 1 misframed');
assert.equal(c.scoreResult(uart(8, 0, 115200)) - c.scoreResult(uart(8, 0, 66674)), 100, 'clean at a standard rate');
// a clock's idle level is its gap between transfers; nothing but clocking
// reads low (TestIdleLevelFromTheGap)
// TRLC-LINKS: REQ-SDS-018
const clock = (lead, lead0, halves, tail, tail0) => {
  // TRLC-LINKS: REQ-SDS-018
  const c = []; const put = (v, n) => { for (let i = 0; i < n; i++) c.push(v); };
  put(lead0, lead); let v = 200;
  for (let i = 0; i < halves; i++) { put(v, 50); v = 250 - v; }
  put(tail0, tail); return c;
};
assert.equal(c.idleLevel(c.sliceChannel(clock(20, 50, 41, 20, 50))), 0, 'nothing but clocking');
assert.equal(c.idleLevel(c.sliceChannel(clock(400, 200, 40, 0, 0))), 1, 'idling high');
assert.equal(c.idleLevel(c.sliceChannel(clock(10, 200, 41, 110, 50))), 0, 'idling low, the gap cut short');
console.log('ALL PASS');
