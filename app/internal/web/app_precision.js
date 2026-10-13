// ENGMODEL-OWNER-UNIT: FU-WEB-APP-PRECISION
"use strict";

// Least-squares amplitude/DC for a frequency/phase candidate. Fits never alter
// the acquisition. Their residual is shown separately, including model error.
// TRLC-LINKS: REQ-SDS-205
function precisionFitSignal(y, dt, seedHz, model) {
  if (y.length < 64 || !(dt > 0) || !(seedHz > 0) || seedHz * dt >= .4 || y.length * dt * seedHz < 2)
    throw new Error("Need at least two cycles and enough samples; check the frequency measurement.");
  // TRLC-LINKS: REQ-SDS-205
  const basis = phase => model === "sine" ? Math.sin(2 * Math.PI * phase) : 1 - 4 * Math.abs((phase - Math.floor(phase)) - .5);
  // TRLC-LINKS: REQ-SDS-205
  function trial(hz, phase) {
    let sx=0, sy=0, sxx=0, sxy=0;
    for (let i=0;i<y.length;i++) {const x=basis((i-(y.length-1)/2)*dt*hz+phase);sx+=x;sy+=y[i];sxx+=x*x;sxy+=x*y[i];}
    const den=sxx-sx*sx/y.length;
    if (den<=1e-12) return {mse:Infinity};
    const amplitude=(sxy-sx*sy/y.length)/den, offset=(sy-amplitude*sx)/y.length;
    let mse=0;
    for (let i=0;i<y.length;i++) {const d=y[i]-offset-amplitude*basis((i-(y.length-1)/2)*dt*hz+phase);mse+=d*d;}
    return {hz,phase,amplitude,offset,mse:mse/y.length};
  }
  let best={mse:Infinity};
  for (let f=-2;f<=2;f++) for (let p=0;p<48;p++) {const r=trial(seedHz*(1+f*.005),p/48);if(r.mse<best.mse) best=r;}
  let df=seedHz*.0025, dp=1/48;
  for(let k=0;k<12;k++) {
    const old=best;
    for(let f=-1;f<=1;f++) for(let p=-1;p<=1;p++) {const r=trial(old.hz+f*df,old.phase+p*dp);if(r.mse<best.mse)best=r;}
    df*=.5;dp*=.5;
  }
  best.fitted=Array.from(y,(_,i)=>best.offset+best.amplitude*basis((i-(y.length-1)/2)*dt*best.hz+best.phase));
  best.residual=Array.from(y,(v,i)=>v-best.fitted[i]);
  best.rms=Math.sqrt(best.mse);
  return best;
}

// Diagnostic periodic fit: remove harmonic content before reporting residual.
// This deliberately excludes harmonic distortion and MUST NOT be called ENOB.
// TRLC-LINKS: REQ-SDS-205
function precisionPeriodicFit(y, dt, seedHz) {
  if(y.length<64 || !(dt>0) || !(seedHz>0) || seedHz*dt>=.4 || y.length*dt*seedHz<2)
    throw new Error("Need at least two cycles and enough samples.");
  const n=y.length, harmonics=Math.min(15,Math.floor(.44/(dt*seedHz)),Math.floor((n-8)/4)), m=2*harmonics+1;
  // TRLC-LINKS: REQ-SDS-205
  function trial(hz, keep=false) {
    const a=Array.from({length:m},()=>new Float64Array(m+1));
    const rows=new Array(n);
    for(let i=0;i<n;i++) {
      const row=new Float64Array(m);row[0]=1;
      const phase=2*Math.PI*hz*(i-(n-1)/2)*dt;
      for(let k=1;k<=harmonics;k++){row[2*k-1]=Math.sin(k*phase);row[2*k]=Math.cos(k*phase);}
      rows[i]=row;
      for(let j=0;j<m;j++){a[j][m]+=row[j]*y[i];for(let k=0;k<=j;k++)a[j][k]+=row[j]*row[k];}
    }
    for(let j=0;j<m;j++)for(let k=0;k<j;k++)a[k][j]=a[j][k];
    for(let j=0;j<m;j++) {
      let pivot=j;for(let k=j+1;k<m;k++)if(Math.abs(a[k][j])>Math.abs(a[pivot][j]))pivot=k;
      [a[j],a[pivot]]=[a[pivot],a[j]];
      if(Math.abs(a[j][j])<1e-10)throw new Error("Periodic fit is ill-conditioned.");
      for(let k=j+1;k<m;k++){const q=a[k][j]/a[j][j];for(let l=j;l<=m;l++)a[k][l]-=q*a[j][l];}
    }
    const c=new Float64Array(m);
    for(let j=m-1;j>=0;j--){let v=a[j][m];for(let k=j+1;k<m;k++)v-=a[j][k]*c[k];c[j]=v/a[j][j];}
    const fitted=rows.map(row=>row.reduce((sum,v,j)=>sum+v*c[j],0));
    const residual=Array.from(y,(v,i)=>v-fitted[i]);const sse=residual.reduce((sum,v)=>sum+v*v,0);
    return {hz,harmonics,amplitude:(Math.max(...fitted)-Math.min(...fitted))/2,offset:c[0],rms:Math.sqrt(sse/(n-m-1)),mse:sse/n,...(keep?{fitted,residual}:{})};
  }
  // Golden-section search around the hardware measurement. The narrow bracket
  // avoids choosing a different spectral peak; reject unsupported short records.
  let lo=seedHz*.998,hi=seedHz*1.002,g=(Math.sqrt(5)-1)/2;
  let x=hi-g*(hi-lo),z=lo+g*(hi-lo),fx=trial(x),fz=trial(z);
  for(let k=0;k<26;k++) {
    if(fx.mse<fz.mse){hi=z;z=x;fz=fx;x=hi-g*(hi-lo);fx=trial(x);}
    else{lo=x;x=z;fx=fz;z=lo+g*(hi-lo);fz=trial(z);}
  }
  return trial(fx.mse<fz.mse?x:z,true);
}

