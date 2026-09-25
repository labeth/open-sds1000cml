#!/usr/bin/env python3
"""Characterize the reviewed original offline profile-core GPMC integration recipe."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-053, REQ-SDS-054, REQ-SDS-055, REQ-SDS-056
import ast,hashlib,json,subprocess
from pathlib import Path
from inventory import ROOT,SOURCE,REV
SCRIPT='validation/2026-09-14-profile-core/test_profile.py'
MOCK='fpga/acq_sram/sim/tb_acquisition_path.v'

def run():
    script=SOURCE/SCRIPT
    assignments=[n for n in ast.parse(script.read_text()).body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='names' for t in n.targets)]
    assert len(assignments)==1
    names=ast.literal_eval(assignments[0].value)
    paths=[str((SOURCE/'fpga/acq_sram'/p).resolve().relative_to(SOURCE.resolve())) for p in names]+[SCRIPT,MOCK]
    sha=lambda p:hashlib.sha256((SOURCE/p).read_bytes()).hexdigest()
    hashes={p:sha(p) for p in paths}
    command=['python3',SCRIPT]
    timeout=False
    try:result=subprocess.run(command,cwd=SOURCE,text=True,capture_output=True,timeout=960)
    except subprocess.TimeoutExpired as exc:
        timeout=True
        decoded=lambda s:s.decode() if isinstance(s,bytes) else s or ''
        result=subprocess.CompletedProcess(command,124,decoded(exc.stdout),decoded(exc.stderr))
    assert all(sha(p)==h for p,h in hashes.items()),'source changed during profile-core characterization'
    output=result.stdout+result.stderr
    reported=json.loads(result.stdout.splitlines()[0])
    for name in names:
        path=str((SOURCE/'fpga/acq_sram'/name).resolve().relative_to(SOURCE.resolve()))
        assert reported[name]==hashes[path]
    mock=(SOURCE/MOCK).read_text().split('module tb_acquisition_path')[0]
    assert reported['adc_mocks']==hashlib.sha256(mock.encode()).hexdigest()
    passed=result.returncode==0 and any(s.startswith('PASS profile core:') for s in result.stdout.splitlines())
    report=dict(status='complete',commit=REV,command=command,exitCode=result.returncode,timedOut=timeout,result='pass' if passed else 'fail',runtimeInputSHA256=hashes,runnerReportedSHA256=reported,derivedMock=dict(source=MOCK,transformation='Keep source prefix before module tb_acquisition_path, matching the original recipe',sha256=reported['adc_mocks']),output=output,scope='Original profile-core bench uses real GPMC slave and finite acquisition/readout with ADC/precision mocks and ideal SRAM/clocks. Forced acquisition fault verifies host invalidation. No board wrapper, PLL/vendor FIFO, physical timing, analog performance, default ABI or legacy top variant is qualified. Native trace/result integration remains pending.')
    (ROOT/'evidence/board-core-review-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
    print('CHARACTERIZED profile core:',report['result'],'; native integration pending',flush=True)
if __name__=='__main__':run()
