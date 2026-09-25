// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-019
const assert=require('node:assert/strict'),path=require('node:path');
const SR=require(path.join(process.argv[2],'superres.js'));
const M=require(path.join(process.argv[2],'superres_math.js'));
const G=require(path.join(process.argv[2],'superres_gate.js'));
const cases=[];
function check(name,f){f();cases.push({name,result:'pass'});}
function signal(){return Float32Array.from({length:256},(_,i)=>128+40*Math.sin(2*Math.PI*i/32));}
check('narrow pulse leaves both midpoint quantiles at baseline',()=>{const x=new Float32Array(100).fill(100);x.fill(200,40,45);assert.equal(M.srMidSwing(x),100);});
check('empty mean and midpoint remain NaN',()=>{assert(Number.isNaN(M.srMeanStd([]).mean));assert(Number.isNaN(M.srMidSwing([])));});
check('flat manual gate failure leaves mutated locked state',()=>{const s=signal();s.fill(128,0,32);const st=SR.srNew(256,4);assert.equal(SR.srSeedRef(st,s,null,-1,{lo:0,hi:16}),false);assert.equal(st.gated,true);assert.equal(st.userRef,true);assert.equal(st.frames,1);assert.equal(st.gtpl,null);});
check('companion appearing after automatic seed remains absent',()=>{const s=signal(),st=SR.srNew(256,4);assert.equal(SR.srFeed(st,s,null,{}),'stacked');assert.equal(SR.srFeed(st,s,s,{}),'stacked');assert.equal(st.c[1].ref,null);assert.equal(SR.srResult(st).mean2,null);});
check('initial clipped companion is accumulated before subsequent clip exclusion',()=>{const s=signal(),c=new Float32Array(256).fill(253),st=SR.srNew(256,4);assert.equal(SR.srFeed(st,s,c,{}),'stacked');assert.equal(st.c[1].clipSkips,0);assert.equal(SR.srResult(st).mean2[0],253);assert.equal(SR.srFeed(st,s,c,{}),'stacked');assert.equal(st.c[1].clipSkips,1);});
check('duplicate decoded centers count one physical occurrence twice',()=>{const s=signal(),st=SR.srNew(256,4);assert.equal(SR.srSeedRef(st,s,null,-1,{lo:64,hi:96}),true);const before=st.hits;assert.equal(G.srGateFeed(st,s,null,{hitCenters:[64,64],centerR:2}),'stacked:2');assert.equal(st.hits,before+2);});
check('zero shift interpolation leaves final raw sample unfilled',()=>{const s=signal(),st=SR.srNew(256,1);SR.srAccum(st,s,0);const r=SR.srResult(st);assert.equal(r.mean[254],s[254]);assert.equal(r.mean[255],-1);});
check('measurement accepts two valid values across a 16-sample span',()=>{const x=new Float32Array(16).fill(-1);x[0]=100;x[15]=200;const r=SR.srMeasure(x,1,0,0);assert(r);assert.equal(r.vpp,100);assert.equal(r.vmean,22);});
console.log(JSON.stringify({result:'pass',cases,scope:'Characterization of eight original-source boundary behaviors, including known input/state limitations; passing assertions do not mean these limitations are corrected.'},null,2));
