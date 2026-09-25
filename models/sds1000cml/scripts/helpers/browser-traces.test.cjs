// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-070, REQ-SDS-071
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict'),path=require('node:path');
const root=process.argv[2], cases=[];
function fixture(){
 const els=new Map(),calls=[];
 const c=vm.createContext({Float64Array,frame:null,fftRaw:null,frozen:false,sr:{showing:false},view:{mode:'YT',c1:true,c2:true,win:{a:0,b:1},vwin:{a:0,b:1}},CW:101,CH:200,dpr:1,FFT_MAX:64,st:{running:true},specMemo:{1:{},2:{}},fftCh:{1:{sel:[],selIdx:new Set()},2:{sel:[],selIdx:new Set()}},mathFn:'off',mathMemo:{},compMemo:{src:null,map:new Map()},refs:{A:null,B:null},frameDtS:f=>f.dt_s,frameSpanS:(f,n)=>f.dt_s*n,spectrum:(values,nyq)=>({values:[...values],nyq}),redraw(){},scheduleRender(){},$:id=>{if(!els.has(id))els.set(id,{value:8,style:{},addEventListener(){},querySelectorAll(){return [];}});return els.get(id);}});
 for(const n of ['app_geom.js','app_draw.js','app_fft.js'])vm.runInContext(fs.readFileSync(path.join(root,n),'utf8'),c);
 c.updateRefRows=()=>{};
 const g={beginPath(){calls.push(['begin']);},moveTo(x,y){calls.push(['move',x,y]);},lineTo(x,y){calls.push(['line',x,y]);},stroke(){calls.push(['stroke']);}};
 return {c,g,calls};
}
function check(name,fn){fn(fixture());cases.push({name,result:'pass'});}
check('vertical code mapping and inverse agree across zoom',({c})=>{c.view.vwin={a:.2,b:.8};for(const code of [0,64,128,200,255])for(const z of [.5,1,4])assert(Math.abs(c.codeAtY(c.yFor(code,z)/c.CH,z)-code)<1e-10);});
check('sparse trace preserves gap pen-up and input bytes',({c,g,calls})=>{const a=[100,-1,150,155];c.drawTrace(g,a,'#fff',1,false);assert.equal(calls.filter(x=>x[0]==='move').length,2);assert.equal(calls.filter(x=>x[0]==='line').length,1);assert.deepEqual(a,[100,-1,150,155]);});
check('display inversion mirrors codes and leaves gap sentinels untouched',({c,g,calls})=>{const a=[100,-1,150];c.drawTrace(g,a,'#fff',1,true);const p=calls.filter(x=>x[0]==='move');assert.equal(p[0][2],c.yFor(155,1));assert.equal(p[1][2],c.yFor(105,1));assert.deepEqual(a,[100,-1,150]);});
check('math addition clips codes and propagates negative gaps',({c})=>{c.frame={c1:[200,-1,128],c2:[200,128,128]};c.mathFn='c1+c2';assert.deepEqual([...c.computeMathRaw()],[255,-1,128]);});
check('unequal math channel lengths produce NaN rather than a missing-data sentinel',({c})=>{c.frame={c1:[128,140],c2:[128]};c.mathFn='c1-c2';assert(Number.isNaN(c.computeMathRaw()[1]));});
check('reference capture copies and caps stored sample arrays',({c})=>{const a=new Int16Array(131073).fill(128);c.frame={c1:a,vpc1:.01};c.saveRef('A');assert(c.refs.A.c1.length<=65536);a[0]=9;assert.equal(c.refs.A.c1[0],128);assert.equal(c.refs.A.vpc1,.01);});
check('FFT gap filling retains a uniform grid and trims margins',({c})=>{const a=Array(40).fill(10);a[0]=a[39]=-1;a[10]=a[11]=-1;a[12]=16;const b=c.gapFill(a);assert.equal(b.length,38);assert.equal(b[9],12);assert.equal(b[10],14);assert.equal(a[10],-1);});
check('FFT gap filling rejects at most 32 surviving samples',({c})=>{assert.equal(c.gapFill(Array(32).fill(1)),null);assert.equal(c.gapFill(Array(33).fill(1)).length,33);assert.equal(c.gapFill(null),null);});
check('live FFT uses raw samples but frozen FFT uses displayed record',({c})=>{c.view.mode='FFT';c.frame={c1:[1],dt_s:4};c.fftRaw={c1:[2],sample_s:2};assert.equal(c.peakSrcCh(1),c.fftRaw.c1);assert.equal(c.peakNyq(),.25);c.frozen=true;assert.equal(c.peakSrcCh(1),c.frame.c1);assert.equal(c.peakNyq(),.125);});
check('raw FFT stride is derived from display frame length',({c})=>{c.view.mode='FFT';c.frame={c1:new Float64Array(256),dt_s:4};c.fftRaw={c1:new Float64Array(64).fill(1),sample_s:2};assert.equal(c.fftStride(),4);const s=c.spectrumFor(1);assert.equal(s.values.length,16);assert.equal(s.nyq,1/16);});
check('source-identity FFT cache does not invalidate on sample-period change',({c})=>{c.frame={c1:new Float64Array(40).fill(1),dt_s:2};const a=c.spectrumFor(1);c.frame.dt_s=4;assert.equal(c.spectrumFor(1),a);assert.equal(a.nyq,.25);assert.equal(c.displayNyq(),.125);});
check('missing raw C2 falls back to display C2 but retains raw timebase',({c})=>{c.view.mode='FFT';c.frame={c1:[1],c2:[3],dt_s:4};c.fftRaw={c1:[2],sample_s:2};assert.equal(c.peakSrcCh(2),c.frame.c2);assert.equal(c.peakSpanS(c.frame.c2),2);});
check('peak amplitude uses displayed calibration with fallback',({c})=>{c.frame={vpc1:.1};c.specMemo[1].spec={half:32,peak:16};assert.equal(c.peakVolts(1,{db:0}),.1);delete c.frame.vpc1;assert.equal(c.peakVolts(1,{db:0}),.04);});
console.log(JSON.stringify({result:'pass',cases,scope:'Original trace/FFT/geometry functions in isolated VM; graphics calls and spectrum transform stubbed. Characterizes known limits; no GPU pixels, true FFT spectral accuracy, browser interaction or hardware operation.'},null,2));
