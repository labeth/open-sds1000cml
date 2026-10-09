const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const source=fs.readFileSync('app_decode.js','utf8');
const compute=source.slice(source.indexOf('function computeDecode()'),source.indexOf('// watchReason'));
for(const flag of ['peak_detect','is_env']) {
 let leases=0, updates=0;
 const c={dcfg:{proto:'can',result:{text:'stale bytes'}},frame:{c1:[0,255,0,255],[flag]:true},renewDecodeLease(){leases++;},updateDecodeResults(){updates++;}};
 vm.createContext(c);vm.runInContext(compute,c);c.computeDecode();
 assert.equal(c.dcfg.result,null,'envelope must not retain a stale decode');
 assert.equal(leases,1,'request sampled data even while the current frame is an envelope');
 assert.equal(updates,1);
}
console.log('ALL PASS');
