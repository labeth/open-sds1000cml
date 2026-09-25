"use strict";
// REQ-SDS-068 / REQ-SDS-069 characterization; no browser, network or hardware.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const e = require(path.join(process.argv[2], 'sigrok_export.js'));
const sg = require(path.join(process.argv[2], 'spectrogram.js'));
const cases = [];
function check(name, run) { run(); cases.push({name, result:'pass'}); }
check('envelope marker is accepted when channel arrays are present', () => {
  const s = e.sigrokSeries({is_env:true, c1:[128,129], dt_s:1e-6});
  assert.equal(s.n, 2);
});
check('zero calibration scale uses the default volts-per-code', () => {
  const s = e.sigrokSeries({c1:[129], vpc1:0, dt_s:1e-6});
  assert.equal(s.ch[0].v[0], 1/25);
});
check('VCD decimation rounds capture length up to a whole stride', () => {
  const n = 131073;
  const v = e.sigrokVCD({n,rateHz:2,ch:[{name:'CH1',v:new Float64Array(n)}]});
  assert(v.includes('$comment decimated 2x'));
  assert.equal(v.trim().split('\n').at(-1), '#65537'); // Original n/rate is 65536.5 s.
});
check('stored ZIP bytes are deterministic for identical entries', () => {
  const entries=[{name:'value',data:new Uint8Array([1,2,3])}];
  assert.deepEqual(e.sigrokZip(entries),e.sigrokZip(entries));
});
check('waterfall clear removes rows and pixels but retains Nyquist', () => {
  const s=sg.sgNew(4,2); sg.sgPushRow(s,new Float64Array([1,2,3,4]),4,4,100e6);
  assert.equal(s.rows,1); sg.sgClear(s);
  assert.equal(s.rows,0); assert(s.data.every(v=>v===0)); assert.equal(s.nyq,100e6);
});
check('invalid peak leaves the waterfall unchanged', () => {
  const s=sg.sgNew(4,2); sg.sgPushRow(s,new Float64Array([1,2,3,4]),4,4,10);
  const before=Array.from(s.data); sg.sgPushRow(s,new Float64Array(4),4,0,20);
  assert.deepEqual(Array.from(s.data),before); assert.equal(s.rows,1); assert.equal(s.nyq,10);
});
check('100 MHz upper-axis label is incorrectly rendered as 1M', () => {
  const ctx={}; vm.runInNewContext(fs.readFileSync(path.join(process.argv[2],'spectrogram.js'),'utf8'),ctx);
  const labels=[]; const g={blit(){},fillText(text){labels.push(text);}};
  const s=ctx.sgNew(4,2); ctx.sgPushRow(s,new Float64Array([1,2,3,4]),4,4,100e6);
  ctx.sgBlit(g,400,200,s);
  assert.deepEqual(labels,['0k','25M','50M','75M','1M']);
});
console.log(JSON.stringify({result:'pass',cases,scope:'Seven reproduced source boundaries. Passing characterization does not endorse the incorrect 100 MHz label or establish browser/WebGL or external-reader compatibility.'},null,2));
