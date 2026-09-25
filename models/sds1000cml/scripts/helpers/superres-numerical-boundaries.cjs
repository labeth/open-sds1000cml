// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-019, REQ-SDS-142
const assert=require('node:assert/strict'),path=require('node:path');
const C=require(path.join(process.argv[2],'superres_comp.js')),E=require(path.join(process.argv[2],'superres_ets.js')),cases=[];
function check(name,f){f();cases.push({name,result:'pass'});}
check('target magnitude is minus 6.02 dB at fbw, despite minus 3 dB comment',()=>assert.equal(C.srCompTargetH(70e6,70e6,3),.5));
check('zero-bit auto budget selects 40 MHz despite 10 MHz raw Nyquist',()=>{const r=C.srCompAuto(0,10e6,.8);assert.equal(r.fbw,40e6);assert(r.fbw>.8*10e6);assert.equal(r.budgetDb,4);});
check('invalid sample period and all-gap inputs return original array',()=>{const x=Float32Array.from([1,2,3,4,5,6,7,8]);assert.equal(C.srCompensate(x,0).comp,x);const g=new Float32Array(16).fill(-1);assert.equal(C.srCompensate(g,1e-9).comp,g);});
check('linear trend is restored without compensation changes',()=>{const x=Float32Array.from({length:32},(_,i)=>50+i);assert.deepEqual([...C.srCompensate(x,1e-9).comp],[...x]);});
function setup(){const st=E.srEtsNew(64,1);st.f=1/16;return {st,x:Float64Array.from({length:256},(_,i)=>128+30*Math.cos(2*Math.PI*i/16))};}
check('ETS rejects companion shorter than alignment channel',()=>{const {st,x}=setup();assert.equal(E.srEtsFeed(st,x,new Float64Array(255)),'folded');assert.equal(st.c[1].present,false);});
check('ETS folds all samples of a longer companion',()=>{const {st,x}=setup();assert.equal(E.srEtsFeed(st,x,new Float64Array(512).fill(100)),'folded');assert(Math.abs(st.c[1].cnt.reduce((a,b)=>a+b,0)-512)<1e-8);});
check('ETS does not mask negative companion gap codes',()=>{const {st,x}=setup();assert.equal(E.srEtsFeed(st,x,new Float64Array(256).fill(-1)),'folded');assert.equal(st.c[1].present,true);assert(Math.abs(st.c[1].sum.reduce((a,b)=>a+b,0)+256)<1e-8);});
console.log(JSON.stringify({result:'pass',cases,scope:'Bounded characterizations of original compensation/ETS functions, including documented mismatches and input assumptions; no physical calibration or timing qualification.'},null,2));