// TRLC-LINKS: REQ-SDS-099
function updatePrecisionLimits(f) {
  const warning=document.getElementById("acquisitionWarning");
  if(warning) warning.textContent=(f.filter||"").includes("calibration bypassed") ? "Interleave correction unavailable at this range/input" : "";
  const rate=document.getElementById("actualSampleRate");
  // A peak-detect frame's points are bucket extremes; the capture itself ran
  // at capture_sample_s (it read 20.8 MS/s at 10 us/div for a 500 MS/s record).
  const capS=f.capture_sample_s>0 ? f.capture_sample_s : f.sample_s;
  if(rate) rate.textContent=capS>0 ? "Actual: "+eng(1/capS,"S/s")+" per channel"+(f.capture_sample_s>0?" (display: min/max of "+Math.round(2*f.sample_s/capS)+" samples)":"") : "Sample rate: —";
  const el=document.getElementById("precisionLimits");if(!el)return;
  const gain=f.noise_gain_ideal||1;
  const bits=Math.log2(gain);
  const bw=f.bandwidth_hz, pass=f.passband_hz;
  el.textContent=(bw?"−3 dB: "+eng(bw,"Hz")+"; ":"")+(pass?"Qualified passband: "+eng(pass,"Hz")+"; ":"")+
    (gain>1?"ideal noise ÷"+gain.toFixed(2)+", +"+bits.toFixed(2)+" bits (independent noise only; not measured ENOB). ":"")+
    "Output Nyquist: "+eng(.5/capS,"Hz")+"; FPGA reduction ×"+(f.decimation||1)+"; " + ((f.decimation||1)>1?"transport ÷"+((f.decimation||1)/2)+" for equal duration. ":"")+(f.filter||"Unfiltered");

}

// precisionWindow is the visible part [a, b] of a capture, block-averaged to at
// most maxN points: its first 4096 samples alone can hold under a cycle of a
// screen of raw samples. Clipping is judged on the samples before they are
// averaged.
// TRLC-LINKS: REQ-SDS-205
function precisionWindow(sig, a, b, dt, maxN) {
  const start = Math.max(0, Math.floor(a * sig.length)), end = Math.min(sig.length, Math.ceil(b * sig.length));
  for (let i = start; i < end; i++) {
    const v = sig[i];
    if (!Number.isFinite(v) || v <= 0 || v >= 255) throw new Error("Fit requires an unclipped, valid window.");
  }
  const k = Math.max(1, Math.ceil((end - start) / maxN)), y = [];
  for (let i = start; i + k <= end; i += k) {
    let sum = 0;
    for (let j = 0; j < k; j++) sum += sig[i + j];
    y.push(sum / k);
  }
  return { y, dt: dt * k, samples: end - start };
}

