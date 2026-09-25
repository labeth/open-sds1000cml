// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Compare converter crossing times on both edge polarities. Diagnostic only.
import {readFileSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const [file,calfile,out,timingfile]=process.argv.slice(2);if(!out)throw Error('capture calibration output required');
const b=readFileSync(file),f=decodeBinFrame(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength)),c=JSON.parse(readFileSync(calfile,'utf8')),channels=[f.c1,f.c2];
if(f.fraction_bits!==8)throw Error('calibration not active');
const scores=Array.from({length:5},(_,r)=>{let error=0;for(let ch=0;ch<2;ch++){
 let input=Array.from(channels[ch].slice(0,2020) as number[]);
 if(f.filter?.includes('aperture compensation')){
  if(!c.timing_ns)throw Error('timing calibration required');
  const observed=input.slice();
  for(let pass=0;pass<10;pass++){const prev=input;input=observed.slice();for(let i=2;i<input.length-2;i++)input[i]-=c.timing_ns[ch][(i+r)%5]/(f.sample_s*1e9)*(prev[i-2]-8*prev[i-1]+8*prev[i+1]-prev[i+2])/12;}
 }
 for(let i=10;i<2010;i++){const p=(i+r)%5,raw=(input[i]-128)*c.gain[ch][p]+128+c.gain_offset[ch][p]+c.offset[ch][p];error+=(raw-Math.round(raw))**2;}}
 return {r,error:error/4000};}).sort((a,b)=>a.error-b.error);if(scores[0].error>1e-5)throw Error('converter phase unknown');const rotation=scores[0].r;
if(timingfile){
 const timing=JSON.parse(readFileSync(timingfile,'utf8')).timing_ns;
 for(let ch=0;ch<2;ch++){const old=channels[ch],x=Float32Array.from(old);for(let i=2;i<old.length-2;i++)x[i]+=timing[ch][(i+rotation)%5]/(f.sample_s*1e9)*(old[i-2]-8*old[i-1]+8*old[i+1]-old[i+2])/12;channels[ch]=x;}
}
const reports=channels.map((x:any)=>{
 const sorted=Array.from(x.slice(0,50000) as number[]).sort((a,b)=>a-b),low=sorted[sorted.length/5|0],high=sorted[sorted.length*4/5|0],mid=(low+high)/2;
 const edges:{i:number,rising:boolean}[]=[];let state=x[0]>mid;
 for(let i=20;i<x.length-30;i++){if(!state&&x[i]>low+.8*(high-low)){edges.push({i,rising:true});state=true;}if(state&&x[i]<low+.2*(high-low)){edges.push({i,rising:false});state=false;}}
 const gaps=edges.slice(1).map((e,i)=>e.i-edges[i].i).sort((a,b)=>a-b);
 const radius=Math.max(6,Math.min(60,Math.floor(gaps[gaps.length>>1]*.8)));
 const thresholds=[.3,.5,.7].map(fraction=>{
  const threshold=low+fraction*(high-low),groups=[Array.from({length:5},()=>[] as number[]),Array.from({length:5},()=>[] as number[])];let rejected=0;
  for(const edge of edges){const times:number[]=[];for(let p=0;p<5;p++){let found=NaN;for(let i=Math.max(5,edge.i-radius-5);i<Math.min(x.length-6,edge.i+radius);i++){if((i+rotation)%5!==p)continue;const a=x[i],b=x[i+5];if(edge.rising?a<=threshold&&b>threshold:a>=threshold&&b<threshold){found=i+5*(threshold-a)/(b-a);break;}}times.push(found);}
   if(times.some(v=>!Number.isFinite(v))||Math.max(...times)-Math.min(...times)>5){rejected++;continue;}const mean=times.reduce((s,v)=>s+v)/5;for(let p=0;p<5;p++)groups[edge.rising?0:1][p].push((times[p]-mean)*f.sample_s*1e9);
  }
  const stats=groups.map(g=>g.map(v=>{const mean=v.reduce((s,x)=>s+x,0)/v.length;return {meanNs:mean,sdNs:Math.sqrt(v.reduce((s,x)=>s+(x-mean)**2,0)/v.length),edges:v.length};}));
  return {fraction,threshold,rejected,rising:stats[0],falling:stats[1],polarityAverageNs:stats[0].map((v,p)=>(v.meanNs+stats[1][p].meanNs)/2),polarityDifferenceNs:stats[0].map((v,p)=>v.meanNs-stats[1][p].meanNs)};
 });return {low,high,edgeCount:edges.length,thresholds};
});writeFileSync(out,JSON.stringify({rotation,channels:reports,method:'Per-converter linear interpolation of crossings; polarity/threshold disagreement indicates confounding response or voltage errors, not calibrated aperture skew.'},null,2)+'\n');console.log(JSON.stringify(reports.map(ch=>({edgeCount:ch.edgeCount,thresholds:ch.thresholds.map(t=>({fraction:t.fraction,averageNs:t.polarityAverageNs,differenceNs:t.polarityDifferenceNs,rejected:t.rejected}))})),null,2));
