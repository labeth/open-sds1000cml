// ENGMODEL-OWNER-UNIT: FU-WEB-APP-GL
// TRLC-LINKS: REQ-SDS-071
const fs = require('node:fs'), vm = require('node:vm'), assert = require('node:assert/strict');
const path = require('node:path'), base = process.argv[2], cases = [];
const check = (name, fn) => { fn(); cases.push({name, result:'pass'}); };
const calls = [], listeners = {}; let lost = false, scheduled = 0, serial = 0;
const gl = new Proxy({}, {get(target, key) {
  if (key in target) return target[key];
  if (key === 'isContextLost') return () => lost;
  if (key === 'getShaderParameter' || key === 'getProgramParameter') return () => true;
  if (key.startsWith('create')) return () => ({id: ++serial});
  if (key === 'getAttribLocation') return () => 0;
  if (key === 'getUniformLocation') return () => ({});
  if (key.toUpperCase() === key) return key;
  return (...args) => calls.push([key, ...args.map(x => x instanceof Float32Array ? [...x] : x)]);
}});
const canvas = {width:100,height:80,getContext:()=>gl,addEventListener:(name,fn)=>listeners[name]=fn};
const c = vm.createContext({Float32Array,Uint8Array,console,
 document:{createElement:()=>({getContext:()=>({clearRect(){},fillRect(){},fillText(){},getImageData:()=>({data:new Uint8Array(128*84*4)})})})},
 scheduleRender:()=>scheduled++});
vm.runInContext(fs.readFileSync(path.join(base,'app_gl.js'),'utf8'),c);
const r=c.glRenderer(canvas), facade=c.glContext2D(r,canvas);
check('two rectangles batch as twelve triangle vertices',()=>{r.begin('#000');calls.length=0;r.fillRect(1,2,3,4,'#fff');r.fillRect(10,20,3,4,'#fff');r.end();assert.deepEqual(calls.filter(x=>x[0]==='drawArrays'),[['drawArrays','TRIANGLES',0,12]]);});
check('translation is applied to uploaded vertex positions',()=>{r.begin();r.translate(5,7);calls.length=0;r.fillRect(1,2,3,4,'#fff');r.end();const b=calls.find(x=>x[0]==='bufferData')[2];assert.deepEqual(b.slice(0,2),[6,9]);});
check('scissor changes flush pending geometry and flip the Y origin',()=>{r.begin();calls.length=0;r.fillRect(0,0,5,5,'#fff');r.clip(2,3,10,20);assert(calls.findIndex(x=>x[0]==='drawArrays')<calls.findIndex(x=>x[0]==='scissor'));assert.deepEqual(calls.find(x=>x[0]==='scissor'),['scissor',2,57,10,20]);});
check('texture changes preserve solid-image-solid paint order',()=>{r.begin();calls.length=0;r.fillRect(0,0,1,1,'#fff');r.image({id:'external'},0,0,1,1);r.fillRect(0,0,1,1,'#fff');r.end();assert.deepEqual(calls.filter(x=>x[0]==='bufferData').map(x=>x[2][8]),[0,1,0]);});
check('lost context discards queued geometry; restored context schedules redraw',()=>{r.begin();calls.length=0;r.fillRect(0,0,1,1,'#fff');lost=true;r.end();assert.equal(calls.filter(x=>x[0]==='drawArrays').length,0);let prevented=false;listeners.webglcontextlost({preventDefault(){prevented=true;}});assert(prevented);lost=false;listeners.webglcontextrestored();assert.equal(scheduled,1);});
check('non-ASCII glyph uses question-mark atlas coordinates',()=>{r.begin();calls.length=0;r.text('µ',0,0,'#fff',14);r.end();const a=calls.find(x=>x[0]==='bufferData')[2];r.begin();calls.length=0;r.text('?',0,0,'#fff',14);r.end();assert.deepEqual(calls.find(x=>x[0]==='bufferData')[2],a);});
check('facade save/restore retains alpha, style and dash values',()=>{facade.fillStyle='#123';facade.globalAlpha=.4;facade.setLineDash([2,3]);facade.save();facade.fillStyle='#abc';facade.globalAlpha=1;facade.setLineDash([8,9]);facade.restore();assert.equal(facade.fillStyle,'#123');assert.equal(facade.globalAlpha,.4);assert.deepEqual([...facade.getLineDash()],[2,3]);});
check('convex quad fan emits two triangles',()=>{const out=[];c.glFillFan({triangle:(...v)=>out.push(v)},[0,0,2,0,2,2,0,2],'#fff');assert.equal(out.length,2);});
// TRLC-LINKS: REQ-SDS-202
const g=vm.createContext({view:{win:{a:.2,b:.6},vwin:{a:0,b:1},fwin:{a:0,b:1},mode:'YT'},CW:401,CH:200,NH:80,DIVX:10,MINSPAN:.001,frame:null,st:{running:true,trig_pos_frac:.5},srGate:{},scheduleRender(){},clearPersist(){},redraw(){},scope:{getBoundingClientRect:()=>({left:0,top:0,width:400,height:200})},nav:{getBoundingClientRect:()=>({left:10,width:100})}});
vm.runInContext(fs.readFileSync(path.join(base,'app_geom.js'),'utf8'),g);
check('column and viewport fraction mapping are inverse across zoom',()=>{for(const i of [0,20,50,100])assert(Math.abs(g.fracForX(g.xForCol(i,101))-i/100)<1e-12);});
check('live deep home window preserves edge location beyond record boundary',()=>{const w=g.homeWindow({tdiv_s:1,col_span_s:100,win_frac:.1,edge_frac:.01});assert(Math.abs(w.a+.04)<1e-12);assert(Math.abs(w.b-.06)<1e-12);});
check('stopped deep home window clamps to the record',()=>{g.st.running=false;const w=g.homeWindow({tdiv_s:1,col_span_s:100,win_frac:.1,edge_frac:.01});assert.equal(w.a,0);assert(Math.abs(w.b-.1)<1e-12);g.st.running=true;});
check('envelope home window uses full record',()=>{const w=g.homeWindow({is_env:true,edge_frac:.1,win_frac:.1});assert.equal(w.a,0);assert.equal(w.b,1);});
check('gate coordinates follow the current edge anchor',()=>{g.frame={edge_frac:.2};assert(Math.abs(g.srGateRF(.1)-.3)<1e-12);g.frame.edge_frac=.4;assert.equal(g.srGateRF(.1),.5);});
check('navigator pointer fraction clamps outside either edge',()=>{assert.equal(g.navFrac({clientX:0}),0);assert.equal(g.navFrac({clientX:200}),1);});
check('acquisition signature ignores channel-two-only length changes',()=>{g.st.running=true;assert.equal(g.acqSig({c2:[1]}),g.acqSig({c2:[1,2,3]}));});
console.log(JSON.stringify({result:'pass',cases,scope:'Original renderer command construction with stub WebGL and raster atlas; original geometry functions in a VM. No shader execution, GPU rasterization, real DOM events or device commands.'},null,2));
