// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-214, REQ-SDS-222
// The page decodes with the scope's own decoder settings, as the status's
// panel Display has them: the protocol (Auto, a detection, is never one),
// lines, rate, SPI mode and bit order.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const dsrc = fs.readFileSync('app_decode.js', 'utf8');
const code = dsrc.slice(dsrc.indexOf('// DEC_SCOPE_PROTOS names'), dsrc.indexOf('// End of decScopeSettings.'));
const c = { DEC_CLOCKED: ['ps2', 'swd', 'mdio', 'wiegand'] };
vm.createContext(c); vm.runInContext(code, c);
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 0 }) }, { proto: 'off' }, 'scope decode Off: the page decodes nothing');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 28, DecChA: 1, DecChB: 0, DecBaud: 12500 }) },
  { proto: 'ps2', clk: 2, data: 1, baud: 12500, layer: '' }, 'PS/2: clock and data roles');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 15, DecChA: 0, DecChB: 1, DecBaud: 19200, DecParity: 'none' }) },
  { proto: 'modbus', line: 1, baud: 19200, modbusParity: 'none', layer: '' }, 'Modbus with the framing the scope found');
assert.equal(c.decScopeSettings({ DecProto: 31 }).proto, 'wiegand', 'the last protocol number');
assert.equal(c.decScopeSettings({ DecProto: 1 }), null, 'Auto is no decoder');
assert.equal(c.decScopeSettings(null), null, 'no status yet');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 2, DecBaud: 115200, DecChA: 0, DecChB: 1 }) },
  { proto: 'uart', line: 1, baud: 115200, parity: 'none', inverted: false, layer: '' }, 'the owner\'s UART on C1 at 115200');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 2, DecBaud: 100000, DecChA: 1, DecParity: 'even', DecInverted: true, DecLayer: 'sbus' }) },
  { proto: 'uart', line: 2, baud: 100000, parity: 'even', inverted: true, layer: 'sbus' }, 'S.BUS: the scope\'s UART framing and layer');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 3, DecChA: 1, DecChB: 0 }) },
  { proto: 'i2c', scl: 2, sda: 1, layer: '', layerAddr: -1, layerDir: 0 }, 'I2C roles');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 3, DecChA: 0, DecChB: 1, DecLayer: 'i2cfilter', DecLayerAddr: 0x50, DecLayerDir: 1 }) },
  { proto: 'i2c', scl: 1, sda: 2, layer: 'i2cfilter', layerAddr: 0x50, layerDir: 1 }, 'I2C filter layer: the address and direction');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 4, DecChA: 0, DecChB: 1, DecCPOL: true, DecCPHA: true }) },
  { proto: 'spi', clk: 1, data: 2, cpol: 1, cpha: 1, msb: true, delayNs: 0, layer: '' }, 'SPI mode 3, MSB first');
assert.equal(c.decScopeSettings({ DecProto: 4, DecChA: 1, DecChB: 0, DecDelayNs: 7.1875 }).delayNs, 7.1875, 'SPI read at the scope\'s data delay');
assert.equal(c.decScopeSettings({ DecProto: 4, DecLSB: true }).msb, false, 'SPI LSB first');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 7, DecChA: 1, DecChB: 0, DecBaud: 500000 }) },
  { proto: 'can', line: 2, baud: 500000, layer: '' }, 'CAN on C2 at the scope\'s bit rate');
assert.deepEqual({ ...c.decScopeSettings({ DecProto: 6, DecChA: 0, DecBaud: 115200 }) },
  { proto: 'sent', line: 1, layer: '' }, 'SENT keeps its own tick');
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
