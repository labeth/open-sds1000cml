const assert=require('node:assert/strict');
const {precisionFitSignal}=require('../../app/internal/web/app_precision.js');
for(const model of ['sine','triangle']) {
 const y=Array.from({length:2500},(_,i)=>{const p=i*.002+.137;return 128+35*(model==='sine'?Math.sin(2*Math.PI*p):1-4*Math.abs(p-Math.floor(p)-.5))+.3*Math.sin(i*1.72)});
 const f=precisionFitSignal(y,2e-9,1e6,model);
 assert.ok(Math.abs(f.hz-1e6)<20);assert.ok(Math.abs(Math.abs(f.amplitude)-35)<.01);assert.ok(Math.abs(f.rms-.3/Math.sqrt(2))<.002);
 console.log(model,f.hz,f.rms);
}
assert.throws(()=>precisionFitSignal([1,2,3],1e-9,1e6,'sine'));
const {precisionPeriodicFit}=require('../../app/internal/web/app_precision.js');
const periodic=Array.from({length:1800},(_,i)=>128+35*Math.sin(i*.032*2*Math.PI)+3*Math.cos(i*.032*6*Math.PI)+.03*Math.sin(i*1.721));
const p=precisionPeriodicFit(periodic,32e-9,1e6);
assert.ok(Math.abs(p.hz-1e6)<5);assert.ok(Math.abs(p.rms-.03/Math.sqrt(2))<.002);console.log('periodic',p.hz,p.rms);
const b=require('node:fs').readFileSync(__dirname+'/r16avg16.bin');const hn=b.readUInt32LE(4),h=JSON.parse(b.subarray(8,8+hn)),q=b.subarray(8+hn);
const y=Array.from({length:h.cols},(_,i)=>q.readUInt16LE(i*2)/256).slice(32,-32);
const measured=precisionPeriodicFit(y,h.sample_s,1e6);console.log('hardware periodic',measured.hz,measured.rms,'residual bits',Math.log2(256/(Math.sqrt(12)*measured.rms)));
assert.ok(measured.rms>0 && measured.rms<.03);
