// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-018
// Edge ringing at the full rate is no I2C clock: ARINC 429's two lines at
// 10 us/div read as I2C on a 20 ns clock, faster than any I2C runs
// (decode_autodetect.go i2cMinClockS, TestAutodetectRingingIsNotI2C).
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const c = {}; vm.createContext(c);
vm.runInContext(fs.readFileSync('decode.js', 'utf8'), c);
const f = JSON.parse(fs.readFileSync('../decode/testdata/arinc429_raw_10us.json', 'utf8'));
const i2c = c.decodeI2C(f.c1, f.c2, f.sample_s, { fmt: 'hex' });
assert.ok(i2c.ok && i2c.bytes.length > 0, 'the capture reads as I2C bytes on its own');
const r = c.autodetect({ c1: f.c1, c2: f.c2, dt_s: f.sample_s, cols: f.c1.length }, { fmt: 'hex' });
assert.notEqual(r.proto, 'i2c', 'ringing auto-detected as I2C');
console.log('ALL PASS');
