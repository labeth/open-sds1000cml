// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// Run by TestUARTRepeatNotSENTJS (arinc_auto_node_test.go).
// A 115200 Bd UART repeating one message reads, on some screens, as SENT with
// one frame of two passing its 4-bit CRC by chance: auto-detect weighs the
// clean UART at a standard rate over one distinct SENT frame, as the scope does
// (decode_autodetect.go).
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const c = {}; vm.createContext(c);
vm.runInContext(fs.readFileSync('decode.js', 'utf8') + '\n' + fs.readFileSync('decode_sent.js', 'utf8'), c);
const f = JSON.parse(fs.readFileSync('../decode/testdata/uart115200_repeat_envelope_200us.json', 'utf8'));
const sent = c.decodeSENT(f.c1, f.sample_s, { fmt: 'hex' });
assert.ok(sent.ok, 'the screen reads as SENT too (one CRC-valid frame)');
const r = c.autodetect({ c1: f.c1, c2: null, dt_s: f.sample_s, cols: f.c1.length }, { fmt: 'hex' });
assert.equal(r.proto, 'uart');
assert.ok(Math.abs(r.result.meta.baud - 115200) < 115200 * 0.03, 'baud ' + r.result.meta.baud);
console.log('ALL PASS');
