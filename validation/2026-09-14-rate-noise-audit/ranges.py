import urllib.request,json,time,struct,pathlib,numpy as np
P=pathlib.Path(__file__).resolve().parent;B='http://192.168.1.209:8080'
def setv(k,v):return json.load(urllib.request.urlopen(urllib.request.Request(B+'/api/set',data=json.dumps(dict(control=k,value=v)).encode(),headers={'Content-Type':'application/json'})))
c=json.load(open('validation/2026-09-14-hardware-ui/ripple/interleave-cal.json'));co=np.array(c['offset']);out=[]
try:
 for k,v in [('acqmode',0),('tdiv',1e-5),('run',1)]:setv(k,v)
 for scale in [1,5,10]:
  setv('vdiv1',scale);setv('vdiv2',scale);time.sleep(1)
  b=urllib.request.urlopen(B+'/api/frame.bin?raw=1').read();n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n]);assert not h.get('fraction_bits');x=np.frombuffer(b[8+n:],dtype='u1').reshape(2,-1);means=np.array([[float(y[i::5].mean()-y.mean()) for i in range(5)] for y in x]);scores=[float(np.mean((means-co[:,[(i+r)%5 for i in range(5)]])**2)) for r in range(5)];r=int(np.argmin(scores));row=dict(scale=scale,range=[[int(y.min()),int(y.max())]for y in x],phase_means=means.tolist(),rotation=r,match_mse=scores[r]);out.append(row);(P/f'range-{scale}.bin').write_bytes(b);print(row,flush=True)
finally:
 setv('vdiv1',1);setv('vdiv2',2)
(P/'ranges.json').write_text(json.dumps(out,indent=2))
