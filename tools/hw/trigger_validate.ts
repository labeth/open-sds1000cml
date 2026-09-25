// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Validate actual captured slope through the physical panel dispatcher.
import {writeFileSync, mkdirSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const out=process.argv[2];if(!out)throw Error('output directory required');mkdirSync(out,{recursive:true});
const captures=Number(process.env.TRIGGER_CAPTURES || 300);
if(!Number.isInteger(captures) || captures<1 || captures>10000)throw Error('invalid trigger capture count');
const status=async()=>await(await fetch(base+'/api/status')).json();
const press=async(button:string)=>{await fetch(base+'/api/panel',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({button})});await new Promise(r=>setTimeout(r,300));};
const initial=await status(),results:any[]=[];
try {
 for(const channel of [1,0])for(const rising of [true,false]){
  await press(channel===0?'ch1vdivpush':'ch2vdivpush');if((await status()).trig_rising!==rising)await press('triglvlpush');
  const rows:any[]=[];let seq=(await status()).seq;const deadline=Date.now()+120000;
  while(rows.length<captures){
   if(Date.now()>deadline)throw Error('capture deadline exceeded');
   const reply=await fetch(base+'/api/frame.bin?cols=2048&full=1&raw=1',{signal:AbortSignal.timeout(10000)});if(!reply.ok)throw Error(`HTTP ${reply.status}`);
   const f=decodeBinFrame(await reply.arrayBuffer());if(f.seq===seq){await new Promise(r=>setTimeout(r,20));continue;}seq=f.seq;
   const a=channel===0?f.c1:f.c2,i=Math.round(f.edge_x);if(!f.trigd||i<15||i+15>=a.length)continue;
   let before=0,after=0;for(let j=5;j<15;j++){before+=a[i-j];after+=a[i+j];}
   const delta=(after-before)/10;rows.push({seq,edge:i,delta,correct:rising?delta>0:delta<0});
  }
  const result={channel:channel+1,rising,captures:rows.length,wrongSlope:rows.filter(r=>!r.correct).length,rows};results.push(result);
  writeFileSync(`${out}/results.json`,JSON.stringify(results,null,2));console.log(JSON.stringify({channel:result.channel,rising,captures:result.captures,wrongSlope:result.wrongSlope}));
 }
}finally{
 await press(initial.trig_source===0?'ch1vdivpush':'ch2vdivpush');if((await status()).trig_rising!==initial.trig_rising)await press('triglvlpush');
}
if(results.some(r=>r.wrongSlope>0))process.exitCode=1;
