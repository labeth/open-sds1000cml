// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-202
// The page's zoom into a live envelope: the poll leases the shown stretch at
// full detail (view=a,b,cols, screen widths from the trigger), the frame
// carries it after its payload, and the trace is drawn from it while it
// covers the shown window.
"use strict";
const assert = require('node:assert/strict'), fs = require('node:fs'), vm = require('node:vm');
const { decodeBinFrame, BIN_MAGIC } = require('./binframe.js');

// TRLC-LINKS: REQ-SDS-202
function msg(flags, hdrObj, payload) {
  const hdr = new TextEncoder().encode(JSON.stringify(hdrObj));
  const buf = new Uint8Array(8 + hdr.length + payload.length);
  buf[0] = BIN_MAGIC; buf[1] = flags;
  new DataView(buf.buffer).setUint32(4, hdr.length, true);
  buf.set(hdr, 8); buf.set(payload, 8 + hdr.length);
  return buf.buffer;
}

// decode: a deep frame of 4 values a channel and a view of 3
{
  const pay = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 20, 21, 22]);
  const f = decodeBinFrame(msg(4, { seq: 2, cols: 4, depth: 4, view_n: 3, view_a: 0.25, view_b: 0.5 }, pay));
  assert.ok(f, 'decodes');
  assert.deepEqual([...f.c1], [1, 2, 3, 4]); assert.deepEqual([...f.c2], [5, 6, 7, 8]);
  assert.deepEqual([...f.vc1], [10, 11, 12]); assert.deepEqual([...f.vc2], [20, 21, 22]);
  assert.equal(decodeBinFrame(msg(4, { seq: 2, cols: 4, depth: 4, view_n: 8 }, pay)), null, 'a view longer than the payload');
  assert.equal(decodeBinFrame(msg(4, { seq: 2, cols: 4, depth: 4, view_n: 2 }, pay)), null, 'a payload that does not add up');
  const p = decodeBinFrame(msg(4 | 0x20, { seq: 2, cols: 2, depth: 2, fraction_bits: 8, view_n: 1 },
    new Uint8Array([0, 1, 0, 2, 0, 3, 0, 4, 9, 7])));
  assert.ok(p && p.c1[0] === 1 && p.c2[1] === 4 && p.vc1[0] === 9 && p.vc2[0] === 7, 'after a precision payload');
}

// draw: the view is used while it covers the window, drawn in its own fractions
{
  const src = fs.readFileSync('app_draw.js', 'utf8');
  const code = src.slice(src.indexOf('// viewCovers is whether'), src.indexOf('// drawTraceHeld draws a trace'));
  const calls = [];
  const c = { view: { win: { a: 0.49, b: 0.51 } } };
  // TRLC-LINKS: REQ-SDS-202
  c.drawTrace = (g, vals, color) => calls.push({ n: vals.length, a: c.view.win.a, b: c.view.win.b, color });
  vm.createContext(c); vm.runInContext(code, c);
  const f = { vc1: new Int16Array(770), vc2: new Int16Array(770), view_a: 0.488, view_b: 0.512 };
  assert.equal(c.viewCovers(f), true);
  assert.equal(c.viewCovers(Object.assign({}, f, { view_a: 0.495 })), false, 'starts after the window');
  assert.equal(c.viewCovers(Object.assign({}, f, { vc1: null })), false, 'no view');
  c.drawViewTrace({}, f.vc1, f.view_a, f.view_b, '#ff0', 1, false);
  assert.equal(calls.length, 1);
  assert.ok(Math.abs(calls[0].a - (0.49 - 0.488) / 0.024) < 1e-12 && Math.abs(calls[0].b - (0.51 - 0.488) / 0.024) < 1e-12, 'the window in the view\'s fractions');
  assert.deepEqual(c.view.win, { a: 0.49, b: 0.51 }, 'the window put back');
}

// poll: the lease while zoomed into a running envelope, view=off once after
{
  const src = fs.readFileSync('app_core.js', 'utf8');
  const code = src.slice(src.indexOf('let viewLeased'), src.indexOf('// TRLC-LINKS: REQ-SDS-023\nasync function pollFrameBin'));
  const c = { view: { win: { a: 0.45, b: 0.55 } }, userZoomed: true, st: { running: true }, CW: 1200,
    frame: { peak_detect: true, win_frac: 4065 / 4096, edge_frac: 0.5, c1: { length: 4096 } } };
  vm.createContext(c); vm.runInContext(code + '\nthis.viewParam = viewParam;', c);
  const q = c.viewParam();
  const m = /^&view=(-?[\d.]+),(-?[\d.]+),(\d+)$/.exec(q);
  assert.ok(m, q);
  const n = 4096, anchor = 0.5 * n, screen = 4065, pad = 0.1 * 0.02;
  assert.ok(Math.abs(+m[1] - ((0.45 - pad) * (n - 1) - anchor) / screen) < 1e-6, 'a: screen widths from the trigger');
  assert.ok(Math.abs(+m[2] - ((0.55 + pad) * (n - 1) - anchor) / screen) < 1e-6, 'b');
  assert.equal(+m[3], 1200, 'the canvas columns');
  c.userZoomed = false;
  assert.equal(c.viewParam(), '&view=off');
  assert.equal(c.viewParam(), '', 'ended once');
  c.userZoomed = true; c.st.running = false;
  assert.equal(c.viewParam(), '', 'stopped: the full record is shown, no lease');
}
console.log('ALL PASS');
