// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-023, REQ-SDS-069
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict'),path=require('node:path');
function fixture(root){
 const els=new Map(),timers=[],requests=[],applied=[],exports=[];
 const el=id=>{if(!els.has(id))els.set(id,{id,value:'0',textContent:'',style:{},dataset:{},options:[],classList:{add(){},remove(){},toggle(){},contains(){return false;}},addEventListener(){},appendChild(){},getAttribute(){},setAttribute(){},querySelectorAll(){return [];}});return els.get(id);};
 const document={hidden:false,activeElement:null,getElementById:el,body:{classList:{toggle(){}},appendChild(){}},createElement:()=>el('created')};
 const c=vm.createContext({console,document,window:{devicePixelRatio:1,addEventListener(){}},getComputedStyle:()=>({getPropertyValue:()=>''}),setTimeout:(fn,ms)=>{timers.push({fn,ms});return timers.length;},performance:{now:()=>1000},Math,fetch:async(...args)=>{requests.push(args);return {ok:true,status:200,json:async()=>({ok:true}),arrayBuffer:async()=>new ArrayBuffer(0)};},decodeBinFrame:()=>({seq:2}),requestAnimationFrame:fn=>{timers.push({fn,ms:'raf'});return timers.length;}});
 for(const n of ['app.js','app_core.js','app_controls.js'])vm.runInContext(fs.readFileSync(path.join(root,n),'utf8'),c);
 c.applyFrame=f=>applied.push(f);c.applyStatus=()=>{};c.redraw=()=>{};c.showSuperseded=()=>vm.runInContext('superseded=true',c);c.exportFile=(...args)=>exports.push(args);
 return {c,el,timers,requests,applied,exports,eval:s=>vm.runInContext(s,c)};
}
const cases=[];
async function check(name,fn){await fn(fixture(process.argv[2]));cases.push({name,result:'pass'});}
(async()=>{
await check('frozen display skips network and schedules an idle retry',async f=>{f.eval('frozen=true');await f.c.pollFrameBin();assert.equal(f.requests.length,0);assert.equal(f.timers[0].ms,90);});
await check('superseded display stops without another timer',async f=>{f.eval('superseded=true');await f.c.pollFrameBin();assert.equal(f.requests.length,0);assert.equal(f.timers.length,0);});
await check('409 response supersedes display and stops polling',async f=>{f.c.fetch=async()=>({status:409});await f.c.pollFrameBin();assert.equal(f.eval('superseded'),true);assert.equal(f.timers.length,0);});
await check('freeze during display request prevents applying late reply',async f=>{f.c.fetch=async()=>{f.eval('frozen=true');return {ok:true,status:200,arrayBuffer:async()=>new ArrayBuffer(0)};};await f.c.pollFrameBin();assert.equal(f.applied.length,0);assert.equal(f.eval('lastSeq'),0);});
await check('invalid decoded frame increments failures and backs off',async f=>{f.c.decodeBinFrame=()=>null;await f.c.pollFrameBin();assert.equal(f.eval('binFailures'),1);assert(f.timers[0].ms>=250&&f.timers[0].ms<500);assert.equal(f.applied.length,0);});
await check('successful display request includes claim epoch and resets failures',async f=>{f.eval('myEpoch=42;binFailures=5');await f.c.pollFrameBin();assert(f.requests[0][0].includes('&epoch=42'));assert.equal(f.eval('binFailures'),0);assert.equal(f.applied.length,1);assert.equal(f.timers[0].ms,10);});
await check('raw FFT late reply is stored even after freeze',async f=>{f.c.decodeBinFrame=()=>({seq:4,c1:[1],sample_s:1});f.c.fetch=async()=>{f.eval('frozen=true');return {ok:true,arrayBuffer:async()=>new ArrayBuffer(0)};};await f.c.fetchFftRaw();assert.equal(f.eval('fftRaw.seq'),4);assert.equal(f.eval('fftRawBusy'),false);});
await check('raw FFT busy flag prevents overlapping request',async f=>{f.eval('fftRawBusy=true');await f.c.fetchFftRaw();assert.equal(f.requests.length,0);});
await check('send parses JSON even on failed HTTP status',async f=>{f.c.fetch=async()=>({ok:false,status:500,json:async()=>({ok:true})});assert.equal((await f.c.send('run',1)).ok,true);});
await check('run click updates status optimistically despite rejected control',async f=>{f.eval('st={running:false}');f.c.send=async()=>({ok:false});f.el('run').onclick();assert.equal(f.eval('st.running'),true);});
await check('probe-scaled trigger conversion uses selected-channel calibration',f=>{f.eval('st={probe1:10,probe2:100,trig_source:1,trig_zero:30000,trig_cpv:1000}');assert.equal(f.eval('trigCodeFor(100)'),29000);});
await check('CSV export uses sample pitch, calibration and blank gaps',f=>{f.eval('frame={seq:7,dt_s:2,c1:[128,-1,130],c2:[129],vpc1:.5,vpc2:.25,off1_v:1,off2_v:0,sr_view:true}');f.el('eCSV').onclick();assert.equal(f.exports[0][0],'scope-7-superres.csv');const rows=f.exports[0][2].trim().split('\n');assert.equal(rows[2],'0.000000e+0,-1.000000e+0,2.500000e-1');assert.equal(rows[3],'2.000000e+0,,');assert.equal(rows[4],'4.000000e+0,0.000000e+0,');});
await check('C2-only CSV export is a no-op',f=>{f.eval('frame={seq:7,c2:[128,129],dt_s:2}');f.el('eCSV').onclick();assert.equal(f.exports.length,0);});
console.log(JSON.stringify({result:'pass',cases,scope:'Original client/control scripts with isolated DOM, network and timer stubs. No real browser, download, device command, asynchronous event scheduling or hardware qualification.'},null,2));
})().catch(e=>{console.error(e);process.exitCode=1;});
