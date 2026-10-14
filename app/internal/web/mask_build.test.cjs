// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const text=fs.readFileSync('app_zonemask.js','utf8');
const helper=text.slice(text.indexOf('function zmPackMask'),text.indexOf('// --- mask build from N raw frames'));
const code=helper+text.slice(text.indexOf('$("zmBuild").onclick'),text.indexOf('\n};',text.indexOf('$("zmBuild").onclick'))+3);
// TRLC-LINKS: REQ-SDS-014
async function run(reject,pos=.5){
 let seq=0,body,message;
 const els={zmBuild:{},zmN:{value:4},zmTolT:{value:0},zmTolV:{value:0},zmCh:{value:0}};
 const c={...require('./wsmath.js'),st:{win_cols:312500,trig_pos_frac:pos},zm:{},
 // TRLC-LINKS: REQ-SDS-014
 $:id=>els[id],
 // TRLC-LINKS: REQ-SDS-014
 zmCplOK:()=>true,
 // TRLC-LINKS: REQ-SDS-014
 zmVctx:()=>null,
 // TRLC-LINKS: REQ-SDS-014
 redraw:()=>{},
 // TRLC-LINKS: REQ-SDS-014
 zmStatus:t=>message=t,
 // TRLC-LINKS: REQ-SDS-014
 btoa:s=>Buffer.from(s,'binary').toString('base64'),
 // TRLC-LINKS: REQ-SDS-014
 decodeBinFrame:f=>f,
 // TRLC-LINKS: REQ-SDS-014
 fetch:async(url,opts)=>{
 // TRLC-LINKS: REQ-SDS-014
 if(url==='/api/mask'){body=JSON.parse(opts.body);body.lo=Array.from(Buffer.from(body.lo_b64,'base64'));body.hi=Array.from(Buffer.from(body.hi_b64,'base64'));return {ok:true,json:async()=>({ok:!reject,err:'rejected for test'})};}
 // TRLC-LINKS: REQ-SDS-014
 return {arrayBuffer:async()=>({seq:++seq,edge_x:2,sample_s:1e-6,tdiv_s:1e-3,cols:4,win_cols:4,c1:[60.25,60.25,190.75,190.75]})};
 }};
 vm.createContext(c);vm.runInContext(code,c);await els.zmBuild.onclick();
 assert.equal(body.win,4);assert.deepEqual(body.lo,pos===0?[190,190,0,0]:[60,60,190,190]);assert.deepEqual(body.hi,pos===0?[191,191,255,255]:[61,61,191,191]);
 if(reject){assert.match(message,/failed.*rejected/);assert.equal(c.zm.mask,undefined);}else{assert.match(message,/mask built from 4/);assert.equal(c.zm.mask.win,4);}
}
// TRLC-LINKS: REQ-SDS-014
async function fullRecord(n=131072,tol=65536) {
 const lo=60.25, hi=190.75;
 let seq=0,body,message;
 const sig=Array.from({length:n},(_,i)=>i%400<100?hi:lo);
 const els={zmBuild:{},zmN:{value:4},zmTolT:{value:tol},zmTolV:{value:2},zmCh:{value:0}};
 const c={...require('./wsmath.js'),st:{win_cols:n,trig_pos_frac:.5},zm:{},
 // TRLC-LINKS: REQ-SDS-014
 $:id=>els[id],
 // TRLC-LINKS: REQ-SDS-014
 zmCplOK:()=>true,
 // TRLC-LINKS: REQ-SDS-014
 zmVctx:()=>null,
 // TRLC-LINKS: REQ-SDS-014
 redraw:()=>{},
 // TRLC-LINKS: REQ-SDS-014
 zmStatus:t=>message=t,
 // TRLC-LINKS: REQ-SDS-014
 btoa:s=>Buffer.from(s,'binary').toString('base64'),
 // TRLC-LINKS: REQ-SDS-014
 decodeBinFrame:f=>f,
 // TRLC-LINKS: REQ-SDS-014
 fetch:async(url,opts)=>{
  // TRLC-LINKS: REQ-SDS-014
  if(url==='/api/mask'){body=JSON.parse(opts.body);body.lo=Array.from(Buffer.from(body.lo_b64,'base64'));body.hi=Array.from(Buffer.from(body.hi_b64,'base64'));return {ok:true,json:async()=>({ok:true})};}
  // TRLC-LINKS: REQ-SDS-014
  return {arrayBuffer:async()=>({seq:++seq,edge_x:n/2,sample_s:2e-9,tdiv_s:.0002,cols:n,win_cols:n,c1:sig})};
 }};
 vm.createContext(c);vm.runInContext(code,c);const start=Date.now();await els.zmBuild.onclick();
 assert.match(message,/mask built from 4/);assert.equal(body.win,n);
 for(const j of [0,1,92,99,100,107,108,392,399,400,n-1].filter(j=>j<n)){
  if(tol<100){const v=sig.slice(Math.max(0,j-tol),Math.min(n,j+tol+1));assert.equal(body.lo[j],Math.floor(Math.min(...v)-2));assert.equal(body.hi[j],Math.ceil(Math.max(...v)+2));}
  else {assert.equal(body.lo[j],58);assert.equal(body.hi[j],193);}
 }
 assert.ok(Date.now()-start<5000,'large-window dilation must stay responsive even at wide tolerance');
}
// TRLC-LINKS: REQ-SDS-014
async function geometryTransition() {
 let seq=0,body,message;
 const els={zmBuild:{},zmN:{value:4},zmTolT:{value:0},zmTolV:{value:0},zmCh:{value:0}};
 const c={...require('./wsmath.js'),st:{win_cols:4,trig_pos_frac:.5},zm:{},
 // TRLC-LINKS: REQ-SDS-014
 $:id=>els[id],
 // TRLC-LINKS: REQ-SDS-014
 zmCplOK:()=>true,
 // TRLC-LINKS: REQ-SDS-014
 zmVctx:()=>null,
 // TRLC-LINKS: REQ-SDS-014
 redraw:()=>{},
 // TRLC-LINKS: REQ-SDS-014
 zmStatus:t=>message=t,
 // TRLC-LINKS: REQ-SDS-014
 btoa:s=>Buffer.from(s,'binary').toString('base64'),
 // TRLC-LINKS: REQ-SDS-014
 decodeBinFrame:f=>f,
 // TRLC-LINKS: REQ-SDS-014
 fetch:async(url,opts)=>{
  // TRLC-LINKS: REQ-SDS-014
  if(url==='/api/mask'){body=JSON.parse(opts.body);return {ok:true,json:async()=>({ok:true})};}
  ++seq;const n=seq<=2?4:6;
  // TRLC-LINKS: REQ-SDS-014
  return {arrayBuffer:async()=>({seq,edge_x:n/2,sample_s:1e-6,tdiv_s:n*.001,cols:n,win_cols:n,c1:Array(n).fill(n===4?60.25:200.25)})};
 }};
 vm.createContext(c);vm.runInContext(code,c);await els.zmBuild.onclick();
 assert.equal(seq,6,'only fresh geometry contributes to the requested frame count');
 assert.equal(body.tdiv_s,.006);assert.equal(body.sample_s,1e-6);assert.equal(body.peak_detect,false);assert.equal(body.win,6);assert.deepEqual([...Buffer.from(body.lo_b64,'base64')],Array(6).fill(200));assert.deepEqual([...Buffer.from(body.hi_b64,'base64')],Array(6).fill(201));assert.match(message,/mask built from 4/);
}
// A dropped frame transfer (a full-rate record under a lease is megabytes) costs
// a try, not the build.
// TRLC-LINKS: REQ-SDS-014
async function droppedTransfer() {
 let seq=0,body,message,calls=0;
 const els={zmBuild:{},zmN:{value:4},zmTolT:{value:0},zmTolV:{value:0},zmCh:{value:0}};
 const c={...require('./wsmath.js'),st:{win_cols:4,trig_pos_frac:.5},zm:{},
 // TRLC-LINKS: REQ-SDS-014
 $:id=>els[id],
 // TRLC-LINKS: REQ-SDS-014
 zmCplOK:()=>true,
 // TRLC-LINKS: REQ-SDS-014
 zmVctx:()=>null,
 // TRLC-LINKS: REQ-SDS-014
 redraw:()=>{},
 // TRLC-LINKS: REQ-SDS-014
 zmStatus:t=>message=t,setTimeout,
 // TRLC-LINKS: REQ-SDS-014
 btoa:s=>Buffer.from(s,'binary').toString('base64'),
 // TRLC-LINKS: REQ-SDS-014
 decodeBinFrame:f=>f,
 // TRLC-LINKS: REQ-SDS-014
 fetch:async(url,opts)=>{
  // TRLC-LINKS: REQ-SDS-014
  if(url==='/api/mask'){body=JSON.parse(opts.body);return {ok:true,json:async()=>({ok:true})};}
  if(++calls===2)throw new TypeError('network error');
  // TRLC-LINKS: REQ-SDS-014
  return {arrayBuffer:async()=>({seq:++seq,edge_x:2,sample_s:1e-6,tdiv_s:1e-3,cols:4,win_cols:4,c1:[60,60,190,190]})};
 }};
 vm.createContext(c);vm.runInContext(code,c);await els.zmBuild.onclick();
 assert.match(message,/mask built from 4/);assert.equal(body.win,4);
}
(async()=>{await run(false);await run(true);await run(false,0);await geometryTransition();await droppedTransfer();await fullRecord(500,7);await fullRecord();console.log('ALL PASS')})().catch(e=>{console.error(e);process.exit(1)});

