// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync('app_geom.js', 'utf8');
const c = {st: {running: true}, DIVX: 10};
vm.createContext(c);
vm.runInContext(source.slice(source.indexOf('function homeSpan('), source.indexOf('function goHome(')), c);
for (const pos of [0, .2, .5, .8, 1]) {
  c.st.trig_pos_frac = pos;
  const f = {tdiv_s: 1, col_span_s: 20, win_frac: .5, edge_frac: pos};
  const w = c.homeWindow(f);
  assert.ok(Math.abs((f.edge_frac - w.a) / (w.b - w.a) - pos) < 1e-12, `trigger position ${pos}`);
}
for (const pos of [undefined, NaN, -1, 2]) {
  c.st.trig_pos_frac = pos;
  const w = c.homeWindow({tdiv_s: 1, col_span_s: 20, win_frac: .5, edge_frac: .5});
  assert.equal(w.a, .25);
}
console.log('trigger position endpoints pass');
