// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-069
// The CSV export of a channel displayed inverted (Cn:INVS) holds what the
// screen shows: its volts negated, the header naming it; a channel not
// inverted exports as acquired.
"use strict";
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const src = fs.readFileSync('app_controls.js', 'utf8');
const csv = src.slice(src.indexOf('$("eCSV").onclick'), src.indexOf('// ---- sigrok exports'));
const inv = src.slice(src.indexOf('// exportInvert is'), src.indexOf('// TRLC-LINKS: REQ-SDS-069\nfunction exportName('));
// TRLC-LINKS: REQ-SDS-069
function exportCSV(st) {
  let out = null;
  const handlers = {};
  const c = {
    st, frame: { seq: 4, c1: [153, 128], c2: [153, 128], dt_s: 1e-6, vpc1: 0.04, vpc2: 0.04, edge_frac: -1 },
    // TRLC-LINKS: REQ-SDS-069
    $: (id) => (handlers[id] = handlers[id] || {}),
    // TRLC-LINKS: REQ-SDS-069
    exportName: (seq, ext) => 'scope-' + seq + '.' + ext,
    // TRLC-LINKS: REQ-SDS-069
    exportFile: (name, type, data) => { out = data; },
  };
  vm.createContext(c);
  vm.runInContext(inv + '\n' + csv, c);
  handlers.eCSV.onclick();
  return out.trim().split('\n');
}
const plain = exportCSV({ inv1: false, inv2: false });
assert.ok(!/inverted/.test(plain[0]), 'no invert: no note');
assert.equal(plain[2], '0.000000e+0,1.000000e+0,1.000000e+0');
const shown = exportCSV({ inv1: true, inv2: false });
assert.match(shown[0], / inverted=c1_as_displayed/);
assert.equal(shown[2], '0.000000e+0,-1.000000e+0,1.000000e+0', 'C1 negated, C2 as acquired');
console.log('ALL PASS');
