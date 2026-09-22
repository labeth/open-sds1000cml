import http.client,json,time,struct,pathlib
c=http.client.HTTPConnection('192.168.1.209',8080,timeout=20)
def get(p):c.request('GET',p);return c.getresponse().read()
def set(k,v):c.request('POST','/api/set',json.dumps(dict(control=k,value=v)),{'Content-Type':'application/json'});print(c.getresponse().read().decode().strip())
set('run',1);time.sleep(2)
s=json.loads(get('/api/status'));seq=0;seen=0;start=time.monotonic();rates=[];window=start;count=0
while time.monotonic()-start<30:
 b=get(f'/api/frame.bin?since={seq}&cols=2048&full=1&waitms=1000');n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n]);sn=h['seq']
 if sn!=seq:seen+=1;count+=1;seq=sn
 if time.monotonic()-window>=5:
  rates.append(count/(time.monotonic()-window));print('web fps',rates[-1]);count=0;window=time.monotonic()
 time.sleep(.01)
dt=time.monotonic()-start;e=json.loads(get('/api/status'));result=dict(web_fps=seen/dt,lcd_fps=(e['lcd_frames']-s['lcd_frames'])/dt,capture_fps=(e['seq']-s['seq'])/dt,interval_web_fps=rates,status=e)
print(json.dumps(result));pathlib.Path('validation/2026-09-14-live-preview/rates.json').write_text(json.dumps(result,indent=2))
set('run',0);start=time.monotonic()
for i in range(20):
 time.sleep(.5);b=get('/api/frame.bin?raw=1');n=struct.unpack_from('<I',b,4)[0];h=json.loads(b[8:8+n])
 if h.get('cols')==1048576:break
print('full stop',time.monotonic()-start,h.get('cols'),h.get('filter'))
pathlib.Path('validation/2026-09-14-live-preview/stopped-header.json').write_text(json.dumps(h,indent=2));pathlib.Path('validation/2026-09-14-live-preview/stopped.png').write_bytes(get('/api/screen.png'))
set('run',1)
