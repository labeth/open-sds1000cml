#!/usr/bin/env python3
"""Cold-start, load RAM-only SRAM tester, measure clock and full-depth patterns."""
import datetime,json,pathlib,subprocess,sys,time,shutil
ROOT=pathlib.Path(__file__).resolve().parents[3]
rbf=pathlib.Path(sys.argv[1]).resolve()
assert rbf.name=='bench.rbf' and ROOT/'fpga/sram_bench/out' in rbf.parents
subprocess.run([sys.executable,str(ROOT/'fpga/sram_bench/audit.py'),str(rbf.parent)],check=True)
label=rbf.parent.name+'-'+datetime.datetime.now().strftime('%H%M%S')
out=ROOT/'the acq2 analysis branch'/label;out.mkdir(exist_ok=False)
for name in ('bench.rbf','bench.v','pll.v','gpmc_slave.v','bench.qsf','bench.sdc','audit.json'):
 shutil.copy2(rbf.parent/name,out/name)
ctl=[str(ROOT/'ota/dist/otactl'),'-tcp','192.168.1.209:5900'];seq=0

def call(args,timeout=40,check=True):
 global seq
 r=subprocess.run(ctl+args,capture_output=True,text=True,timeout=timeout)
 (out/f'{seq:03d}.txt').write_text('ARGS '+json.dumps(args)+'\n'+r.stdout+r.stderr);seq+=1
 if check and r.returncode:raise RuntimeError(r.stdout+r.stderr)
 return r

def obj(r):return json.JSONDecoder().raw_decode(r.stdout)[0]
print('Evidence',out,flush=True)
r=subprocess.run([str(ROOT/'ota/dist/otactl'),'-shelly','192.168.1.223','power','cycle'],capture_output=True,text=True,timeout=20)
(out/'power.txt').write_text(r.stdout+r.stderr);assert r.returncode==0
print('Waiting for cold boot',flush=True)
for _ in range(45):
 r=call(['ping'],check=False)
 if r.returncode==0:break
 time.sleep(1)
else:raise RuntimeError('OTA did not return')
assert '6.01.01.21R2' in call(['scpi','*IDN?']).stdout
call(['scpi','STOP'])
pids=call(['exec','pidof','SDS1000_arm.app']).stdout.splitlines()[0].split();assert len(pids)==1 and pids[0].isdigit()
call(['exec','kill','-STOP',pids[0]])
assert 'T (stopped)' in call(['exec','cat',f'/proc/{pids[0]}/status']).stdout
call(['put','/tmp/srambench.arm','/dev/srambench'])
call(['exec','chmod','755','/dev/srambench'])
call(['put',str(rbf),'/dev/bench.rbf'])
print('Loading volatile FPGA image',flush=True)
load=call(['exec','/dev/srambench','load','/dev/bench.rbf'])
assert 'identity=5b51' in load.stdout
clock=obj(call(['exec','/dev/srambench','measure']))
print('Measured MHz',clock['measured_hz']/1e6,flush=True)
latency=2;results=[]
for seed in (0x13579bdf,0xeca86420,0x87654321):
 r=obj(call(['exec','/dev/srambench','run',hex(seed),str(latency)]))
 if r['errors'] and not results:
  trials={latency:r}
  for candidate in range(8):
   if candidate==latency:continue
   trials[candidate]=obj(call(['exec','/dev/srambench','run',hex(seed),str(candidate)]))
  latency=min(trials,key=lambda x:trials[x]['errors'])
  r=trials[latency]
  print('Latency/error sweep',{x:t['errors'] for x,t in trials.items()},flush=True)
  (out/'latency-sweep.json').write_text(json.dumps(trials,indent=2)+'\n')
 results.append(r)
 print(hex(seed),'checked',r['checked'],'errors',r['errors'],'latency',latency,flush=True)
 if r['errors']:break
(out/'build-audit.json').write_text((rbf.parent/'audit.json').read_text())
(out/'summary.json').write_text(json.dumps({'clock':clock,'results':results,'rbf':str(rbf)},indent=2)+'\n')
print('DONE; tester remains loaded and vendor suspended',flush=True)
