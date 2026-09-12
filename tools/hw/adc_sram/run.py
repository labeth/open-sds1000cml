#!/usr/bin/env python3
"""Cold-start and validate the isolated SRAM capture image; volatile writes only."""
import datetime,json,pathlib,shutil,subprocess,time,sys
ROOT=pathlib.Path(__file__).resolve().parents[3]
phase=int(sys.argv[1]) if len(sys.argv)>1 else 4000
assert phase in (0,1000,2000,3000,4000)
build=ROOT/'fpga/acq_sram/out'/f'100mhz-p{phase}'
subprocess.run(['python3',str(ROOT/'fpga/acq_sram/audit.py'),str(build)],check=True)
out=ROOT/'the acq2 analysis branch'/datetime.datetime.now().strftime('device-%H%M%S')
out.mkdir()
for name in ('bench.rbf','bench.v','record.v','transport.v','adc_unpack.v','ddio_pair.v','lane_in.v','gpmc_slave.v','pll.v','lanemap_seed.vh','bench.qsf','bench.sdc','audit.json'):
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
call(['put','/tmp/acqsram.arm','/dev/acqsram']);call(['exec','chmod','755','/dev/acqsram'])
call(['put',str(build/'bench.rbf'),'/dev/acq-capture.rbf'])
assert 'identity=5a52' in call(['exec','/dev/acqsram','load','/dev/acq-capture.rbf']).stdout
print('Loaded capture probe',flush=True)
status=obj(call(['exec','/dev/acqsram','capture','0','524271','17']));print(status,flush=True)
assert status['length']==524288
result=obj(call(['exec','/dev/acqsram','recall','/dev/acq-first.bin','0','512']));print(result,flush=True)
call(['get','/dev/acq-first.bin',str(out/'first.bin')])
(out/'first-recall.json').write_text(json.dumps(result,indent=2)+'\n')
assert result['first']==0 and result['last']==511 and result['nonconsecutive_words']==0, 'counter recall mismatch'
print('Device remains in volatile capture probe for follow-up tests',flush=True)
