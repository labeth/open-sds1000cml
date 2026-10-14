// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// A roll view's held ranges (no capture behind them) draw dimmed: the trace is
// drawn twice, the held samples at a low alpha and the captured ones in full,
// each with the other's samples as gaps; the split is kept per frame array.
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const src = fs.readFileSync('app_draw.js', 'utf8');
const code = src.slice(src.indexOf('// drawTraceHeld draws a trace'), src.indexOf('// TRLC-LINKS: REQ-SDS-071\nfunction drawEnv('));
const calls = [];
// TRLC-LINKS: REQ-SDS-071
const c = { drawTrace: (g, cols, color, zoom, inv) => calls.push({ cols: Array.from(cols), alpha: g.globalAlpha }) };
vm.createContext(c); vm.runInContext(code, c);
const g = { globalAlpha: 1 };
const cols = Float32Array.from([10, 11.5, 12, 13, 14, 15]);
c.drawTraceHeld(g, cols, [[1, 3], [5, 9]], '#ff0', 1, false);
assert.equal(calls.length, 2);
assert.deepEqual(calls[0], { cols: [-1, 11.5, 12, -1, -1, 15], alpha: 0.35 }, 'the held samples, dimmed');
assert.deepEqual(calls[1], { cols: [10, -1, -1, 13, 14, -1], alpha: 1 }, 'the captured samples, in full');
assert.equal(g.globalAlpha, 1);
const first = c.heldSplit(cols, cols._heldSplit.ranges);
assert.equal(first, cols._heldSplit, 'kept for the same ranges');
calls.length = 0;
c.drawTraceHeld(g, cols, null, '#ff0', 1, false);
assert.equal(calls.length, 1, 'no held ranges: one plain trace');
console.log('ALL PASS');
