// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Fit each converter independently to a known bench sine; report offset,
// gain, aperture mismatch and bit-order alternatives without changing samples.
import {readFileSync,writeFileSync,readdirSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const dir=process.argv[2],freq=Number(process.argv[3]||5e6);
if(!dir)throw Error('capture directory required');
function fit(a:number[],idx:number[],dt:number){
 const m=Array.from({length:3},()=>[0,0,0,0]);
 for(let j=0;j<a.length;j++){const t=2*Math.PI*freq*dt*idx[j],x=[1,Math.sin(t),Math.cos(t)];for(let r=0;r<3;r++){for(let c=0;c<3;c++)m[r][c]+=x[r]*x[c];m[r][3]+=x[r]*a[j];}}
 for(let k=0;k<3;k++){const d=m[k][k];for(let c=k;c<4;c++)m[k][c]/=d;for(let r=0;r<3;r++)if(r!==k){const v=m[r][k];for(let c=k;c<4;c++)m[r][c]-=v*m[k][c];}}
 const [dc,s,c]=m.map(r=>r[3]);let ss=0;for(let j=0;j<a.length;j++){const t=2*Math.PI*freq*dt*idx[j];ss+=(a[j]-dc-s*Math.sin(t)-c*Math.cos(t))**2;}
 return {dc,amplitude:Math.hypot(s,c),phase:Math.atan2(c,s),rms:Math.sqrt(ss/a.length)};
}
const result=[];
for(const name of readdirSync(dir).filter(s=>/^frame-\d+\.bin$/.test(s)).slice(0,20)){
 const b=readFileSync(dir+'/'+name),f=decodeBinFrame(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength));
 const channels=[f.c1,f.c2].map((arr:any)=>{
  const a=Array.from(arr) as number[],phases=Array.from({length:5},(_,p)=>{const idx=[];for(let i=p;i<a.length;i+=5)idx.push(i);return fit(idx.map(i=>a[i]),idx,f.sample_s);});
  const ref=phases[0].phase,relative=phases.map(p=>Math.atan2(Math.sin(p.phase-ref),Math.cos(p.phase-ref))/(2*Math.PI*freq)*1e9),mean=relative.reduce((s,v)=>s+v)/5;
  const tests=[];for(let bit=0;bit<8;bit++)for(let other=bit+1;other<8;other++){let ss=0;for(let p=0;p<5;p++){const idx=[];for(let i=p;i<a.length;i+=5)idx.push(i);const x=idx.map(i=>{const v=a[i];return v^((((v>>bit)^(v>>other))&1)*((1<<bit)|(1<<other)));});ss+=fit(x,idx,f.sample_s).rms**2/5;}tests.push({bits:[bit,other],rms:Math.sqrt(ss)});}
  return {wholeChannelRMS:fit(a,a.map((_,i)=>i),f.sample_s).rms,phases:phases.map((p,i)=>({...p,relativeTimingNs:relative[i]-mean})),identityRMS:Math.sqrt(phases.reduce((s,p)=>s+p.rms**2,0)/5),bestBitSwap:tests.sort((a,b)=>a.rms-b.rms)[0]};
 });result.push({seq:f.seq,channels});
}
writeFileSync(dir+'/phase-diagnosis.json',JSON.stringify(result,null,2));
console.log(JSON.stringify({frames:result.length,channels:[0,1].map(ch=>({wholeChannelRMS:result.reduce((s,r)=>s+r.channels[ch].wholeChannelRMS,0)/result.length,residualRMS:result.reduce((s,r)=>s+r.channels[ch].identityRMS,0)/result.length,maxTimingSpreadNs:Math.max(...result.map(r=>{const x=r.channels[ch].phases.map(p=>p.relativeTimingNs);return Math.max(...x)-Math.min(...x)})),minBitSwapRMS:Math.min(...result.map(r=>r.channels[ch].bestBitSwap.rms)),example:result[0].channels[ch]}))},null,2));
