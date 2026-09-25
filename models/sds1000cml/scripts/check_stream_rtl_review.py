#!/usr/bin/env python3
"""REQ-SDS-186: characterize stream benches in an immutable local snapshot.

Baseline review runner: no source annotations or native result integration.
The separate packet-bank and SRAM-backed paths are explicit.
"""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-186
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import snapshot_sources,SCHEDULER_SOURCES
A='fpga/acq_sram/'
CASES=[]
for aw in [3,10,11]:
 for name,sources,success in [
  ('stream_banks',[A+'stream_banks.v'],'PASS stream banks:'),
  ('stream_path',[A+'stream_packetizer.v',A+'stream_banks.v'],'PASS packetizer + banks:')]:
  CASES.append(dict(id=name+'-'+str(aw),test=A+'sim/tb_'+name+'.v',top='tb',sources=sources,success=success,parameters=dict(BANK_AW=aw),timeoutSeconds=900))
for aw,target in [(13,10003),(19,65539)]:
 CASES.append(dict(id='sram-stream-'+str(aw),test=A+'sim/tb_sram_stream_path.v',top='tb_stream_path',sources=sorted(set(SCHEDULER_SOURCES+[A+'stream_path.v',A+'stream_engine.v'])),success='PASS stream wrapper:',parameters=dict(AW=aw,TARGET=target),timeoutSeconds=900))

def run():
 sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
 review=json.loads((ROOT/'evidence/stream-rtl-source-review.json').read_text())
 for p,h in review['sourceSHA256'].items():assert sha(SOURCE/p)==h,p
 target=ROOT/'evidence/stream-rtl-review-test-run.json'
 report=dict(commit=REV,status='running',cases=[],scope='Eight original stream cases: packetizer/banks at BANK_AW3,10,11 and SRAM wrapper AW13/TARGET10003,AW19/TARGET65539. Fixed ideal clocks and behavioral SRAM/RAM; no physical CDC, vendor FIFO timing, fitted timing or hardware qualification. Legacy adc_stream and top-level bench are not executed.',runtime=dict(iverilog=subprocess.run(['iverilog','-V'],capture_output=True,text=True,check=True).stdout.splitlines()[0]))
 with tempfile.TemporaryDirectory(prefix='sds-stream-review-') as tmp:
  snapshot=Path(tmp)/'sources';hashes=snapshot_sources(SOURCE,snapshot,CASES);report['runtimeInputSHA256']=hashes
  for c in CASES:
   binary=str(Path(tmp)/c['id']);sources=list(c['sources'])
   command=['iverilog','-g2012','-s',c['top'],'-o',binary]+[f'-P{c["top"]}.{k}={v}' for k,v in c['parameters'].items()]+[c['test'],*sources]
   compiled=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=60)
   sim=None;timedout=False
   if compiled.returncode==0:
    try:sim=subprocess.run(['vvp',binary],cwd=snapshot,capture_output=True,text=True,timeout=c['timeoutSeconds'])
    except subprocess.TimeoutExpired as exc:
     timedout=True
     def decoded(s):return s.decode() if isinstance(s,bytes) else s or ''
     sim=subprocess.CompletedProcess(['vvp',binary],124,decoded(exc.stdout),decoded(exc.stderr))
   passed=bool(sim and sim.returncode==0 and any(s.startswith(c['success']) for s in sim.stdout.splitlines()))
   row=dict(**c,compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr,simulationCommand=['vvp',binary],simulationExitCode=sim.returncode if sim else None,simulationOutput=sim.stdout+sim.stderr if sim else '',timedOut=timedout,result='pass' if passed else 'fail',sourceSnapshot=True)
   report['cases'].append(row);target.write_text(json.dumps(report,indent=2)+'\n');print(c['id'],row['result'],flush=True)
  assert all(sha(SOURCE/p)==h for p,h in hashes.items()),'source changed during simulation'
 report['status']='complete';report['result']='pass' if all(c['result']=='pass' for c in report['cases']) else 'fail'
 target.write_text(json.dumps(report,indent=2)+'\n')
 print('CHARACTERIZED',len(CASES),'cases:',report['result'],'; native integration pending',flush=True)
if __name__=='__main__':run()
