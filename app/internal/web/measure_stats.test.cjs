// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// MEASURE's statistics count a reading once: by the frame measured (meas_seq;
// the server reuses a reading for the frames of the next 100 ms while
// running), the edge shape by its edge-window read (edge_read; carried to the
// frames between reads).
"use strict";
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const { RunStats } = require('./wsmath.js');
const src = fs.readFileSync('app_measure.js', 'utf8');
const code = src.slice(src.indexOf('const MEAS_UNIT'), src.indexOf('// One unit per row'));
const c = { RunStats, ws: { cur: "measure" }, st: { tdiv_s: 2e-4 }, frame: null,
  MEAS_ALL: ["Vpp", "Vmax", "Vmin", "Vmean", "Vrms", "Freq", "Period", "Duty", "ACrms", "Vtop", "Vbase", "Vampl", "Rise", "Fall", "+Width", "-Width", "Overshoot", "Preshoot"] };
vm.createContext(c);
vm.runInContext(code + '\nthis.count = (id) => (measStats.get(id) || { n: 0 }).n;', c);
// TRLC-LINKS: REQ-SDS-217
const m = (rise) => ({ vpp: 3.3, rise_s: rise, edge_shape: true, has_timing: true, window_timing: true, freq: 1e6, period: 1e-6, duty: 50 });
const feed = [
  { seq: 1, meas_seq: 1, edge_read: 1, m1: m(25e-9) },
  { seq: 2, meas_seq: 1, edge_read: 1, m1: m(25e-9) }, // the same reading, a newer frame
  { seq: 3, meas_seq: 3, edge_read: 1, m1: m(25e-9) }, // a new reading, the edge carried
  { seq: 4, meas_seq: 4, edge_read: 2, m1: m(26e-9) }, // a new edge read
  { seq: 5, meas_seq: 5, m1: { vpp: 3.3, rise_s: 3e-9, has_timing: true, freq: 1e6 } }, // a raw frame: its own edges
];
for (const f of feed) { c.frame = f; c.measAccumulate(); }
assert.equal(c.count("1:Vpp"), 4, 'each reading once');
assert.equal(c.count("1:Rise"), 3, 'each edge read once, and the raw frame\'s own');
assert.equal(c.count("1:Freq"), 3, 'the timing the windows gave, once a read');
console.log('ALL PASS');
