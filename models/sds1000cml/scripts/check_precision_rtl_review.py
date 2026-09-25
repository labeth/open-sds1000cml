#!/usr/bin/env python3
"""REQ-SDS-040: characterize precision benches in an immutable local snapshot.

Baseline review runner: no source annotations or native result integration.
The historical parallel composition and ideal FIFO dependencies are explicit.
"""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-040
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import snapshot_sources
A='fpga/acq_sram/'
REFERENCE='validation/2026-09-14-stream-path/precision-tail/reference_precision.v'
REF_HASH='dc03fe4fd3d2daf0d0f757f1f0f9c942793f650f8ab709aba881b95860598b33'
CASES=[]
for name,top,success in [
 ('integrator','tb','PASS split integrator:'),
 ('precision_fir','tb','PASS 500 independent FIR outputs:'),
 ('precision','tb','PASS precision /4096:'),
 ('precision_shared','tb_precision_shared','PASS complete shared precision path,'),
 ('precision_config','tb_precision_config','PASS precision settings/rearm'),
 ('precision_tail_queue','tb_precision_tail_queue','PASS RAM queue: reset during filter update'),
 ('precision_tail','tb_precision_tail','PASS scheduled precision tail:')]:
 sources=[A+'precision.v',A+'precision_tail.v']
 if name=='precision':sources.append(A+'sim/tb_precision_config.v')
 CASES.append(dict(id=name,test=A+'sim/tb_'+name+'.v',top=top,sources=sources,success=success,timeoutSeconds=900,requirements=['REQ-SDS-040']))

def run():
 sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
 review=json.loads((ROOT/'evidence/precision-rtl-source-review.json').read_text())
 for p,h in review['sourceSHA256'].items():assert sha(SOURCE/p)==h,p
 target=ROOT/'evidence/precision-rtl-review-test-run.json'
 report=dict(commit=REV,status='running',cases=[],scope='Seven original self-checking precision benches. Full remaining tail logs0..12; full shared wrapper logs4..13; ideal dcfifo model explicitly supplied to legacy DC bench. Frozen parallel reference shares unchanged fast CIC modules. No physical CDC, vendor FIFO timing, fitted timing or hardware qualification.',runtime=dict(iverilog=subprocess.run(['iverilog','-V'],capture_output=True,text=True,check=True).stdout.splitlines()[0]))
 with tempfile.TemporaryDirectory(prefix='sds-precision-review-') as tmp:
  snapshot=Path(tmp)/'sources';hashes=snapshot_sources(SOURCE,snapshot,[dict(c,sources=c['sources']+[REFERENCE]) for c in CASES]);report['runtimeInputSHA256']=hashes
  assert hashes[REFERENCE]==REF_HASH
  reference=(snapshot/REFERENCE).read_text();current=(snapshot/(A+'precision.v')).read_text()
  assert reference.split('module adc_precision')[0]==current.split('module adc_precision')[0]
  derived='module reference_adc_precision'+reference.split('module adc_precision',1)[1]
  (snapshot/'reference.v').write_text(derived)
  report['derivedReference']=dict(source=REFERENCE,sourceSHA256=REF_HASH,transformation='Keep adc_precision module body and rename module to reference_adc_precision; reuse text-identical current fast CIC modules.',sha256=hashlib.sha256(derived.encode()).hexdigest())
  for c in CASES:
   binary=str(Path(tmp)/c['id']);sources=list(c['sources'])
   if c['id'] in ['precision_shared','precision_config']:sources+=['reference.v']
   command=['iverilog','-g2012','-s',c['top'],'-o',binary,c['test'],*sources]
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
