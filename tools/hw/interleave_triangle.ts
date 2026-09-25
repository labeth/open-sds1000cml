// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Estimate static converter gain/offset on straight triangle ramps.
import {readFileSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const [file,calfile,out]=process.argv.slice(2);if(!out)throw Error('capture calibration output required');
const b=readFileSync(file),f=decodeBinFrame(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength)),cal=JSON.parse(readFileSync(calfile,'utf8'));
if(f.fraction_bits!==8||f.sample_s!==2e-9)throw Error('expected offset-calibrated raw-rate record');
const a=[f.c1,f.c2];
const scores=Array.from({length:5},(_,r)=>({r,error:a.reduce((s:number,x:any,ch:number)=>{for(let i=0;i<2000;i++){const raw=x[i]+cal.offset[ch][(i+r)%5];s+=(raw-Math.round(raw))**2;}return s;},0)/4000})).sort((a,b)=>a.error-b.error);
if(scores[0].error>1e-5||scores[1].error<1e-4)throw Error('ambiguous converter phase');
const rotation=scores[0].r;
function solve(m:number[][]){const a=m.map(r=>r.slice());for(let k=0;k<3;k++){const d=a[k][k];if(Math.abs(d)<1e-12)throw Error('singular fit');for(let c=k;c<4;c++)a[k][c]/=d;for(let r=0;r<3;r++)if(r!==k){const v=a[r][k];for(let c=k;c<4;c++)a[r][c]-=v*a[k][c];}}return a.map(r=>r[3]);}
const channels=a.map((x:any)=>{
 const points=Array.from({length:5},()=>[] as number[][]);let blocks=0;
 for(let start=0;start+100<x.length;start+=100){
  let mean=0,slope=0;for(let j=0;j<100;j++){mean+=x[start+j]/100;slope+=(j-49.5)*x[start+j]/83325;}
  let lo=0,hi=0;for(let j=0;j<25;j++){lo+=(j-12)*x[start+j]/1300;hi+=(j-12)*x[start+75+j]/1300;}
  if(Math.abs(slope)<.02||Math.abs(lo-hi)>.012||Math.abs(lo-slope)>.01||Math.abs(hi-slope)>.01)continue;
  blocks++;for(let j=0;j<100;j++){const ref=mean+slope*(j-49.5),res=x[start+j]-ref;points[(start+j+rotation)%5].push([ref-128,slope,res]);}
 }
 const phases=points.map(pts=>{const m=Array.from({length:3},()=>[0,0,0,0]);for(const p of pts){const row=[1,p[0],p[1]];for(let r=0;r<3;r++){for(let c=0;c<3;c++)m[r][c]+=row[r]*row[c];m[r][3]+=row[r]*p[2];}}const [offset,gainError,timingSamples]=solve(m);return {offset,gain:1+gainError,timingNs:timingSamples*f.sample_s*1e9,samples:pts.length,rms:Math.sqrt(pts.reduce((s,p)=>s+(p[2]-offset-gainError*p[0]-timingSamples*p[1])**2,0)/pts.length)};});
 return {blocks,phases};
});
writeFileSync(out,JSON.stringify({rotation,channels,method:'100-sample straight-ramp blocks, per-converter residual regression against level and slope; exploratory fit, not deployed'},null,2)+'\n');console.log(JSON.stringify(channels,null,2));
