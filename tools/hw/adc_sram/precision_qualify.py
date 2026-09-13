#!/usr/bin/env python3
"""Revision 9 device qualification. Scope writes only RAM; cold boot on failure."""
import argparse, datetime, hashlib, json, pathlib, subprocess, time
ROOT=pathlib.Path(__file__).resolve().parents[3]
p=argparse.ArgumentParser();p.add_argument('build',type=pathlib.Path);p.add_argument('--helper',type=pathlib.Path,default=pathlib.Path('/tmp/acqsram-precision.arm'));p.add_argument('--origin-probe',action='store_true');args=p.parse_args()
build=args.build.resolve();subprocess.run(['python3',str(ROOT/'fpga/acq_sram/audit.py'),str(build)],check=True)
out=ROOT/'validation'/'2026-09-13-default'/datetime.datetime.now().strftime('precision-%H%M%S');out.mkdir(parents=True)
ctl=[str(ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900'];events=[]
def call(argv,check=True,timeout=45):
 r=subprocess.run(ctl+list(map(str,argv)),capture_output=True,text=True,timeout=timeout)
 events.append({'args':list(map(str,argv)),'code':r.returncode,'stdout':r.stdout,'stderr':r.stderr})
 (out/'commands.json').write_text(json.dumps(events,indent=2)+'\n')
 if check and r.returncode:raise RuntimeError(r.stdout+r.stderr)
 return r.stdout

def probe(*argv):
 text=call(['exec','env','SCOPE_EDMA=1','/dev/acqsram',*argv])
 return json.JSONDecoder().raw_decode(text[text.index('{'):])[0]

def power():
 subprocess.run([str(ROOT/'ota/dist/otactl'),'-shelly','192.168.1.223','power','cycle'],check=True,timeout=20,capture_output=True)

def wait_agent():
 for _ in range(50):
  try: r=subprocess.run(ctl+['ping'],capture_output=True,timeout=5)
  except subprocess.TimeoutExpired: continue
  if r.returncode==0:return
  time.sleep(1)
 raise RuntimeError('agent did not return')

results=[]
try:
 power();wait_agent();call(['app','stop'])
 call(['put',args.helper,'/dev/acqsram']);call(['exec','chmod','755','/dev/acqsram'])
 call(['put',build/'bench.rbf','/dev/acq-precision.rbf'])
 assert 'identity=5a52' in call(['exec','/dev/acqsram','load','/dev/acq-precision.rbf'])
 print('Loaded revision 9 candidate',flush=True)
 if args.origin_probe:
  for log in [0,4,5,8,12,0]:
   st=probe('capture',0,239,17,128,log)
   for bias in [0,1,2]:
    r=probe('recall-warm','/dev/acq-origin.bin',0,256,16,bias)
    results.append({'log':log,'bias':bias,'status':st,'recall':r})
    print('origin',log,bias,r['first'],r['last'],r['nonconsecutive_words'],flush=True)
  (out/'origin-results.json').write_text(json.dumps(results,indent=2)+'\n')
  print('Evidence:',out,flush=True)
  raise SystemExit(0)
 for log,n in [(0,524288),(4,524288),(8,524288),(12,524288),(16,4096),(0,524288)]:
  st=probe('capture',0,n-17,17,128,log)
  m=st['capture'];assert m['revision']==9 and m['locked'] and not m['data_fault'] and m['words']==n,st
  r=probe('recall','/dev/acq-precision.bin',0,n)
  expected=hashlib.sha256(b''.join(i.to_bytes(4,'little') for i in range(n))).hexdigest()
  assert r['sha256']==expected and r['nonconsecutive_words']==0,r
  results.append({'kind':'counter','log':log,'status':st,'recall':r});print('counter',log,n,r['seconds'],'PASS',flush=True)
 call(['exec','/dev/acqsram','range-both','8'])
 for log in [0,4,8,4]:
  st=probe('capture',1,262144,262144,128,log)
  r=probe('recall','/dev/acq-precision.bin',0,524288)
  call(['get','/dev/acq-precision.bin',out/f'adc-d{log}-{len(results)}.bin'])
  results.append({'kind':'adc','log':log,'status':st,'recall':r});print('adc',log,'PASS transfer',flush=True)
 for cfg in [17,49,81,113]:
  probe('capture',cfg,262144,262144,128,4)
  for _ in range(100):
   st=probe('status');m=st['capture']
   if m['frozen'] and m['ready']:break
   time.sleep(.02)
  else:raise RuntimeError('triangle edge did not trigger')
  assert m['triggered'] and not m['data_fault']
  r=probe('recall','/dev/acq-edge.bin',m['trigger_index']-4,16)
  call(['get','/dev/acq-edge.bin',out/f'edge-{cfg}.bin'])
  results.append({'kind':'edge','cfg':cfg,'status':st,'recall':r});print('edge',cfg,'PASS capture',flush=True)
 (out/'results.json').write_text(json.dumps(results,indent=2)+'\n')
 (out/'build.json').write_text((build/'audit.json').read_text())
 print('Evidence:',out,flush=True)
except Exception:
 (out/'partial-results.json').write_text(json.dumps(results,indent=2)+'\n')
 power()
 raise
