import pathlib,json,struct,urllib.request,time,numpy as np
P=pathlib.Path(__file__).resolve().parent;B='http://192.168.1.209:8080'
def read(b):
 n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n]);q=h.get('fraction_bits')==8;return h,np.frombuffer(b[8+n:],dtype='<u2' if q else 'u1').reshape(2,-1).astype(float)/(256 if q else 1)
for i in range(25):
 b=urllib.request.urlopen(B+'/api/frame.bin?raw=1',timeout=20).read();h,y=read(b)
 if h.get('cols')==1048576 and h.get('tdiv_s')==1e-5 and 'phase checked' in h.get('filter',''):break
 time.sleep(.5)
assert 'phase checked' in h.get('filter','') and h['cols']==1048576,h
(P/'corrected.bin').write_bytes(b);(P/'corrected-lcd.png').write_bytes(urllib.request.urlopen(B+'/api/screen.png').read());results={}
for name,b in [('before',(P/'original.bin').read_bytes()),('after',b)]:
 h,y=read(b);rows=[]
 for x in y:
  x=x[:262144];freq=np.fft.rfftfreq(len(x),h['sample_s']);s=np.abs(np.fft.rfft((x-x.mean())*np.hanning(len(x))));fund=s[(freq>0.99e6)&(freq<1.01e6)].max();spur=s[(freq>99.99e6)&(freq<100.01e6)].max();means=[float(x[i::5].mean()-x.mean())for i in range(5)];rows.append(dict(spur_dbc=float(20*np.log10(spur/fund)),phase_offsets=means,phase_ptp=float(np.ptp(means))))
 results[name]=rows
print(json.dumps(results,indent=2));(P/'comparison.json').write_text(json.dumps(results,indent=2))
