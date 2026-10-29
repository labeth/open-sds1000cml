// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-207, REQ-SDS-222
// Bit order is SPI's setting: an SPI bus read LSB first leaves the Manchester
// bus set next decoded and matched MSB first, as the scope decodes it.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const dec = fs.readFileSync('app_decode.js', 'utf8');
const compute = dec.slice(dec.indexOf('function computeDecode()'), dec.indexOf('// watchReason'));
const st = fs.readFileSync('app_serialtrig.js', 'utf8');
const params = st.slice(st.indexOf('// TRLC-LINKS: REQ-SDS-206\nfunction stProtoNum'), st.indexOf('let stLastSig'));
for (const proto of ['manchester', 'spi']) {
  let cfg = null;
  const el = { value: '', textContent: '' };
  const c = { dcfg: { proto, msb: false, auto: true, fmt: 'hex', line: 1, clk: 1, data: 2, bits: 8 }, frame: { c1: [0, 255], c2: [0, 255] },
    // TRLC-LINKS: REQ-SDS-207
    $: () => el,
    // TRLC-LINKS: REQ-SDS-207
    // TRLC-LINKS: REQ-SDS-207
    updateDecodeResults() {},
    // TRLC-LINKS: REQ-SDS-207
    decCodes: () => [0, 255],
    // TRLC-LINKS: REQ-SDS-207
    frameDtS: () => 1e-6,
    // TRLC-LINKS: REQ-SDS-207
    decodeManchester: (_c, _t, o) => { cfg = { msb: o.msb }; return null; },
    // TRLC-LINKS: REQ-SDS-207
    decodeSPI: (_a, _b, _t, o) => { cfg = { msb: o.bitOrder === 'msb' }; return null; },
    // TRLC-LINKS: REQ-SDS-206
    stParseAddr: () => -1,
    // TRLC-LINKS: REQ-SDS-206
    stParsePattern: () => ({ bytes: [0x55], pattern: [] }) };
  vm.createContext(c);
  vm.runInContext(compute + params, c);
  c.computeDecode();
  const want = proto !== 'spi';
  assert.equal(cfg.msb, want, proto + ' decoded ' + (cfg.msb ? 'MSB' : 'LSB') + ' first after SPI LSB first');
  assert.equal(c.stParams().msb, want, proto + ' trigger matched ' + (want ? 'LSB' : 'MSB') + ' first after SPI LSB first');
}
console.log('ALL PASS');
