// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
// The page decodes with the scope's own decoder settings, as the status's
// panel Display has them: the protocol (Auto, a detection, is never one),
// lines, rate, SPI mode and bit order.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const dsrc = fs.readFileSync('app_decode.js', 'utf8');
const code = dsrc.slice(dsrc.indexOf('// DEC_SCOPE_PROTOS names'), dsrc.indexOf('// End of decScopeSettings.'));
const c = {};
vm.createContext(c); vm.runInContext(code, c);
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 0 }) }, { proto: 'off' }, 'scope decode Off: the page decodes nothing');
assert.equal(c.decScopeSettings({ DecProto: 1 }), null, 'Auto is no decoder');
assert.equal(c.decScopeSettings(null), null, 'no status yet');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 2, DecBaud: 115200, DecChA: 0, DecChB: 1 }) },
  { proto: 'uart', line: 1, baud: 115200 }, 'the owner\'s UART on C1 at 115200');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 3, DecChA: 1, DecChB: 0 }) },
  { proto: 'i2c', scl: 2, sda: 1 }, 'I2C roles');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 4, DecChA: 0, DecChB: 1, DecCPOL: true, DecCPHA: true }) },
  { proto: 'spi', clk: 1, data: 2, cpol: 1, cpha: 1, msb: true, delayNs: 0 }, 'SPI mode 3, MSB first');
assert.equal(c.decScopeSettings({ DecProto: 4, DecChA: 1, DecChB: 0, DecDelayNs: 7.1875 }).delayNs, 7.1875, 'SPI read at the scope\'s data delay');
assert.equal(c.decScopeSettings({ DecProto: 4, DecLSB: true }).msb, false, 'SPI LSB first');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 7, DecChA: 1, DecChB: 0, DecBaud: 500000 }) },
  { proto: 'can', line: 2, baud: 500000 }, 'CAN on C2 at the scope\'s bit rate');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 6, DecChA: 0, DecBaud: 115200 }) },
  { proto: 'sent', line: 1 }, 'SENT keeps its own tick');
const src = fs.readFileSync('app_bus.js', 'utf8');

// The table lists the complete packets kept, or else the capture's cut ones.
{
  const code2 = src.slice(src.indexOf('// busShownRows is'), src.indexOf('// End of busShownRows.'));
  const c2 = {};
  vm.createContext(c2); vm.runInContext(code2, c2);
  const whole = [{ bytes: [0x55], complete: true }], cut = [{ bytes: [0x69, 0x20, 0x55, 0xAA, 0x0F], complete: false }, { bytes: [], complete: false }];
  assert.equal(c2.busShownRows(whole, cut).partial, false, 'complete packets kept: they are listed');
  const s = c2.busShownRows([], cut);
  assert.ok(s.partial && s.rows.length === 1 && s.rows[0].bytes.length === 5, 'nothing complete: the cut packet with bytes is listed, as on the scope');
  console.log('ALL PASS (rows)');
}
