// ENGMODEL-OWNER-UNIT: FU-WEB-BODE
// TRLC-LINKS: REQ-SDS-125
'use strict';
const assert = require('node:assert/strict');
const path = require('node:path');
const {spawnSync} = require('node:child_process');
const target = path.resolve(process.argv[2], 'bode.js');
const b = require(target);
const cases = [];
function test(name, fn) { const details = fn(); cases.push({name, result:'pass', ...details}); }
function context() {
  const calls = [];
  const g = {calls};
  for (const name of ['clearRect','fillText','beginPath','moveTo','lineTo','stroke','arc','fill'])
    g[name] = (...args) => calls.push({name,args});
  return g;
}
const points = (freq, gain_db, phase_deg) => ({freq,gain_db,phase_deg});
const bad = g => g.calls.flatMap(c => c.args).filter(x => typeof x === 'number' && !Number.isFinite(x)).length;
test('frequency labels lose significant integer zeros', () => {
  const inputs=[100,100000,100000000], actual=inputs.map(b.bodeFmtHz);
  assert.deepEqual(actual,['1','1k','1M']); return {inputs,actual};
});
test('all-NaN range is inverted infinite bounds', () => {
  const actual=b.bodeNiceRange([NaN],-20,20,10);assert.deepEqual(actual,[Infinity,-Infinity]);
  return {actual:actual.map(String)};
});
test('clean three-point trace places log midpoints and both panels', () => {
  const g=context(); b.bodeDraw(g,460,300,points([1000,10000,100000],[0,-6,-12],[0,-45,-90]),{});
  assert.equal(bad(g),0); const arcs=g.calls.filter(c=>c.name==='arc');assert.equal(arcs.length,6);
  assert.deepEqual(arcs.map(c=>c.args[0]),[42,231,420,42,231,420]);
  assert(arcs.slice(0,3).every(c=>c.args[1]>=12 && c.args[1]<=161));
  assert(arcs.slice(3).every(c=>c.args[1]>=177 && c.args[1]<=278));
  return {arcX:arcs.map(c=>c.args[0])};
});
test('hostile values reach drawing operations as nonfinite coordinates', () => {
  const samples={nanGain:points([1e5,1e6],[NaN,-6],[0,-45]),infGain:points([1e5,1e6],[Infinity,-6],[0,-45]),nanPhase:points([1e5,1e6],[0,-6],[NaN,-45]),zeroFrequency:points([0,1e6],[0,-6],[0,-45]),negativeFrequency:points([-1e5,1e6],[0,-6],[0,-45])};
  const counts={};for(const [name,p] of Object.entries(samples)){const g=context();b.bodeDraw(g,460,300,p,{});counts[name]=bad(g);assert(counts[name]>0);}
  return {nonfiniteCoordinateCounts:counts};
});
test('nonempty point set requires gain and phase arrays', () => {
  assert.throws(()=>b.bodeDraw(context(),460,300,{freq:[1000]},{}),TypeError);return {};
});
test('unsorted frequencies can place finite points outside plot bounds', () => {
  const g=context();b.bodeDraw(g,460,300,points([1e6,1e5,5e5],[0,-6,-3],[0,-45,-20]),{});
  const x=g.calls.filter(c=>c.name==='arc').map(c=>c.args[0]);assert.equal(bad(g),0);assert(x.some(v=>v<42 || v>420));return {arcX:x};
});
test('infinite tick upper bound exceeds bounded child execution', () => {
  const run=spawnSync(process.execPath,['-e',"require(process.argv[1]).bodeLogTicks(1,Infinity)",target],{timeout:2000,maxBuffer:1024,killSignal:'SIGKILL'});
  assert.equal(run.error?.code,'ETIMEDOUT');return {timeoutMs:2000,resultMeaning:'characterized nontermination within deadline; child killed'};
});
test('empty point set clears and draws invitation without traces', () => {
  const g=context();b.bodeDraw(g,460,300,points([],[],[]),{});assert.deepEqual(g.calls.map(c=>c.name),['clearRect','fillText']);assert(g.calls[1].args[0].includes('arm FRA'));return {};
});
console.log(JSON.stringify({result:'pass',cases,scope:'Recording drawing context and one bounded child process; no browser pixels, measurement accuracy or physical qualification.'},null,2));
