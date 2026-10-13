// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-205
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const els={precisionFit:{},precisionFitResult:{},precisionChannel:{value:'0'},precisionModel:{value:'sine'},precisionFitCanvas:{getContext:()=>({fillRect(){},beginPath(){},lineTo(){},moveTo(){},stroke(){},fillText(){}}),width:700,height:200}};
const c={document:{getElementById:id=>els[id]},frame:{seq:1,c1:Array.from({length:200},(_,i)=>128+30*Math.sin(i*2*Math.PI/20)),m1:{freq:50},sample_s:.001,cols:200,col_span_s:.2,vpc1:.04},view:{win:{a:0,b:1}},st:{vdiv1:1,probe1:1,running:true},eng:(v,u)=>v.toFixed(4)+u};
vm.createContext(c);vm.runInContext(fs.readFileSync('app_precision.js','utf8'),c);
(async()=>{
await els.precisionFit.onclick();const before=els.precisionFitResult.textContent;assert.match(before,/2\.4000V pp/);
c.st.vdiv1=.5;await els.precisionFit.onclick();assert.equal(els.precisionFitResult.textContent,before,'stopped display settings cannot rescale capture volts');

// A display of min/max pairs (a screen of more samples than the page's columns, live or stopped): the fit
// takes a capture of samples through the raw feed and fits the whole visible window, averaged to at most
// 4096 points (a stopped record arrives as min/max pairs too).
const raw={seq:7,c1:Array.from({length:100000},(_,i)=>128+30*Math.sin(2*Math.PI*i*2e-9*50000)),sample_s:2e-9};
let asked=0;
c.fetch=async(url)=>{asked++;assert.match(url,/raw=1&lease=raw/);return {ok:true,arrayBuffer:async()=>null};};
c.decodeBinFrame=()=>raw;
c.frame.m1.freq=50000;
for(const flag of ['peak_detect','is_env']){
  c.frame[flag]=true;await els.precisionFit.onclick();
  assert.match(els.precisionFitResult.textContent,/sine fit, CH1, capture 7: 50000\.\d+Hz, 2\.4000V pp/,'a capture of samples, fitted: '+els.precisionFitResult.textContent.slice(0,90));
  assert.match(els.precisionFitResult.textContent,/The visible 100000 samples, averaged to 4000 points/);
  delete c.frame[flag];
}
assert.equal(asked,2,'one raw capture asked each time');
// stopped, no capture of samples comes: the advice names what works
c.frame.peak_detect=true;c.st.running=false;c.decodeBinFrame=()=>({unchanged:true});
await els.precisionFit.onclick();
assert.match(els.precisionFitResult.textContent,/min\/max envelopes cannot be fitted and no capture of samples came. Run, or a faster time\/div/);
delete c.frame.peak_detect;c.st.running=true;

// the window: [a, b] of the record, block-averaged, clipping judged on the samples
const w=c.precisionWindow(Array.from({length:100000},()=>100),.25,.75,2e-9,4096);
assert.equal(w.samples,50000);assert.equal(w.y.length,3846);assert.ok(Math.abs(w.dt-26e-9)<1e-15);
assert.throws(()=>c.precisionWindow([100,255,100],0,1,1,4096),/unclipped/);
console.log('ALL PASS');
})().catch((e)=>{console.error(e);process.exit(1);});
