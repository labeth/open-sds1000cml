#!/usr/bin/env python3
"""REQ-SDS-048: characterize the original scheduling experiment.

Baseline review runner: no source annotations or native result integration.
The separate packet-bank and SRAM-backed paths are explicit.
"""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-048
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import snapshot_sources,SCHEDULER_SOURCES
A='fpga/acq_sram/'
CASES=[dict(id='sram-timeslice-original',test=A+'sim/tb_sram_timeslice.v',top='tb',sources=[A+n+'.v' for n in ['transport','ingress_path','ingress_stream','ingress_fifo','word_bridge']]+[A+'sim/ddr_model.v'],success='PASS timeslice:',parameters={},timeoutSeconds=900)]

def run():
 sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
 review=json.loads((ROOT/'evidence/remaining-rtl-source-review.json').read_text())
 for p,h in review['sourceSHA256'].items():assert sha(SOURCE/p)==h,p
 target=ROOT/'evidence/timeslice-experiment-test-run.json'
 report=dict(commit=REV,status='running',cases=[],scope='Original full-geometry scheduling experiment: AW19,BATCH5120,FIFO4608,ROUNDS16,HOST_DELAY2500000. The scheduler is testbench code, with ideal clocks and SRAM; this is not execution of the production hardware scheduler or physical qualification.',runtime=dict(iverilog=subprocess.run(['iverilog','-V'],capture_output=True,text=True,check=True).stdout.splitlines()[0]))
 with tempfile.TemporaryDirectory(prefix='sds-timeslice-review-') as tmp:
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
   passed=bool(sim and sim.returncode==0 and any(s.startswith(c['success']) for s in sim.stdout.splitlines()) and 'FATAL' not in sim.stdout and 'FAIL' not in sim.stdout)
   row=dict(**c,compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr,simulationCommand=['vvp',binary],simulationExitCode=sim.returncode if sim else None,simulationOutput=sim.stdout+sim.stderr if sim else '',timedOut=timedout,result='pass' if passed else 'fail',sourceSnapshot=True)
   report['cases'].append(row);target.write_text(json.dumps(report,indent=2)+'\n');print(c['id'],row['result'],flush=True)
  assert all(sha(SOURCE/p)==h for p,h in hashes.items()),'source changed during simulation'
 report['status']='complete';report['result']='pass' if all(c['result']=='pass' for c in report['cases']) else 'fail'
 target.write_text(json.dumps(report,indent=2)+'\n')
 print('CHARACTERIZED',len(CASES),'cases:',report['result'],'; native integration pending',flush=True)
if __name__=='__main__':run()
