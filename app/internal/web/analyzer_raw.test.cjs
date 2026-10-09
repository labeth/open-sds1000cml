// ENGMODEL-OWNER-UNIT: FU-APP-WEB
// TRLC-LINKS: REQ-SDS-019, REQ-SDS-067
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
(async()=>{
for(const [file,key,loop,end] of [['app_eye.js','ej','ejLoop','// TRLC-LINKS: REQ-SDS-200\n$("ejEye")'],['app_superres.js','sr','srLoop','// TRLC-LINKS: REQ-SDS-019\n$("srArm")']]){
 const src=fs.readFileSync(file,'utf8'),start=src.indexOf('async function '+loop+'('),code=src.slice(start,src.indexOf(end,start));
 let sample={seq:1,peak_detect:true},ingested=0,stopped=0,queued=0;
 const state={armed:true,gen:1,lastSeq:0,fails:0};
 const c={st:{running:true},[key]:state,srFails:0,fetch:async()=>({ok:true,arrayBuffer:async()=>null}),decodeBinFrame:()=>sample,[key+'Ingest'](){ingested++;},[key+'Stop'](){stopped++;state.armed=false;},[key+'Status'](){},setTimeout(){queued++;},Date,Math};
 vm.createContext(c);vm.runInContext(code,c);
 await c[loop](1);assert.equal(ingested,0);assert.equal(stopped,0);assert.equal(queued,1,'keep requesting raw data during transition');
 sample={seq:2,c1:[1,2,3]};await c[loop](1);assert.equal(ingested,1);assert.equal(state.rawWaitAt,0);
 c.st.running=false;sample={seq:3,is_env:true};await c[loop](1);assert.equal(stopped,1,'stopped envelope cannot become a sampled capture');
}
console.log('ALL PASS');
})().catch(e=>{console.error(e);process.exit(1)});
