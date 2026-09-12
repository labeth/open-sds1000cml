#!/usr/bin/env python3
"""Recall and ADC checks against an already loaded acqsram probe; no reload."""
import datetime,json,pathlib,statistics,subprocess,sys,time
ROOT=pathlib.Path(__file__).resolve().parents[3]
out=ROOT/'the acq2 analysis branch'/datetime.datetime.now().strftime('checks-%H%M%S');out.mkdir()
ctl=[str(ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900'];seq=0

def call(args):
 global seq
 r=subprocess.run(ctl+args,capture_output=True,text=True,timeout=40)
 (out/f'{seq:03d}.txt').write_text('ARGS '+json.dumps(args)+'\n'+r.stdout+r.stderr);seq+=1
 if r.returncode:raise RuntimeError(r.stdout+r.stderr)
 return r

def probe(*args):return json.JSONDecoder().raw_decode(call(['exec','/dev/acqsram',*map(str,args)]).stdout)[0]
print('Evidence',out,flush=True)
mode=sys.argv[1]
if mode=='ramp':
 a=probe('capture',0,524271,17);assert a['length']==524288
 checks=[]
 for repeat in range(2):
  r=probe('recall','/dev/acq-full.bin',0,524288);checks.append(r);print(r,flush=True)
  assert r['first']==0 and r['last']==524287 and r['nonconsecutive_words']==0
 assert checks[0]['sha256']==checks[1]['sha256']
 (out/'summary.json').write_text(json.dumps(checks,indent=2)+'\n')
 call(['get','/dev/acq-full.bin',str(out/'ramp-full.bin')])
elif mode=='wrapped':
 a=probe('capture',16,262144,262144,255)
 assert a['status']&8 and not a['status']&16
 time.sleep(.05)
 a=probe('force');assert a['length']==524288 and a['trigger_index']==262144
 r=probe('recall','/dev/acq-wrapped.bin',0,524288);print(r,flush=True)
 assert r['first']>0 and r['nonconsecutive_words']==0 and r['last']==(r['first']+524287)&0xffffffff
 part=probe('recall','/dev/acq-window.bin',17,1024)
 assert part['first']==(r['first']+17)&0xffffffff and part['nonconsecutive_words']==0
 again=probe('recall','/dev/acq-wrapped.bin',0,524288);assert again['sha256']==r['sha256'];print(again,flush=True)
 (out/'summary.json').write_text(json.dumps({'capture':a,'full':r,'window':part,'repeat':again},indent=2)+'\n')
elif mode=='adc':
 a=probe('capture',1,524271,17);assert a['length']==524288
 checks=[]
 for repeat in range(2):
  r=probe('recall','/dev/acq-adc.bin',0,524288);checks.append(r);print(r,flush=True)
 assert checks[0]['sha256']==checks[1]['sha256']
 call(['get','/dev/acq-adc.bin',str(out/'adc-full.bin')])
 data=(out/'adc-full.bin').read_bytes()
 stats=[{'channel':ch+1,'samples':len(data[ch::2]),'mean':statistics.mean(data[ch::2]),'std':statistics.pstdev(data[ch::2]),'min':min(data[ch::2]),'max':max(data[ch::2])} for ch in range(2)]
 (out/'summary.json').write_text(json.dumps({'recalls':checks,'channels':stats},indent=2)+'\n');print(stats,flush=True)
elif mode in ('dc','dcfine'):
 # Small independent DAC sweeps while all ten cores are sampled atomically.
 # Always restore both DACs to the documented fallback zero after this probe.
 rows=[]
 try:
  for ch in (0,1):probe('offset',ch,10223)
  for ch in (0,1):
   for code in range(10200,10801,2 if mode=='dcfine' else 10):
    probe('offset',ch,code)
    raw=probe('snapshot',128)['cores']
    row={'channel_changed':ch+1,'dac_code':code,'cores':[{'mean':statistics.mean(s[i] for s in raw),'std':statistics.pstdev(s[i] for s in raw),'min':min(s[i] for s in raw),'max':max(s[i] for s in raw)} for i in range(10)]}
    rows.append(row);print('ch',ch+1,'DAC',code,'means',[round(x['mean'],1) for x in row['cores']],flush=True)
   probe('offset',ch,10223)
 finally:
  for ch in (0,1):probe('offset',ch,10223)
 (out/'summary.json').write_text(json.dumps(rows,indent=2)+'\n')
else:raise ValueError('ramp | adc | dc')
