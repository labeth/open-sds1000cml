"""Hardware capture audit. Fit residual includes source/ADC distortion, not ENOB."""
import urllib.request,json,struct,time,pathlib,sys,numpy as np
from scipy.optimize import minimize_scalar
P=pathlib.Path(__file__).resolve().parent; B='http://192.168.1.209:8080'
def get(p):return urllib.request.urlopen(B+p,timeout=20).read()
def setv(k,v):return json.loads(urllib.request.urlopen(urllib.request.Request(B+'/api/set',data=json.dumps(dict(control=k,value=v)).encode(),headers={'Content-Type':'application/json'})).read())
def frame():
 b=get('/api/frame.bin?raw=1');n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n]);q=h.get('fraction_bits')==8;y=np.frombuffer(b[8+n:],dtype='<u2' if q else 'u1').astype(float).reshape(2,-1)/(256 if q else 1);return b,h,y
results=json.loads((P/'measurements.json').read_text()) if (P/'measurements.json').exists() else []
try:
 for name,mode,td,avg in [('raw',0,5e-7,1),('r16',4,5e-7,1),('r16avg4',4,5e-7,4),('r16avg16',4,5e-7,16),('r16avg64',4,5e-7,64),('r32',4,1e-5,1),('r32avg16',4,1e-5,16),('r256',4,1e-4,1),('r256avg4',4,1e-4,4),('r256avg16',4,1e-4,16)]:
  if len(sys.argv)>1 and name not in sys.argv[1:]:continue
  for k,v in [('acqmode',mode),('tdiv',td),('avgcount',avg),('run',1)]:setv(k,v)
  time.sleep(1); samples=[];seq=-1;deadline=time.monotonic()+35
  while len(samples)<(6 if avg>=64 else 12) and time.monotonic()<deadline:
   b,h,y=frame()
   if h['seq']==seq or (avg>1 and f'{avg}/{avg}' not in h.get('filter','')):time.sleep(.02);continue
   seq=h['seq'];guard=max(32,h.get('filter_guard',0));y=y[:,guard:-guard];dt=h['sample_s'];n=y.shape[1]
   if n<64:continue
   # Harmonic fit includes rounded triangle harmonics. RMS is still not ENOB.
   maxharm=min(15,int(.44/dt/1e6));t=(np.arange(n)-(n-1)/2)*dt
   def design(f):return np.column_stack([np.ones(n)]+[fn(2*np.pi*k*f*t) for k in range(1,maxharm+1) for fn in (np.sin,np.cos)])
   def calc(f):
    a=design(f);r=y.T-a@np.linalg.lstsq(a,y.T,rcond=None)[0];return float(np.mean(r*r)),r
   if maxharm:
    opt=minimize_scalar(lambda f:calc(f)[0],bounds=(999000,1001000),method='bounded',options={'xatol':.05});r=calc(opt.x)[1];freq=opt.x
   else:r=(y-y.mean(axis=1)[:,None]).T;freq=None
   samples.append({'seq':seq,'rms_codes':np.sqrt(np.mean(r*r,axis=0)).tolist(),'ptp_codes':np.ptp(y,axis=1).tolist(),'frequency':freq,'n':n})
  (P/(name+'.bin')).write_bytes(b);(P/(name+'.png')).write_bytes(get('/api/screen.png'))
  result={'name':name,'header':h,'samples':samples,'median_rms_codes':np.median([s['rms_codes'] for s in samples],axis=0).tolist() if samples else None,'input_in_passband':h.get('decimation',1)<=32,'note':'Harmonic fit residual includes noise and distortion; slow /256 suppresses the 1 MHz input, so noise floor only.'}
  results=[r for r in results if r["name"]!=name];results.append(result);(P/'measurements.json').write_text(json.dumps(results,indent=2));print(name,result['median_rms_codes'],len(samples),flush=True)
finally:
 for k,v in [('acqmode',4),('tdiv',5e-7),('avgcount',16),('run',1)]:setv(k,v)
