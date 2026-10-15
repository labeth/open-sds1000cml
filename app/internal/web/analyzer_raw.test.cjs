// ENGMODEL-OWNER-UNIT: FU-APP-WEB
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
(async()=>{
for(const [file,key,loop,end] of [['app_eye.js','ej','ejLoop','// TRLC-LINKS: REQ-SDS-200\n$("ejEye")'],['app_superres.js','sr','srLoop','// TRLC-LINKS: REQ-SDS-019\n$("srArm")']]){
 const src=fs.readFileSync(file,'utf8'),start=src.indexOf('async function '+loop+'('),code=src.slice(start,src.indexOf(end,start));
 let sample={seq:1,peak_detect:true},ingested=0,stopped=0,queued=0;
 const state={armed:true,gen:1,lastSeq:0,fails:0};
 const noBuffer={ok:true,
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  arrayBuffer:async()=>null};
 const c={st:{running:true},[key]:state,srFails:0,ejOptBusy:false,
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  fetch:async()=>noBuffer,
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  decodeBinFrame:()=>sample,
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  [key+'Ingest'](){ingested++;},
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  [key+'Stop'](){stopped++;state.armed=false;},
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  [key+'Status'](){},
  // TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
  setTimeout(){queued++;},
  Date,Math};
 vm.createContext(c);vm.runInContext(code,c);
 await c[loop](1);assert.equal(ingested,0);assert.equal(stopped,0);assert.equal(queued,1,'keep requesting raw data during transition');
 sample={seq:2,c1:[1,2,3],full_rate:true};await c[loop](1);assert.equal(ingested,1);if(key==='sr')assert.equal(state.rawWaitAt,0);
 c.st.running=false;sample={seq:3,is_env:true};await c[loop](1);
 if(key==='sr')assert.equal(stopped,1,'stopped envelope cannot become a sampled capture');
 else{assert.equal(stopped,0,'the eye holds what it folded');assert.equal(ingested,1,'an envelope never folds into the eye');}
}
console.log('ALL PASS');
})().catch(e=>{console.error(e);process.exit(1)});
