// Offline characterization of REQ-SDS-023 and REQ-SDS-070; no DOM or network.
"use strict";
const assert = require('node:assert/strict');
const path = require('node:path');
const { decodeBinFrame, BIN_MAGIC } = require(path.join(process.argv[2], 'binframe.js'));
const { spectrum, detectPeaks, nearestPeak, component } = require(path.join(process.argv[2], 'peaks.js'));
const cases = [];
function check(name, body) { body(); cases.push({name, result: 'pass'}); }
function frame(flags, header, payload) {
  const json = new TextEncoder().encode(JSON.stringify(header));
  const out = new Uint8Array(8 + json.length + payload.length);
  out[0] = BIN_MAGIC; out[1] = flags;
  new DataView(out.buffer).setUint32(4, json.length, true);
  out.set(json, 8); out.set(payload, 8 + json.length);
  return out.buffer;
}
check('fractional cols and head are coerced to signed integers', () => {
  const f = decodeBinFrame(frame(0, {seq: 1, cols: 2.9, head: 0.9}, [1,2,3,4]));
  assert.deepEqual([...f.c1], [1,2]);
  assert.equal(f.cols, 2.9); // Original header survives while allocation uses coerced value.
});
check('unknown high flag bit is accepted', () => {
  assert.deepEqual([...decodeBinFrame(frame(0x80, {seq: 1, cols: 2}, [1,2,3,4])).c1], [1,2]);
});
check('envelope branch precedes Q8 flag validation', () => {
  const f = decodeBinFrame(frame(0x20, {seq: 1, cols: 2, is_env: true}, [1,2,3,4,5,6,7,8]));
  assert(f.e1min instanceof Int16Array);
  assert.equal(f.c1, undefined);
});
check('raw Q8 envelope is rejected', () => {
  assert.equal(decodeBinFrame(frame(0x30, {seq: 1, cols: 2, is_env: true, fraction_bits: 8}, [1,2,3,4,5,6,7,8])), null);
});
check('unchanged header bypasses cols validation with empty payload', () => {
  assert.equal(decodeBinFrame(frame(0, {seq: 1, unchanged: true, cols: -7}, [])).cols, -7);
});
check('spectrum takes largest power-of-two prefix', () => {
  const values = Array.from({length: 32}, (_, i) => 128 + 40*Math.sin(2*Math.PI*4*i/32));
  assert.deepEqual(spectrum(values.concat([1e9, -1e9]), 500), spectrum(values, 500));
  assert.equal(spectrum(values.slice(0,15), 500), null);
});
check('peak frequency is zero when Nyquist is omitted', () => {
  const values = Array.from({length: 64}, (_, i) => 128 + 40*Math.sin(2*Math.PI*8*i/64));
  const found = detectPeaks(spectrum(values), {});
  assert(found.length > 0); assert(found.every(p => p.freq === 0));
});
check('nearestPeak uses first entry on equal-distance tie', () => {
  assert.equal(nearestPeak([{freq: 10}, {freq: 20}], 15), 0);
});
check('component synthesizes gaps but returns null for all-gap input', () => {
  assert.equal(component([-1,-1,-1], 1), null);
  const x = Array.from({length: 64}, (_, i) => 128 + 40*Math.cos(2*Math.PI*8*i/64));
  x[3] = -1;
  assert(component(x, 8)[3] >= 0);
});
console.log(JSON.stringify({result: 'pass', cases, scope: 'Nine deterministic source-boundary cases under Node. No browser rendering, HTTP server or physical measurement qualification.'}, null, 2));
