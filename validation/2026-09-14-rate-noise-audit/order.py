import pathlib,json,struct,numpy as np
from scipy.optimize import minimize_scalar
P=pathlib.Path(__file__).resolve().parent;b=(P/'original.bin').read_bytes();n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n]);x=np.frombuffer(b[8+n:],dtype='u1').reshape(2,-1)[:,:100000];dt=h['sample_s'];t=np.arange(x.shape[1])*dt
results=[]
for ch,y0 in enumerate(x):
 # Fit the observed periodic waveform; phase centering removes converter DC.
 means=np.array([y0[i::5].mean() for i in range(5)]);y=y0-means[np.arange(len(y0))%5]
 def fit(f,z=y):
  a=np.column_stack([np.ones(len(t)),np.sin(2*np.pi*f*t),np.cos(2*np.pi*f*t)]);c=np.linalg.lstsq(a,z,rcond=None)[0];return np.mean((z-a@c)**2)
 f=minimize_scalar(fit,bounds=(999000,1001000),method='bounded').x
 phases=[]
 for lane in range(5):
  tt=t[lane::5];a=np.column_stack([np.ones(len(tt)),np.sin(2*np.pi*f*tt),np.cos(2*np.pi*f*tt)]);c=np.linalg.lstsq(a,y[lane::5],rcond=None)[0];phases.append(np.arctan2(c[2],c[1]))
 phases=np.unwrap(phases);skew=(phases-np.mean(phases))/(2*np.pi*f)*1e9
 # Pairwise digital bit swaps: compare periodic-fit errors after per-lane DC removal.
 tt=t[:20000];a=np.column_stack([np.ones(len(tt))]+[fn(2*np.pi*k*f*tt) for k in range(1,16) for fn in (np.sin,np.cos)]);q=np.linalg.qr(a,mode='reduced')[0]
 def err(z):
  z=z[:len(tt)].astype(float);z-=np.array([z[i::5].mean() for i in range(5)])[np.arange(len(z))%5];return float(np.sqrt(np.mean((z-q@(q.T@z))**2)))
 baseline=err(y0);swaps=[]
 for i in range(8):
  for j in range(i+1,8):
   flip=((y0>>i)^(y0>>j))&1;z=y0^(flip<<i)^(flip<<j);swaps.append({'bits':[i,j],'rms':err(z)})
 lane_checks=[]
 for lane in range(5):
  zl=y0[lane:20000:5];ql=np.linalg.qr(a[lane::5],mode='reduced')[0]
  def le(z):
   z=z.astype(float);return float(np.sqrt(np.mean((z-ql@(ql.T@z))**2)))
  candidates=[]
  for i in range(8):
   for j in range(i+1,8):
    flip=((zl>>i)^(zl>>j))&1;candidates.append({'bits':[i,j],'rms':le(zl^(flip<<i)^(flip<<j))})
  lane_checks.append({'lane':lane,'baseline_rms':le(zl),'best_swap':min(candidates,key=lambda v:v['rms'])})
 row={'per_lane':lane_checks,'channel':ch+1,'frequency_hz':f,'lane_skew_ns':skew.tolist(),'baseline_rms':baseline,'best_swap':min(swaps,key=lambda v:v['rms']),'all_swaps':swaps};results.append(row);print({k:v for k,v in row.items() if k!='all_swaps'})
(P/'order.json').write_text(json.dumps(results,indent=2))
