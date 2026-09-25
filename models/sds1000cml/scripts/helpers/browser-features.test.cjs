// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-018
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict'),path=require('node:path');
const root=process.argv[2],cases=[];
const precision=vm.createContext({});vm.runInContext(fs.readFileSync(path.join(root,'app_precision.js'),'utf8'),precision);
async function check(name,fn){await fn();cases.push({name,result:'pass'});}
function fixture(){
 const elements=new Map(),intervals=[],requests=[],commands=[];
 const $=id=>{if(!elements.has(id)){const classes=new Set();elements.set(id,{value:'',style:{},textContent:'',classList:{contains:k=>classes.has(k),add:k=>classes.add(k),remove:k=>classes.delete(k),toggle:(k,on)=>{if(on===undefined)on=!classes.has(k);on?classes.add(k):classes.delete(k);}},addEventListener(){}});}return elements.get(id);};
 const c=vm.createContext({$,document:{querySelectorAll:()=>[]},dcfg:{proto:'uart',line:1,baud:9600,bits:8,auto:true,msb:true,watch:false,watchErr:true,watchMatch:'',captures:[],reviewIdx:-1,lastCapKey:'',hist:[],lastStreamSeq:0},st:{serial_mode:1},frame:{c1:[128],dt_s:1,seq:1},view:{win:{a:0,b:1},mode:'YT'},setInterval:fn=>intervals.push(fn),fetch:async(...a)=>{requests.push(a);return {json:async()=>({ok:true})};},send:(...a)=>commands.push(a),frameDtS:f=>f.dt_s,recompute(){},redraw(){},updateMeas(){},updateCursors(){}});
 vm.runInContext(fs.readFileSync(path.join(root,'app_serialtrig.js'),'utf8'),c);
 vm.runInContext(fs.readFileSync(path.join(root,'app_decode.js'),'utf8'),c);
 c.updateDecodeResults=()=>{};c.updateCaptureList=()=>{};
 return {c,$,intervals,requests,commands};
}
(async()=>{
await check('sine fit recovers synthetic frequency, amplitude and offset',()=>{const n=128,dt=1/128,y=Array.from({length:n},(_,i)=>128+20*Math.sin(2*Math.PI*4*i*dt+.3));const f=precision.precisionFitSignal(y,dt,4,'sine');assert(Math.abs(f.hz-4)<.001);assert(Math.abs(Math.abs(f.amplitude)-20)<.01);assert(Math.abs(f.offset-128)<.01);assert(f.rms<.01);});
await check('periodic fit removes supplied harmonic distortion',()=>{const n=128,dt=1/128,y=Array.from({length:n},(_,i)=>128+20*Math.sin(2*Math.PI*4*i*dt)+3*Math.cos(2*Math.PI*8*i*dt));const f=precision.precisionPeriodicFit(y,dt,4);assert(f.rms<.001);assert(f.harmonics>=2);});
await check('fit rejects short record, invalid dt and insufficient cycles',()=>{for(const [y,dt,f] of [[Array(32).fill(128),.01,4],[Array(128).fill(128),0,4],[Array(128).fill(128),.001,1]])assert.throws(()=>precision.precisionFitSignal(y,dt,f,'sine'));});
await check('byte parsing rejects malformed tokens without dropping them',()=>{const {c}=fixture();assert.equal(c.stParseBytes('AA GG BB'),null);assert.deepEqual([...c.stParseBytes('0xff, 3C')],[255,60]);assert.equal(c.stParseBytes('0XFF'),null);});
await check('byte parsing truncates valid long patterns but validates their tail',()=>{const {c}=fixture();assert.equal(c.stParseBytes(Array(65).fill('AA').join(' ')).length,64);assert.equal(c.stParseBytes(Array(64).fill('AA').join(' ')+' GG'),null);});
await check('address parser permits wildcard and only seven-bit addresses',()=>{const {c}=fixture();assert.equal(c.stParseAddr(''),-1);assert.equal(c.stParseAddr('7f'),127);assert.equal(c.stParseAddr('80'),null);});
await check('rejected configuration does not arm serial triggering',async()=>{const {c,$,commands}=fixture();c.fetch=async()=>({json:async()=>({ok:false})});await $('stArm').onclick();assert.equal(c.stArmed(),false);assert.equal(commands.length,0);});
await check('failed serial push retains signature and suppresses timer retry',async()=>{const {c,$,intervals}=fixture();let count=0;c.fetch=async()=>{count++;throw Error('offline');};$('stArm').classList.add('on');await assert.rejects(c.stPush());intervals[0]();await Promise.resolve();assert.equal(count,1);});
await check('unsupported decode protocol disarms serial triggering',()=>{const {c,$,commands}=fixture();$('stArm').classList.add('on');c.dcfg.proto='can';c.stOnDecodeChange();assert.deepEqual(commands,[['serialmode',0]]);});
await check('C2-only frame is not dispatched by decode UI',()=>{const {c}=fixture();c.frame={c2:[128]};let called=false;c.decodeUART=()=>{called=true;};c.computeDecode();assert.equal(called,false);assert.equal(c.dcfg.result,null);});
await check('watch reports error plus case-insensitive transcript match',()=>{const {c}=fixture();c.dcfg.watchMatch='abc';assert.equal(c.watchReason({spans:[{kind:'nak'}],text:'ABC'}),'error+abc');c.dcfg.watchMatch='/[/';assert.equal(c.watchReason({spans:[],text:'abc'}),'');});
await check('watch dedup persists across a nonmatching frame',()=>{const {c}=fixture();c.dcfg.watch=true;let result={ok:true,text:'same',spans:[{kind:'nak'}]};c.decodeUART=()=>result;c.computeDecode();result={ok:true,text:'different',spans:[]};c.computeDecode();result={ok:true,text:'same',spans:[{kind:'nak'}]};c.computeDecode();assert.equal(c.dcfg.captures.length,1);});
await check('history and capture buffers cap at 200 and 80 entries',()=>{const {c}=fixture();c.dcfg.watch=true;c.dcfg.stream=true;for(let i=1;i<=210;i++){c.frame={c1:[128],dt_s:1,seq:i,stream_seq:i};c.decodeUART=()=>({ok:true,text:String(i),spans:[{kind:'nak'}]});c.computeDecode();}assert.equal(c.dcfg.hist.length,200);assert.equal(c.dcfg.captures.length,80);assert.equal(c.dcfg.captures[0].seq,131);});
console.log(JSON.stringify({result:'pass',cases,scope:'Original precision fits on synthetic arrays and decode/serial UI control flow with isolated DOM, decoder, network and timer stubs. No browser pixels, physical metrology, protocol truth or device commands.'},null,2));
})().catch(e=>{console.error(e);process.exitCode=1;});
