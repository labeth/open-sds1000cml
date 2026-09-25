// Compare unfiltered captures when qualifying ADC input sampling timing.
// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
import {mkdirSync, writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const out=process.argv[2];if(!out)throw Error('evidence directory required');mkdirSync(out,{recursive:true});
const base=process.env.SCOPE_URL || 'http://192.168.1.209:8080';
const reports:any[]=[];let last=-1;const deadline=Date.now()+120000;
while(reports.length<80){
 if(Date.now()>deadline)throw Error('timed out waiting for 80 distinct captures');
 const r=await fetch(base+'/api/frame.bin?cols=2048&full=1&raw=1',{signal:AbortSignal.timeout(10000)});if(!r.ok)throw Error(`HTTP ${r.status}`);
 const buf=Buffer.from(await r.arrayBuffer()),f=decodeBinFrame(buf.buffer.slice(buf.byteOffset,buf.byteOffset+buf.byteLength));
 if(f.seq===last){await new Promise(r=>setTimeout(r,100));continue;}last=f.seq;
 writeFileSync(`${out}/frame-${reports.length}.bin`,buf);
 const channels=[f.c1,f.c2].map((x:Uint8Array)=>{const a=Array.from(x),spikes:any[]=[];
  for(let i=5;i<a.length-5;i++){const expected=(a[i-5]+a[i+5])/2,residual=a[i]-expected;if(Math.abs(residual)>45)spikes.push({i,value:a[i],expected,residual,phase:i%5});}
  return {samples:a.length,min:Math.min(...a),max:Math.max(...a),means:Array.from({length:5},(_,p)=>{let sum=0,k=0;for(let i=p;i<a.length;i+=5){sum+=a[i];k++;}return sum/k}),spikes};
 });reports.push({seq:f.seq,sample_s:f.sample_s,channels});
}
writeFileSync(`${out}/analysis.json`,JSON.stringify(reports,null,2));
console.log(JSON.stringify({frames:reports.length,channels:[0,1].map(ch=>({samples:reports.reduce((n,f)=>n+f.channels[ch].samples,0),positiveSpikes:reports.reduce((n,f)=>n+f.channels[ch].spikes.filter((s:any)=>s.residual>45).length,0)}))}));
