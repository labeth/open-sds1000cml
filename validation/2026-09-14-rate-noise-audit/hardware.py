import urllib.request,json,struct,time,pathlib
P=pathlib.Path(__file__).resolve().parent;B='http://192.168.1.209:8080'
def setv(k,v):
 r=json.load(urllib.request.urlopen(urllib.request.Request(B+'/api/set',data=json.dumps(dict(control=k,value=v)).encode(),headers={'Content-Type':'application/json'})));assert r['ok'],r

def frame():
 b=urllib.request.urlopen(B+'/api/frame.bin?raw=1',timeout=20).read();n=struct.unpack_from('<I',b,4)[0];return b,json.loads(b[8:8+n])
rows=[]
try:
 for k,v in [('acqmode',0),('vdiv1',1),('vdiv2',2),('norm',0),('run',1)]:setv(k,v)
 for td,dec in [(1e-9,1),(5e-7,1),(1e-5,1),(2e-4,1),(5e-4,16),(2e-3,32)]:
  setv('tdiv',td);deadline=time.monotonic()+15
  while True:
   b,h=frame()
   if h.get('tdiv_s')==td and h.get('decimation')==dec:break
   assert time.monotonic()<deadline,h
   time.sleep(.1)
  assert abs(h['sample_s']-2e-9*dec)<1e-15
  rows.append(h);print('NORMAL',td,'rate',1/h['sample_s'],'depth',h['capture_depth'],flush=True)
 for scale in [1,2,5,10]:
  _,old=frame();setv('tdiv',1e-5);setv('vdiv1',scale);setv('vdiv2',scale);deadline=time.monotonic()+15
  while True:
   b,h=frame()
   if h['seq']>old['seq']+1 and h.get('tdiv_s')==1e-5 and h.get('decimation')==1 and h.get('vpc1')==scale/25 and h.get('vpc2')==scale/25:break
   assert time.monotonic()<deadline,h
   time.sleep(.1)
  assert 'phase checked' in h.get('filter',''),(scale,h);print('Calibration',scale,'V/div PASS',flush=True)
 for rate in [31250000,15625000]:
  setv('acqmode',4);setv('precisionrate',rate)
  for td in [5e-7,1e-5]:
   setv('tdiv',td);deadline=time.monotonic()+15
   while True:
    b,h=frame()
    if h.get('tdiv_s')==td and abs(1/h['sample_s']-rate)<.01:break
    assert time.monotonic()<deadline,h
    time.sleep(.1)
   rows.append(h);print('PRECISION',td,'rate',1/h['sample_s'],flush=True)
finally:
 for k,v in [('acqmode',0),('vdiv1',1),('vdiv2',2),('tdiv',1e-5),('trigsource',1),('triglevelcode',32457),('norm',0),('run',1)]:setv(k,v)
 time.sleep(1);setv('run',0)
(P/'hardware-rates.json').write_text(json.dumps(rows,indent=2))
