// ENGMODEL-OWNER-UNIT: FU-APP-ENGINE
// AU source: A2 (blue)/C1=SCL, A11 (yellow)/C2=SDA; 0x24 W, 55 AA 0F F0.
import{mkdirSync,writeFileSync}from'node:fs';
import{createRequire}from'node:module';
const{decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const base=process.env.SCOPE_URL||'http://192.168.1.209:8080',out=process.argv[2];
if(!out)throw Error('output directory required');mkdirSync(out,{recursive:true});
const pause=(ms:number)=>new Promise(r=>setTimeout(r,ms));
const status=async()=>await(await fetch(base+'/api/status',{signal:AbortSignal.timeout(5000)})).json();
const post=async(path:string,body:unknown)=>{const r=await fetch(base+path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),signal:AbortSignal.timeout(5000)});const j=await r.json();if(!r.ok||!j.ok)throw Error(JSON.stringify(j));};
const set=(control:string,value:number)=>post('/api/set',{control,value});
const initial=await status(),results:any[]=[];
try{
 await set('serialmode',0);await set('norm',0);await set('tdiv',.0005);await set('run',1);await pause(1500);
 const f=decodeBinFrame(await(await fetch(base+'/api/frame.bin?cols=4096&raw=1',{signal:AbortSignal.timeout(10000)})).arrayBuffer());
 for(const [channel,a]of[f.c1,f.c2].entries()){
  let lo=255,hi=0;for(const x of a)if(x>=0){lo=Math.min(lo,x);hi=Math.max(hi,x);}
  results.push({name:'input',channel:channel+1,lo,hi});
  if(hi-lo<20)throw Error(`C${channel+1} lacks a usable logic signal; check its connection before testing I2C`);
 }
 for(const test of[
  {name:'match',addr:0x24,rw:0,bytes:[0x55,0xaa],match:true},
  {name:'wrong-address',addr:0x25,rw:0,bytes:[0x55,0xaa],match:false},
  {name:'wrong-direction',addr:0x24,rw:1,bytes:[0x55,0xaa],match:false},
  {name:'wrong-data',addr:0x24,rw:0,bytes:[0xde,0xad],match:false},
  {name:'resume',addr:0x24,rw:0,bytes:[0x55,0xaa],match:true},
 ]){
  await post('/api/serial',{proto:2,chA:0,chB:1,inverted:true,haveThr:true,threshold:103,addr:test.addr,rw:test.rw,bytes:test.bytes});
  await set('serialmode',1);await set('norm',1);await pause(1500);
  const a=await status();if(a.serial_backend!=='hardware-i2c')throw Error('hardware I2C was not selected');
  let z=a;const deadline=Date.now()+(test.match?15000:4000);
  while(Date.now()<deadline){await pause(200);z=await status();if(test.match&&z.seq>=a.seq+10)break;}
  if(z.bus_errors!==a.bus_errors || z.wedged || (test.match ? z.seq<a.seq+10 : z.seq!==a.seq))throw Error(`failed ${test.name}`);
  results.push({name:test.name,before:a,after:z});console.log(JSON.stringify({name:test.name,publications:z.seq-a.seq}));
 }
}catch(error){results.push({error:String(error)});throw error;}
finally{
 writeFileSync(out+'/i2c-hardware-validation.json',JSON.stringify({initial,results},null,2));
 await set('serialmode',0);await set('norm',initial.norm?1:0);await set('tdiv',initial.tdiv_s);await set('run',initial.running?1:0);
}