// Deep records retain the mask at its trigger-relative times, rather than
// hiding it or stretching its four samples across the eight-sample record.
{
 const source = fs.readFileSync('app_zonemask.js','utf8');
 const overlay = source.slice(source.indexOf('function drawZones('), source.indexOf('// Zone/mask test RAW'));
 const points=[];
 const c={view:{mode:'YT',win:{a:0,b:1}},frame:{cols:8,col_span_s:8,edge_frac:.5,tdiv_s:1,win_frac:.5,win_cols:4},st:{},CW:14,
 // TRLC-LINKS: REQ-SDS-014
 zm:{zones:[],mask:{lo:[60,70,80,90],hi:[160,170,180,190],win:4,posFrac:.5,geometry:{tdiv_s:1,sample_s:1}}},yFor:v=>v};
 const g={
  // TRLC-LINKS: REQ-SDS-014
  beginPath(){},
  // TRLC-LINKS: REQ-SDS-014
  moveTo:(x,y)=>points.push([x,y]),
  // TRLC-LINKS: REQ-SDS-014
  lineTo:(x,y)=>points.push([x,y]),
  // TRLC-LINKS: REQ-SDS-014
  stroke(){}};
 vm.createContext(c);vm.runInContext(overlay,c);c.drawZones(g);
 assert.deepEqual(points,[[4,60],[6,70],[8,80],[10,90],[4,160],[6,170],[8,180],[10,190]]);
 points.length=0;c.frame.tdiv_s=2;c.drawZones(g);assert.equal(points.length,0,'changed acquisition timebase must not display a stale mask');
}
