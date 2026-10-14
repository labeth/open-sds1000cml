// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-214
// A page entering BUS with its decode off decodes the bus the scope already
// decodes, with the scope's sheet settings (no detection that could step the
// scope's timebase); the scope's Off or Auto leaves the page to detect.
"use strict";
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const src = fs.readFileSync('app_bus.js', 'utf8');
const code = src.slice(src.indexOf('// busScopeDecode is'), src.indexOf('// End of busScopeDecode.'));
const c = {};
vm.createContext(c); vm.runInContext(code, c);
// TRLC-LINKS: REQ-SDS-214
const rows = (o) => Object.fromEntries(Object.entries(o).map(([k, v]) => [k, { ID: k, Value: v }]));
assert.equal(c.busScopeDecode(rows({ 'bus.protocol': 0 })), null, 'scope decode Off: the page detects');
assert.equal(c.busScopeDecode(rows({ 'bus.protocol': 1 })), null, 'scope Auto: the page detects');
assert.deepEqual({ ...c.busScopeDecode(rows({ 'bus.protocol': 2, 'bus.baud': 115200, 'bus.source': 0, 'bus.bit_rate': 500000 })) },
  { proto: 'uart', line: 1, baud: 115200 }, 'the owner\'s UART on C1 at 115200');
assert.deepEqual({ ...c.busScopeDecode(rows({ 'bus.protocol': 3, 'bus.scl': 1, 'bus.sda': 0 })) },
  { proto: 'i2c', scl: 2, sda: 1 }, 'I2C roles');
assert.deepEqual({ ...c.busScopeDecode(rows({ 'bus.protocol': 4, 'bus.clk': 0, 'bus.data': 1, 'bus.spi_mode': 3 })) },
  { proto: 'spi', clk: 1, data: 2, cpol: 1, cpha: 1 }, 'SPI mode 3');
assert.deepEqual({ ...c.busScopeDecode(rows({ 'bus.protocol': 7, 'bus.source': 1, 'bus.bit_rate': 500000 })) },
  { proto: 'can', line: 2, baud: 500000 }, 'CAN on C2 at the scope\'s bit rate');
assert.deepEqual({ ...c.busScopeDecode(rows({ 'bus.protocol': 6, 'bus.source': 0 })) },
  { proto: 'sent', line: 1 }, 'SENT keeps its own tick');

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
