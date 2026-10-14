// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
(async()=>{
const src=fs.readFileSync('app_eye.js','utf8');const code=src.slice(src.indexOf('async function ejOptimize()'),src.indexOf('// ==== wiring ===='));
let report='',targets=[],locks=0;
const button={classList:{
 // TRLC-LINKS: REQ-SDS-201
 add(){},
 // TRLC-LINKS: REQ-SDS-201
 remove(){}}};
const c={ejOptBusy:false,ej:{armed:true,st:{records:10}},st:{band:'sram',tdiv_s:20e-6},
 // TRLC-LINKS: REQ-SDS-201
 $:()=>button,
 // TRLC-LINKS: REQ-SDS-201
 ejStatus:t=>report=t,
 // TRLC-LINKS: REQ-SDS-201
 autoset:async()=>{},
 // TRLC-LINKS: REQ-SDS-201
 ejStepToNearest:async t=>{targets.push(t);c.st.tdiv_s=t;return t;},
 // TRLC-LINKS: REQ-SDS-201
 ejSleep:async()=>{},
 // TRLC-LINKS: REQ-SDS-201
 ejFreshState(){},
 // TRLC-LINKS: REQ-SDS-201
 ejWaitLock:async()=>{locks++;return 250;},
 // TRLC-LINKS: REQ-SDS-201
 ejResult:()=>({bitRate:2e6}),
 // TRLC-LINKS: REQ-SDS-201
 ejSettleBand:async()=>{},
 // TRLC-LINKS: REQ-SDS-201
 eng:(v,u)=>v+u};
vm.createContext(c);vm.runInContext(code,c);await c.ejOptimize();
assert.match(report,/optimized SRAM.*250\.0 samp\/UI/);assert.equal(locks,2);assert.equal(targets.length,2);assert.ok(targets.every(t=>Math.abs(t-6.4e-6)<1e-12),'choose record duration for 128 bits, preserve actual oversampling');assert.equal(c.ejOptBusy,false);
console.log('ALL PASS');
})().catch(e=>{console.error(e);process.exit(1)});
