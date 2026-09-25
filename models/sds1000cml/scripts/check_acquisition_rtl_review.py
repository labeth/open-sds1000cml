#!/usr/bin/env python3
"""Characterize original acquisition RTL benches in frozen offline source snapshots."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import SCHEDULER_SOURCES,snapshot_sources
A='fpga/acq_sram/'
base=sorted(set(SCHEDULER_SOURCES+[A+n+'.v' for n in ['stream_engine','finite_recall','capture_engine','capture_path','board_capture_path','finite_writer','record','acquisition_source','acquisition_path']]))
cases=[]
def case(name,top,sources,success,parameters=None,label=None,timeout=180):
 cases.append(dict(id=label or name,test=A+'sim/tb_'+name+'.v',top=top,sources=sources,success=success,parameters=parameters or {},timeoutSeconds=timeout))
case('unpack','tb',[A+'adc_unpack.v'],'PASS: all 80')
case('interleave','tb',[A+'interleave.v'],'PASS 80-to-32')
case('adc_interleave','tb',[A+'interleave.v','fpga/common/lane_in.v','fpga/common/ddio_pair.v'],'PASS factory-phase')
case('acquisition_source','tb_acquisition_source',[A+'acquisition_source.v'],'PASS acquisition source:')
case('trigger_pipeline','tb_trigger_pipeline',[A+'acquisition_path.v'],'PASS trigger pipeline:')
case('record','tb',[A+'record.v',A+'recall.v'],'PASS full 524288-word',timeout=300)
case('finite_writer_faults','tb_finite_writer_faults',[A+'finite_writer.v',A+'record.v'],'PASS finite writer:')
case('finite_recall_faults','tb_finite_recall_faults',[A+'finite_recall.v',A+'ordinal_counter.v'],'PASS finite recall faults:')
case('capture_start','tb_capture_start',base,'PASS shared start pipeline:')
case('board_capture','tb_board_capture',base,'PASS board capture:',timeout=300)
for aw in [13,19]:
 case('finite_capture','tb_finite_capture',base,'PASS full physical depth:' if aw==19 else 'PASS finite capture:',dict(AW=aw),f'finite-capture-{aw}',900)
for aw,continuation,full in [(13,0,0),(13,1,0),(19,1,1)]:
 case('finite_recall','tb_finite_recall',base,'PASS finite recall suite:',dict(AW=aw,CONTINUE_READS=continuation,FULL_ONLY=full),f'finite-recall-{aw}-{continuation}',900)
for wrapped in [0,1]:
 for enabled in [0,1]:
  case('capture_engine','tb_capture_engine',base,'PASS shared capture engine:' if enabled else 'PASS deep capture backend:',dict(WRAPPED=wrapped,ENABLE_STREAM=enabled),f'capture-engine-{wrapped}-{enabled}',300)
for label,params,success in [('stream',{},'PASS integrated ADC-source'),('deep',dict(ENABLE_STREAM=0),'PASS deep acquisition:'),('ready',dict(READY_ONLY=1),'PASS cached readiness:'),('host-fault',dict(HOST_FAULT_ONLY=1),'PASS recall host fault:')]:
 case('acquisition_path','tb_acquisition_path',base,success,params,'acquisition-path-'+label,300)
for phase in range(4):
 for short in [0,1]:case('command_bridge','tb_command_bridge',[A+'command_bridge.v'],'PASS command bridge',dict(PHASE=phase,SHORT_RESET=short),f'command-bridge-{phase}-{short}')
 for enabled in [0,1]:case('command_port','tb_command_port',[A+'command_bridge.v',A+'command_port.v'],'PASS command port',dict(PHASE=phase,ENABLE_STREAM=enabled),f'command-port-{phase}-{enabled}')
case('command_gpmc','tb_command_gpmc',[A+'command_bridge.v',A+'command_port.v','fpga/common/gpmc_slave.v'],'PASS real GPMC slave')
review=json.loads((ROOT/'evidence/acquisition-rtl-source-review.json').read_text());sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
for p,h in review['sourceSHA256'].items():assert sha(SOURCE/p)==h,p
report=dict(commit=REV,scope='Original unchanged self-checking benches with ideal clocks and behavioral memory; ADC/precision mocks remain explicit. -DSIM selects behavioral DDR wrappers. The original ADC interleave bench may fail compilation when its vendor dcfifo model is unavailable; compilation failures are retained, not counted as exercised assertions. No fitted timing or physical hardware qualification.',cases=[],runtime=dict(iverilog=subprocess.run(['iverilog','-V'],capture_output=True,text=True).stdout.splitlines()[0]))
target=ROOT/'evidence/acquisition-rtl-review-test-run.json'
with tempfile.TemporaryDirectory(prefix='sds-acquisition-review-') as tmp:
 snapshot=Path(tmp)/'sources';inputs=[dict(x,sources=x['sources']+['fpga/default/lanemap_seed.vh']) for x in cases];hashes=snapshot_sources(SOURCE,snapshot,inputs);report['runtimeInputSHA256']=hashes
 for c in cases:
  binary=str(Path(tmp)/c['id']);command=['iverilog','-g2012','-DSIM','-I','fpga/default','-s',c['top'],'-o',binary]+[f'-P{c["top"]}.{k}={v}' for k,v in c['parameters'].items()]+[c['test'],*c['sources']]
  compiled=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=60);sim=None;timedout=False
  if compiled.returncode==0:
   try:sim=subprocess.run(['vvp',binary],cwd=snapshot,capture_output=True,text=True,timeout=c['timeoutSeconds'])
   except subprocess.TimeoutExpired as exc:timedout=True;sim=subprocess.CompletedProcess(['vvp',binary],124,(exc.stdout or b'').decode() if isinstance(exc.stdout,bytes) else exc.stdout or '',(exc.stderr or b'').decode() if isinstance(exc.stderr,bytes) else exc.stderr or '')
  passed=bool(sim and sim.returncode==0 and any(x.startswith(c['success']) for x in sim.stdout.splitlines()))
  row=dict(**c,compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr,simulationCommand=['vvp',binary],simulationExitCode=sim.returncode if sim else None,simulationOutput=sim.stdout+sim.stderr if sim else '',timedOut=timedout,result='pass' if passed else 'fail',sourceSnapshot=True)
  report['cases'].append(row);report['status']='running';target.write_text(json.dumps(report,indent=2)+'\n');print(c['id'],row['result'],flush=True)
assert all(sha(SOURCE/p)==h for p,h in hashes.items()),'source changed during simulation'
report['status']='complete';report['result']='fail' if any(x['result']=='fail' for x in report['cases']) else 'pass';target.write_text(json.dumps(report,indent=2)+'\n')
print('CHARACTERIZED',len(cases),'cases;',sum(x['result']=='pass' for x in report['cases']),'pass;',sum(x['result']=='fail' for x in report['cases']),'fail; baseline only, native integration pending',flush=True)
