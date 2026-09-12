#!/usr/bin/env python3
"""Cold-start and validate the isolated SRAM capture image; volatile writes only."""
import datetime,json,pathlib,shutil,subprocess,time,sys
ROOT=pathlib.Path(__file__).resolve().parents[3]
phase=int(sys.argv[1]) if len(sys.argv)>1 else 1000
assert phase in (1000,2000,3000)
seed=int(sys.argv[2]) if len(sys.argv)>2 else 1
build=ROOT/(f'fpga/acq_sram/out/250mhz-p{phase}-interleave'+(f'-seed{seed}' if seed!=1 else ''))
subprocess.run(['python3',str(ROOT/'fpga/acq_sram/audit.py'),str(build)],check=True)
out=ROOT/'the acq2 analysis branch'/datetime.datetime.now().strftime('device-%H%M%S')
out.mkdir()
for name in ('bench.rbf','interleave.v','adc_pll.v','bench.v','record.v','transport.v','adc_unpack.v','ddio_pair.v','lane_in.v','gpmc_slave.v','pll.v','lanemap_seed.vh','bench.qsf','bench.sdc','audit.json'):
 shutil.copy2(build/name,out/name)
ctl=[str(ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900'];seq=0

def call(args,check=True):
 global seq
 r=subprocess.run(ctl+args,capture_output=True,text=True,timeout=40)
 (out/f'{seq:03d}.txt').write_text('ARGS '+json.dumps(args)+'\n'+r.stdout+r.stderr);seq+=1
 if check and r.returncode:raise RuntimeError(r.stdout+r.stderr)
 return r

def obj(r):return json.JSONDecoder().raw_decode(r.stdout)[0]
print('Evidence',out,flush=True)
r=subprocess.run([str(ROOT/'ota/dist/otactl'),'-shelly','192.168.1.223','power','cycle'],capture_output=True,text=True,timeout=20)
(out/'power.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
for _ in range(45):
 if call(['ping'],False).returncode==0:break
 time.sleep(1)
else:raise RuntimeError('cold boot failed')
assert '6.01.01.21R2' in call(['scpi','*IDN?']).stdout
call(['scpi','STOP'])
pid=call(['exec','pidof','SDS1000_arm.app']).stdout.splitlines()[0].strip();assert pid.isdigit()
call(['exec','kill','-STOP',pid]);assert 'T (stopped)' in call(['exec','cat',f'/proc/{pid}/status']).stdout
call(['put','/tmp/acqsram-il.arm','/dev/acqsram']);call(['exec','chmod','755','/dev/acqsram'])
call(['put',str(build/'bench.rbf'),'/dev/acq-capture.rbf'])
assert 'identity=5a52' in call(['exec','/dev/acqsram','load','/dev/acq-capture.rbf']).stdout
print('Loaded capture probe',flush=True)
def probe(*args):return obj(call(['exec','/dev/acqsram',*map(str,args)]))
def valid(status):
 m=status['capture'];assert m['revision']==7 and m['sample_rate_hz']==500000000 and m['interleaved'] and m['locked'] and not m['data_fault'], status
 return status
results=[]
if '--trace' in sys.argv:
 for attempt in range(2):
  print('warm capture',probe('capture',0,0,64),flush=True)
 print('full capture',probe('capture',0,524271,17),flush=True)
 for off in [0,16,32,512]:
  print('physical',probe('physical',off,16),flush=True)
  print('before buffer',probe('readtrace'),flush=True)
 for off in [524272,524256,524224,496]:
  print('prefix',probe('physical',off,64),flush=True)
 print('continued',probe('recall','/dev/acq-small.bin',528,16),flush=True)
 print('before buffer',probe('readtrace'),flush=True)
 sys.exit(0)
for attempt in range(2):
 status=valid(probe('capture',0,524271,17));assert status['length']==524288
 result=probe('recall','/dev/acq-counter.bin',0,524288)
 assert result['bytes']==2097152 and result['first']==0 and result['last']==524287 and result['nonconsecutive_words']==0,result
 assert result['sha256']=='ae42b13d7e0af3e77723caf8357d34c7e061526ed9eefeb67b04a0aaa69f33e2',result
 results.append({'kind':'counter','status':status,'recall':result});print(results[-1],flush=True)
# Center the floating inputs using volatile offset DACs before ADC captures.
for ch in range(2):call(['exec','/dev/acqsram','offset',str(ch),'10450'])
snapshot=probe('snapshot',256);(out/'snapshot.json').write_text(json.dumps(snapshot,indent=2)+'\n')
for attempt in range(3):
 status=valid(probe('capture',1,262144,262144));assert status['length']==524288
 result=probe('recall','/dev/acq-adc.bin',0,524288)
 assert result['bytes']==2097152,result
 call(['get','/dev/acq-adc.bin',str(out/f'adc-{attempt}.bin')])
 results.append({'kind':'adc','status':status,'recall':result});print(results[-1],flush=True)
(out/'qualification.json').write_text(json.dumps(results,indent=2)+'\n')
print('Full-capacity counter and ADC checks passed. Volatile interleave image remains loaded for follow-up tests.',flush=True)
