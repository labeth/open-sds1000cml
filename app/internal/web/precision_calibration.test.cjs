// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-205
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const els={precisionFit:{},precisionFitResult:{},precisionChannel:{value:'0'},precisionModel:{value:'sine'},precisionFitCanvas:{getContext:()=>({fillRect(){},beginPath(){},lineTo(){},moveTo(){},stroke(){},fillText(){}}),width:700,height:200}};
const c={document:{getElementById:id=>els[id]},frame:{seq:1,c1:Array.from({length:200},(_,i)=>128+30*Math.sin(i*2*Math.PI/20)),m1:{freq:50},sample_s:.001,cols:200,col_span_s:.2,vpc1:.04},view:{win:{a:0,b:1}},st:{vdiv1:1,probe1:1},eng:(v,u)=>v.toFixed(4)+u};
vm.createContext(c);vm.runInContext(fs.readFileSync('app_precision.js','utf8'),c);
els.precisionFit.onclick();const before=els.precisionFitResult.textContent;assert.match(before,/2\.4000V pp/);
c.st.vdiv1=.5;els.precisionFit.onclick();assert.equal(els.precisionFitResult.textContent,before,'stopped display settings cannot rescale capture volts');
for(const flag of ['peak_detect','is_env']){c.frame[flag]=true;els.precisionFit.onclick();assert.match(els.precisionFitResult.textContent,/min\/max envelopes cannot be fitted/);delete c.frame[flag];}
console.log('ALL PASS');
