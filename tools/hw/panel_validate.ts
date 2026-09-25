// Live front-panel behavior checks. All control writes go through /api/panel.
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
import {mkdirSync, writeFileSync, appendFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.env.PANEL_EVIDENCE || 'validation/2026-09-24-panel-current';
const phase=process.argv[2] || 'basic';
mkdirSync(out,{recursive:true});
const results:any[]=[]; const touched=new Set<string>();
const delay=(ms:number)=>new Promise(r=>setTimeout(r,ms));
async function get(path:string){const r=await fetch(base+path,{signal:AbortSignal.timeout(10000)}); assert(r.ok,`${path}: HTTP ${r.status}`);return r.json();}
const status=()=>get('/api/status');
function slim(s:any){ const {acq_log,cmd_log,...rest}=s;return rest; }
async function send(body:any){const r=await fetch(base+'/api/panel',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(10000)});const reply=await r.json();assert.equal(reply.ok,true,JSON.stringify({body,reply}));touched.add(body.button || body.knob);await delay(160);const s=await status();appendFileSync(`${out}/${phase}-events.jsonl`,JSON.stringify({at:new Date().toISOString(),input:body,status:slim(s)})+'\n');return s;}
const press=(button:string)=>send({button});const knob=(name:string,dir=1,steps=1)=>send({knob:name,dir,steps});
async function until(pred:(s:any)=>boolean,timeout=10000){const end=Date.now()+timeout;let s;do{s=await status();if(pred(s))return s;await delay(150);}while(Date.now()<end);throw Error('state timeout: '+JSON.stringify(slim(s)));}
async function test(name:string,fn:()=>Promise<any>){try{await fn();results.push({name,result:'pass'});console.log('PASS',name);}catch(e:any){results.push({name,result:'fail',error:e.message});console.log('FAIL',name,e.message.slice(0,450));}writeFileSync(`${out}/${phase}-results.json`,JSON.stringify({phase,base,results,touched:[...touched]},null,2));}
async function close(){if((await status()).panel.Open)await press('menu');}
async function open(button:string,title:string){await close();const s=await press(button);assert.equal(s.panel.Title,title);return s;}
async function screenshot(name:string){const r=await fetch(base+'/api/screen.png',{signal:AbortSignal.timeout(10000)});assert(r.ok);writeFileSync(`${out}/${phase}-${name}.png`,Buffer.from(await r.arrayBuffer()));}
async function running(){if(!(await status()).running)await press('runstop');}
async function timebase(target:number){for(let n=0;n<40;n++){const s=await status();if(Math.abs(s.tdiv_s-target)<target*1e-5)return;await knob('tdiv',s.tdiv_s<target?1:-1);}throw Error('timebase not reached');}
async function signalCheck(name:string){await test(name,async()=>{await running();await timebase(1e-7);await delay(700);const reply=await fetch(base+'/api/frame.bin?cols=1024&full=1',{signal:AbortSignal.timeout(10000)});assert(reply.ok);const f=decodeBinFrame(await reply.arrayBuffer());assert(f,'invalid binary frame');writeFileSync(`${out}/${phase}-${name}-frame.json`,JSON.stringify(f));for(const ch of ['m1','m2']){assert(f[ch]?.has_timing,`${ch}: no resolved waveform`);assert(Math.abs(f[ch].freq-5e6)<250000,`${ch}: ${f[ch].freq} Hz, expected 5 MHz`);}assert(!f.clip1&&!f.clip2,'input clipping');assert(f.coherent,'incoherent frame');});}
async function toggle(button:string,key:string,panel=false){const before=await status();const after=await press(button);assert.notDeepEqual(panel?after.panel[key]:after[key],panel?before.panel[key]:before[key]);const restored=await press(button);assert.deepEqual(panel?restored.panel[key]:restored[key],panel?before.panel[key]:before[key]);}
async function soft(slot:number,field:string,panel=false){const a=await status();const b=await press('f'+slot);assert.notDeepEqual(panel?b.panel[field]:b[field],panel?a.panel[field]:a[field],`F${slot} ${field}`);const c=await knob('adjust',-1);assert.deepEqual(panel?c.panel[field]:c[field],panel?a.panel[field]:a[field],`ADJUST reversal ${field}`);}
if(phase==='basic'){
 await test('latest build and panel snapshot',async()=>{const s=await status();assert(s.version.includes('f436963'));assert(s.panel);assert(!s.wedged);writeFileSync(`${out}/status-new-build.json`,JSON.stringify(s,null,2));});
 await test('AUTO starts; locks other inputs; second AUTO cancels',async()=>{let s=await press('auto');assert(s.panel.AutosetBusy,'autoset did not start');const visible=s.panel.ShowC1;s=await press('ch1');assert.equal(s.panel.ShowC1,visible);await press('auto');await until(s=>!s.panel.AutosetBusy,15000);});
 await test('AUTO completes on live input',async()=>{await press('auto');await until(s=>!s.panel.AutosetBusy,60000);});
 await signalCheck('five-megahertz-before-controls');
 await test('RUN/STOP toggles both ways',()=>toggle('runstop','running'));
 await test('SINGLE captures and stops',async()=>{await running();const before=await status();await press('single');const s=await until(s=>!s.running,12000);assert(s.seq>before.seq,'no newly published single frame');await press('runstop');assert((await status()).running);});
 for(const [button,key] of [['ch1','ShowC1'],['ch2','ShowC2'],['measure','ShowMeas']])await test(button+' toggles display',()=>toggle(button,key,true));
 await test('MATH cycles all five modes',async()=>{const start=(await status()).panel.MathMode;for(let i=1;i<=5;i++)assert.equal((await press('math')).panel.MathMode,(start+i)%5);});
 await test('V/DIV pushes select trigger source',async()=>{assert.equal((await press('ch1vdivpush')).trig_source,0);assert.equal((await press('ch2vdivpush')).trig_source,1);});
 await test('TRIGGER LEVEL push reverses edge',()=>toggle('triglvlpush','trig_rising'));
 for(const [button,title] of [['trigmenu','TRIGGER'],['acquire','ACQUIRE'],['display','DISPLAY'],['horizmenu','HORIZ'],['cursors','CURSOR'],['ref','REF A/B']])await test(button+' opens menu',()=>open(button,title));
 await test('MENU opens main and closes overlay',async()=>{await close();assert.equal((await press('menu')).panel.Title,'MENU');assert.equal((await press('menu')).panel.Open,false);});
 for(const [name,key] of [['tdiv','tdiv_s'],['ch1vdiv','vdiv1'],['ch2vdiv','vdiv2'],['ch1pos','off1_v'],['ch2pos','off2_v'],['triglevel','trig_code'],['horizpos','trig_pos_frac']])await test(name+' rotation changes and restores',async()=>{const a=await status();const dir=(name==='ch1vdiv'||name==='ch2vdiv') && a[key]>=10 || (name==='ch1pos'||name==='ch2pos') && a[key]>=39 ? -1:1;const b=await knob(name,dir);assert.notEqual(b[key],a[key]);const c=await knob(name,-dir);assert(Math.abs(c[key]-a[key])<Math.max(1e-10,Math.abs(a[key])*1e-6),`${key}: ${a[key]} -> ${c[key]}`);});
 await test('ADJUST edits selected menu setting',async()=>{await open('trigmenu','TRIGGER');const a=await status();assert.notEqual((await knob('adjust',1)).norm,a.norm);assert.equal((await knob('adjust',-1)).norm,a.norm);});
 await close();
 for(const button of ['print','saverecall','set50','defaultsetup','force','help','ch1pospush','ch2pospush','tdivpush','horizpospush'])await test(button+' accepted with no assigned action',async()=>{const keys=['running','norm','tdiv_s','trig_code','trig_source','trig_rising','vdiv1','vdiv2','off1_v','off2_v','panel'];const a=await status();const b=await press(button);for(const k of keys)assert.deepEqual(b[k],a[k],k);});
 await screenshot('overview');
 await signalCheck('five-megahertz-after-controls');
}else if(phase==='menus'){
 await test('trigger F1–F5 and reverse adjustment',async()=>{await open('trigmenu','TRIGGER');await press('f5');await knob('adjust',-1);for(const [i,k] of [[1,'norm'],[2,'trig_rising'],[3,'trig_source'],[4,'trig_type'],[5,'holdoff_s']] as any)await soft(i,k);});
 await test('display F1–F5',async()=>{await open('display','DISPLAY');for(const [i,k] of [[1,'ShowC1'],[2,'ShowC2'],[3,'ShowMeas'],[4,'ViewMode'],[5,'MathMode']] as any)await soft(i,k,true);});
 await test('horizontal timebase, trigger position and zoom',async()=>{await open('horizmenu','HORIZ');await soft(1,'tdiv_s');await soft(2,'trig_pos_frac');await soft(3,'Zoom',true);await press('f3');const a=await status();assert.notEqual((await knob('horizpos',1)).panel.ZoomOff,a.panel.ZoomOff);await knob('horizpos',-1);await knob('adjust',-1);assert.equal((await status()).panel.Zoom,1);});
 await test('channel coupling/probe/persistence',async()=>{await open('display','DISPLAY');assert.equal((await press('display')).panel.Title,'CHANNEL');for(const [i,k] of [[1,'cpl1'],[2,'cpl2'],[3,'probe1'],[4,'probe2']] as any)await soft(i,k);await soft(5,'Persist',true);});
 await test('cursors enable/type/selection/nudge/ADJUST',async()=>{await open('cursors','CURSOR');if(!(await status()).panel.CurOn)await press('f1');const type=(await status()).panel.CurType;assert.equal((await press('f2')).panel.CurType,1-type);await press('f2');const selected=(await status()).panel.CurSel;assert.equal((await press('f3')).panel.CurSel,1-selected);const a=await status();const key=a.panel.CurType===0?'CurX':'CurY';const sel=a.panel.CurSel;assert((await press('f4')).panel[key][sel]<a.panel[key][sel]);assert.equal((await press('f5')).panel[key][sel],a.panel[key][sel]);assert((await knob('adjust',1)).panel[key][sel]>a.panel[key][sel]);await knob('adjust',-1);await screenshot('cursors');await press('f1');});
 await test('reference capture/show/clear',async()=>{await open('ref','REF A/B');await press('f1');await press('f2');let s=await status();assert(!s.panel.Items[0].Value.includes('empty'));for(const i of [3,4]){const a=(await status()).panel.Items[i-1].Value;assert.notEqual((await press('f'+i)).panel.Items[i-1].Value,a);}await screenshot('references');await press('f5');s=await status();assert(s.panel.Items[2].Value==='-' && s.panel.Items[3].Value==='-',JSON.stringify(s.panel.Items));});
 await test('acquisition mode/count/ETS/memory and empty F5',async()=>{await open('acquire','ACQUIRE');await soft(1,'acq_mode');for(let i=0;(await status()).acq_mode!==1&&i<4;i++)await press('f1');await soft(2,'avg_count');if((await status()).band==='sram'){const before=await status();await press('f3');await press('f4');const after=await status();assert.equal(after.ets,before.ets);assert.equal(after.mem_depth,before.mem_depth);assert(after.panel.Items[3].Value.includes('fixed'));}else{await soft(3,'ets');await soft(4,'mem_depth');}const a=(await status()).panel;assert.deepEqual((await press('f5')).panel,a);for(let i=0;(await status()).acq_mode!==0&&i<5;i++)await press('f1');});
 await test('main F1–F5 page navigation',async()=>{for(const [i,title] of [[1,'TRIGGER'],[2,'ACQUIRE'],[3,'DISPLAY'],[4,'HORIZ'],[5,'DECODE']] as any){await close();await press('menu');assert.equal((await press('f'+i)).panel.Title,title);}});
 await test('decode protocols and configuration',async()=>{await close();await press('menu');await press('f5');for(let i=0;(await status()).panel.DecProto!==0&&i<5;i++)await press('f1');for(const proto of [1,2,3,4]){assert.equal((await press('f1')).panel.DecProto,proto);if(proto===1)await soft(2,'DecFormat',true);if(proto===2){await soft(2,'DecBaud',true);await soft(3,'DecChA',true);await soft(4,'DecFormat',true);}if(proto===3){await soft(2,'DecChA',true);await soft(3,'DecChB',true);await soft(4,'DecFormat',true);}if(proto===4){await soft(2,'DecChA',true);await soft(3,'DecChB',true);const a=(await status()).panel;await press('f4');const b=(await status()).panel;assert(a.DecCPOL!==b.DecCPOL||a.DecCPHA!==b.DecCPHA);await knob('adjust',-1);await soft(5,'DecFormat',true);}}await press('f1');});
 await close();await signalCheck('five-megahertz-after-menus');
}else if(phase==='advanced'){
 await test('trigger qualifier softkeys',async()=>{
  await open('trigmenu','TRIGGER');
  for(let n=0;(await status()).trig_type!==0&&n<4;n++)await press('f4');
  for(const [type,title,count] of [[1,'PULSE',4],[2,'SLOPE',5],[3,'VIDEO',3]] as any){
   assert.equal((await press('f4')).trig_type,type);assert.equal((await press('trigmenu')).panel.Title,title);
   for(let i=1;i<=count;i++){const a=(await status()).panel.Items[i-1].Value;assert.notEqual((await press('f'+i)).panel.Items[i-1].Value,a);await knob('adjust',-1);}
   for(let i=count+1;i<=5;i++){const a=(await status()).panel;assert.deepEqual((await press('f'+i)).panel,a);}
   await press('trigmenu');
  }
  await press('f4');assert.equal((await status()).trig_type,0);
 });
 await test('UTILITY arms, ADJUST push selects gates/review, UTILITY cancels',async()=>{
  await running();await close();await press('utility');let s=await status();assert(s.panel.SRActive,s.panel.SRStatus);
  for(let focus=1;focus<=4;focus++){s=await press('adjustpush');assert.equal(s.panel.SRFocus,focus%4);if(focus<=2){const gate=s.panel.SRGate;await knob('adjust',focus===1?1:-1);assert.notDeepEqual((await status()).panel.SRGate,gate);}}
  for(const i of [1,2,3,4]){const a=(await status()).panel.Items[i-1].Value;assert.notEqual((await press('f'+i)).panel.Items[i-1].Value,a);await knob('adjust',-1);}
  await press('f5');await screenshot('superresolution');await press('utility');await delay(1200);const cancelled=await status();assert.equal(cancelled.panel.SRActive,false);assert.equal(cancelled.panel.SRFocus,0);
 });
 await test('mask menu mode, build settings, clear and build',async()=>{
  await open('acquire','ACQUIRE');await press('acquire');await press('acquire');assert.equal((await status()).panel.Title,'MASK');
  for(const i of [1,3,4]){const a=(await status()).panel.Items[i-1].Value;assert.notEqual((await press('f'+i)).panel.Items[i-1].Value,a);await knob('adjust',-1);}
  await press('f5');await press('f2');const built=await until(s=>!s.panel.MaskStatus.toLowerCase().includes('building'),30000);assert.match(built.panel.MaskStatus,/ready/,built.panel.MaskStatus);await screenshot('mask');
 });
 await close();await signalCheck('five-megahertz-after-advanced');
}else{throw Error('unknown phase '+phase);}
writeFileSync(`${out}/${phase}-results.json`,JSON.stringify({phase,base,results,touched:[...touched]},null,2));
console.log(JSON.stringify({passed:results.filter(r=>r.result==='pass').length,failed:results.filter(r=>r.result==='fail').length,touched:[...touched]}));
process.exitCode=results.some(r=>r.result==='fail')?1:0;