// precisionSampledFrame takes one capture of samples through the raw feed (as
// the eye and super-res do) when the display holds min/max pairs: a screen of
// more samples than the page's columns. null when none comes (a stopped
// record has no new capture).
// TRLC-LINKS: REQ-SDS-205
async function precisionSampledFrame() {
  for (let i = 0; i < 4; i++) {
    const r = await fetch("/api/frame.bin?since=" + (frame ? frame.seq : 0) + "&waitms=1500&raw=1&lease=raw");
    if (!r.ok) return null;
    const f = decodeBinFrame(await r.arrayBuffer());
    if (f && !f.unchanged && f.c1 && f.c1.length >= 64 && !f.peak_detect && !f.is_env && f.sample_s > 0) return f;
    if (!(st && st.running)) return null;
  }
  return null;
}

if(typeof document!=="undefined") {

  // TRLC-LINKS: REQ-SDS-205
  document.getElementById("precisionFit").onclick=async()=>{
    const out=document.getElementById("precisionFitResult");
    try {
      if(!frame)throw new Error("No capture yet.");
      let src=frame;
      if(frame.peak_detect||frame.is_env){
        // the display holds min/max pairs: fit a capture of samples instead
        // (a stopped record arrives as min/max pairs too)
        out.textContent="Taking a capture of samples…";
        src=await precisionSampledFrame();
        if(!src)throw new Error("Fit requires sampled data; min/max envelopes cannot be fitted and no capture of samples came. Run, or a faster time/div (a stopped record redraws from memory), or the Precision acquisition.");
      }
      const ch=+document.getElementById("precisionChannel").value, model=document.getElementById("precisionModel").value;
      const sig=ch?src.c2:src.c1, m=ch?frame.m2:frame.m1;
      const w=precisionWindow(sig,view.win.a,view.win.b,src.sample_s||src.col_span_s/src.cols,4096), y=w.y, dt=w.dt;
      const fit=model==="periodic"?precisionPeriodicFit(y,dt,m&&m.freq):precisionFitSignal(y,dt,m&&m.freq,model);
      const vpc=ch?frame.vpc2:frame.vpc1;
      if(!(vpc>0))throw new Error("Capture calibration unavailable.");
      const rms=fit.rms*vpc, vpp=2*Math.abs(fit.amplitude)*vpc;
      out.textContent=model+" fit, CH"+(ch+1)+", capture "+src.seq+": "+eng(fit.hz,"Hz")+", "+eng(vpp,"V")+" pp; residual RMS "+eng(rms,"V")+
        " ("+(100*rms/vpp).toFixed(2)+"% of Vpp). "+(rms>.05*vpp?"Poor model match. ":"")+(model==="periodic"?fit.harmonics+" harmonics removed; residual-equivalent "+Math.log2(256/(Math.sqrt(12)*fit.rms)).toFixed(2)+" bits over the original ADC range. Harmonic distortion excluded; not SINAD ENOB or accuracy. ":"Residual is noise + distortion + timing/model error, not ADC ENOB. ")+
        (w.samples>y.length?"The visible "+w.samples+" samples, averaged to "+y.length+" points":"The visible "+y.length+" samples")+"; snapshot only. "+(src.filter||frame.filter||"");
      const cv=document.getElementById("precisionFitCanvas");cv.hidden=false;const ctx=cv.getContext("2d"),wd=cv.width,h=cv.height;
      ctx.fillStyle="#071014";ctx.fillRect(0,0,wd,h);
      const lo=Math.min(...y),hi=Math.max(...y),rr=Math.max(...fit.residual.map(Math.abs),.001);
      // TRLC-LINKS: REQ-SDS-205
      function line(a,color,fn){ctx.strokeStyle=color;ctx.beginPath();a.forEach((v,i)=>{const x=i/(a.length-1)*wd, yy=fn(v);if(i)ctx.lineTo(x,yy);else ctx.moveTo(x,yy);});ctx.stroke();}
      line(y,"#e8c546",v=>100-(v-lo)/(hi-lo)*80);line(fit.fitted,"#e6ecf2",v=>100-(v-lo)/(hi-lo)*80);
      line(fit.residual,"#35c8e8",v=>155-v/rr*30);ctx.fillStyle="#ddd";ctx.fillText("Captured (yellow), fit (white)",8,12);ctx.fillText("Residual (cyan), ±"+eng(rr*vpc,"V"),8,122);
    } catch(e) {out.textContent=e.message;}
  };
}
if(typeof module!=="undefined")module.exports={precisionFitSignal,precisionPeriodicFit,precisionWindow};
