#!/usr/bin/env python3
"""Volatile cold-start ADC clock/core routing check. Inputs may remain floating."""
import datetime,json,pathlib,subprocess,time,statistics
ROOT=pathlib.Path(__file__).resolve().parents[3]
build=ROOT/'fpga/acq_sram/out/100mhz-p4000-interleave'
subprocess.run(['python3',str(ROOT/'fpga/acq_sram/audit.py'),str(build)],check=True)
out=ROOT/'the acq2 analysis branch'/datetime.datetime.now().strftime('clock-probe-%H%M%S');out.mkdir()
ctl=[str(ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900'];seq=0
for name in ['bench.v','interleave.v','record.v','transport.v','adc_pll.v','pll.v','bench.qsf','bench.sdc','audit.json']:(out/name).write_bytes((build/name).read_bytes())
def call(args,check=True):
 global seq
 r=subprocess.run(ctl+args,text=True,capture_output=True,timeout=90);(out/f'{seq:03d}.txt').write_text(json.dumps(args)+'\n'+r.stdout+r.stderr);seq+=1
 if check and r.returncode:raise RuntimeError(r.stdout+r.stderr)
 return r
r=subprocess.run([str(ROOT/'ota/dist/otactl'),'-shelly','192.168.1.223','power','cycle'],capture_output=True,text=True,timeout=20);(out/'power.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
for _ in range(45):
 if call(['ping'],False).returncode==0:break
 time.sleep(1)
assert '6.01.01.21R2' in call(['scpi','*IDN?']).stdout
call(['scpi','STOP']);pid=call(['exec','pidof','SDS1000_arm.app']).stdout.splitlines()[0];assert pid.isdigit();call(['exec','kill','-STOP',pid]);assert 'T (stopped)' in call(['exec','cat',f'/proc/{pid}/status']).stdout
call(['put','/tmp/acqsram-il.arm','/dev/acqsram']);call(['exec','chmod','755','/dev/acqsram']);call(['put',str(build/'bench.rbf'),'/dev/acq-capture.rbf']);assert 'identity=5a52' in call(['exec','/dev/acqsram','load','/dev/acq-capture.rbf']).stdout
print('Loaded diagnostic clock image',out,flush=True)
def probe(*args):return json.JSONDecoder().raw_decode(call(['exec','/dev/acqsram',*map(str,args)]).stdout)[0]
def means():
 rows=probe('snapshot',128)['cores'];return [statistics.mean(r[i] for r in rows) for i in range(10)]
results=[]
try:
 for bit in range(10):
  probe('encode-mask',1023)
  for ch in range(2):probe('offset',ch,10223)
  before=means();probe('encode-mask',1023^(1<<bit))
  for ch in range(2):probe('offset',ch,10600)
  after=means();delta=[round(b-a,3) for a,b in zip(before,after)]
  frozen=[i for i,d in enumerate(delta) if abs(d)<3];row={'disabled_encode_bit':bit,'before':before,'after':after,'delta':delta,'frozen_core_indices':frozen};results.append(row);print(row,flush=True)
  assert frozen==[[1],[0],[2],[3],[5],[4],[6],[7],[8],[9]][bit], row
  assert all(abs(d)>100 for i,d in enumerate(delta) if i not in frozen), row
finally:
 probe('encode-mask',1023)
 for ch in range(2):probe('offset',ch,10223)
 (out/'clock-core-map.json').write_text(json.dumps(results,indent=2)+'\n')
print('Probe complete; volatile diagnostic image remains loaded',flush=True)
