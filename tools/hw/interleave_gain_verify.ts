// ENGMODEL-OWNER-UNIT: FU-APP-PANEL
// Compare deployed gain correction with offset-only on identical samples.
import {readFileSync,readdirSync,writeFileSync} from 'node:fs';
import {createRequire} from 'node:module';
const {decodeBinFrame}=createRequire(import.meta.url)('../../app/internal/web/binframe.js');
const [dir,calfile,out,freqArg,timingfile]=process.argv.slice(2),freq=Number(freqArg||5e6);
if(!out)throw Error('capture directory, calibration, output required');
const c=JSON.parse(readFileSync(calfile,'utf8'));
const timing=timingfile?JSON.parse(readFileSync(timingfile,'utf8')).timing_ns:null;
function fit(a:number[],dt:number){const m=Array.from({length:3},()=>[0,0,0,0]);for(let i=0;i<a.length;i++){const t=2*Math.PI*freq*dt*i,x=[1,Math.sin(t),Math.cos(t)];for(let r=0;r<3;r++){for(let k=0;k<3;k++)m[r][k]+=x[r]*x[k];m[r][3]+=x[r]*a[i];}}for(let k=0;k<3;k++){const d=m[k][k];for(let j=k;j<4;j++)m[k][j]/=d;for(let r=0;r<3;r++)if(r!==k){const d=m[r][k];for(let j=k;j<4;j++)m[r][j]-=d*m[k][j];}}const [dc,s,co]=m.map(r=>r[3]);let ss=0;for(let i=0;i<a.length;i++){const t=2*Math.PI*freq*dt*i;ss+=(a[i]-dc-s*Math.sin(t)-co*Math.cos(t))**2;}return Math.sqrt(ss/a.length);}
const rows=[];
for(const name of readdirSync(dir).filter(n=>/^frame-\d+\.bin$/.test(n))){
 const b=readFileSync(dir+'/'+name),f=decodeBinFrame(b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength));if(f.fraction_bits!==8)throw Error('Q8 calibration missing');const a=[f.c1,f.c2];
 const raw=(v:number,ch:number,p:number)=>(v-128)*c.gain[ch][p]+128+c.gain_offset[ch][p]+c.offset[ch][p];
 const scores=Array.from({length:5},(_,r)=>{let err=0;for(let ch=0;ch<2;ch++)for(let i=0;i<Math.min(2000,a[ch].length);i++){const x=raw(a[ch][i],ch,(i+r)%5);err+=(x-Math.round(x))**2;}return {r,error:err/4000};}).sort((a,b)=>a.error-b.error);
 if(scores[0].error>1e-5||scores[1].error<1e-4)throw Error('gain correction identity ambiguous');const r=scores[0].r;
 rows.push({seq:f.seq,channels:a.map((a:any,ch:number)=>{const old=Array.from(a as number[],(v,i)=>{const p=(i+r)%5;return Math.round((Math.round(raw(v,ch,p))-c.offset[ch][p])*256)/256;});const adjusted=Array.from(a as number[]);if(timing)for(let i=2;i<a.length-2;i++)adjusted[i]+=timing[ch][(i+r)%5]/(f.sample_s*1e9)*(a[i-2]-8*a[i-1]+8*a[i+1]-a[i+2])/12;return {offsetOnlyRMS:fit(old,f.sample_s),gainCorrectedRMS:fit(Array.from(a),f.sample_s),timingRMS:fit(adjusted,f.sample_s)};})});
}
const summary={frames:rows.length,channels:[0,1].map(ch=>({timingRMS:rows.reduce((s,r)=>s+r.channels[ch].timingRMS,0)/rows.length,timingImproved:rows.filter(r=>r.channels[ch].timingRMS<r.channels[ch].gainCorrectedRMS).length,offsetOnlyRMS:rows.reduce((s,r)=>s+r.channels[ch].offsetOnlyRMS,0)/rows.length,gainCorrectedRMS:rows.reduce((s,r)=>s+r.channels[ch].gainCorrectedRMS,0)/rows.length,improved:rows.filter(r=>r.channels[ch].gainCorrectedRMS<r.channels[ch].offsetOnlyRMS).length}))};
writeFileSync(out,JSON.stringify({summary,rows},null,2)+'\n');console.log(JSON.stringify(summary,null,2));
