// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Estimate converter gain from an independently fitted known sine capture.
import {readFileSync,writeFileSync,readdirSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const [dir,calPath,out]=process.argv.slice(2);
if(!out)throw Error('usage: capture-directory offset-calibration output-json');
const cal=JSON.parse(readFileSync(calPath,'utf8'));
const fits=JSON.parse(readFileSync(dir+'/phase-diagnosis.json','utf8'));
const bySeq=new Map(fits.map((f:any)=>[f.seq,f]));
const gains=[Array.from({length:5},()=>[] as number[]),Array.from({length:5},()=>[] as number[])];
for(const name of readdirSync(dir).filter(n=>/^frame-\d+\.bin$/.test(n))){
 const b=readFileSync(dir+'/'+name),f=decodeBinFrame(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength)),fit:any=bySeq.get(f.seq);
 if(!fit)continue;if(f.fraction_bits!==8)throw Error('requires offset-calibrated Q8 capture');
 const a=[f.c1,f.c2];
 const scores=Array.from({length:5},(_,r)=>({r,error:a.reduce((s:number,x:any,ch:number)=>s+Array.from(x as number[]).reduce((sum:number,v:number,i:number)=>{const raw=v+cal.offset[ch][(i+r)%5];return sum+(raw-Math.round(raw))**2;},0),0)/(2*a[0].length)})).sort((a,b)=>a.error-b.error);
 if(scores[0].error>1e-5||scores[1].error<1e-4)throw Error('ambiguous converter identity');
 const r=scores[0].r;
 for(let ch=0;ch<2;ch++){const amp=fit.channels[ch].phases.map((p:any)=>p.amplitude),mean=amp.reduce((a:number,b:number)=>a+b)/5;for(let i=0;i<5;i++)gains[ch][(i+r)%5].push(amp[i]/mean);}
}
const stats=gains.map(phases=>phases.map(a=>{if(!a.length)throw Error('no captures');const mean=a.reduce((s,v)=>s+v)/a.length;return {gain:mean,sd:Math.sqrt(a.reduce((s,v)=>s+(v-mean)**2,0)/a.length),frames:a.length};}));
writeFileSync(out,JSON.stringify({stats,candidate:{...cal,gain:stats.map(ch=>ch.map(p=>p.gain))}},null,2)+'\n');console.log(JSON.stringify(stats));
