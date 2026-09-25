"use strict";
// REQ-SDS-068 / REQ-SDS-125: isolated globals and recording callbacks, no network.
const assert=require('node:assert/strict'), fs=require('node:fs'), vm=require('node:vm'), path=require('node:path');
const source=fs.readFileSync(path.join(process.argv[2],'app_views.js'),'utf8');
function fixture() {
  const elements={}, calls=[], timers=[];
  const element=id=>elements[id] ||= {value:'0',textContent:'',width:400,height:200,classList:{toggle(){},remove(){}},getBoundingClientRect(){return {width:400,height:200};}};
  const ctx={$:element,dpr:1,window:{devicePixelRatio:1},bode:{armed:false,lastN:-1},spg:{armed:false,sg:null,lastSeq:-1,ch:1},frame:null,st:null,
    setInterval(fn,delay){timers.push({fn,delay});},fetch(url,opts){calls.push({url,opts});return Promise.resolve({json:async()=>({ok:true,n:2,freq:[1,2]})});},
    send(...args){calls.push({send:args});},eng:(v,u)=>`${v} ${u}`,peakNyq:()=>100,
    sgNew:()=>({rows:0,floorDb:-60}),sgPushRow(sg){sg.rows++;},sgClear(sg){sg.rows=0;},spectrum:()=>({mags:[1],half:1,peak:1,nyq:100}),
    glCardCtx:()=>null,glCardEnd(){},sgBlit(){},bodeDraw(){},DIMCOL:'#abcdef',GRIDCOL:'#000000',AXISCOL:'#111111',C1COL:'#222222',C2COL:'#333333'};
  vm.runInNewContext(source,ctx);
  return {ctx,elements,element,calls,timers};
}
const cases=[];
function check(name,fn){fn();cases.push({name,result:'pass'});}
check('formatters preserve integer zeros and gate absent measurements',()=>{
  const {ctx:c}=fixture();assert.equal(c.fmtTdiv(100),'100 s');assert.equal(c.fmtVdiv(.02),'20 mV');
  assert.equal(c.fmtMeas('Freq',{has_timing:false,freq:100}),'—');assert.equal(c.fmtMeas('unknown',{}),'—');
  assert.equal(c.hexA('#102030',.5),'rgba(16,32,48,0.5)');
});
check('label fitting trims a prefix without an ellipsis',()=>{
  const {ctx:c}=fixture();const g={measureText:s=>({width:s.length*4})};assert.equal(c.fitLabel(g,'abcdef',12),'abc');assert.equal(c.fitLabel(g,'x',6),'');
});
check('spectrogram skips unarmed, envelope and duplicate frames',()=>{
  const {ctx:c}=fixture();c.frame={seq:1,c1:Array(32).fill(128)};c.spgPushCurrent();assert.equal(c.spg.sg,null);
  c.spg.armed=true;c.frame.is_env=true;c.spgPushCurrent();assert.equal(c.spg.sg,null);
  c.frame.is_env=false;c.spgPushCurrent();assert.equal(c.spg.sg.rows,1);c.spgPushCurrent();assert.equal(c.spg.sg.rows,1);
});
check('changing channel retains old rows and last sequence',()=>{
  const {ctx:c,element}=fixture();c.spg.armed=true;c.frame={seq:1,c1:Array(32).fill(128),c2:Array(32).fill(129)};c.spgPushCurrent();
  element('spgCh').value='2';element('spgCh').onchange();c.spgPushCurrent();assert.equal(c.spg.ch,2);assert.equal(c.spg.sg.rows,1);assert.equal(c.spg.lastSeq,1);
  c.frame.seq=2;c.spgPushCurrent();assert.equal(c.spg.sg.rows,2);
});
check('floor change before allocation is discarded',()=>{
  const {ctx:c,element}=fixture();element('spgFloor').value='-20';element('spgFloor').onchange();assert.equal(c.spgEnsure().floorDb,-60);
});
check('clear leaves last sequence unchanged',()=>{
  const {ctx:c,element}=fixture();c.spg.armed=true;c.frame={seq:1,c1:Array(32).fill(128)};c.spgPushCurrent();element('spgClear').onclick();c.spgPushCurrent();assert.equal(c.spg.sg.rows,0);assert.equal(c.spg.lastSeq,1);
});
check('equal Bode reference and DUT still post an arm command',()=>{
  const {ctx:c,element,calls}=fixture();element('bodeRef').value='0';element('bodeDut').value='0';element('bodeArm').onclick();
  assert.equal(c.bode.armed,true);const b=JSON.parse(calls[0].opts.body);assert.deepEqual(b,{control:'bodemode',value:1,lo:0,hi:0});
});
check('periodic callbacks are registered at 1000 and 80 milliseconds',()=>{
  assert.deepEqual(fixture().timers.map(t=>t.delay),[1000,80]);
});
console.log(JSON.stringify({result:'pass',cases,scope:'Eight deterministic view-glue characterizations with recording globals. Browser rendering and measurement accuracy are separate evidence.'},null,2));
