#!/usr/bin/env python3
"""Execute the documented original SRAM tester against frozen offline inputs."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV


def run():
    digest=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
    review=json.loads((ROOT/'evidence/sram-bench-rtl-source-review.json').read_text())
    for path,sha in review['sourceSHA256'].items():
        assert digest(SOURCE/path)==sha,path
    paths=['fpga/sram_bench/sim/tb.v','fpga/sram_bench/bench.v','fpga/common/gpmc_slave.v']
    hashes={p:digest(SOURCE/p) for p in paths}
    report=dict(commit=REV,status='running',runtimeInputSHA256=hashes,cases=[],scope='Original 10 MHz behavioral SRAM tester with normal and folded address-bit18 memory. Ideal PLL/DDR/SRAM models; no physical capacity, timing, host protocol or device qualification.')
    with tempfile.TemporaryDirectory(prefix='sds-sram-bench-review-') as tmp:
        snapshot=Path(tmp)/'sources'
        for path in paths:
            dest=snapshot/path;dest.parent.mkdir(parents=True,exist_ok=True);dest.write_bytes((SOURCE/path).read_bytes())
        binary=str(Path(tmp)/'bench.vvp')
        command=['iverilog','-g2012','-DBENCH_MHZ=10','-s','tb','-o',binary,*paths]
        compiled=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=60)
        report.update(compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr)
        for alias in [0,1]:
            command=['vvp',binary]+(['+alias'] if alias else [])
            sim=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=300) if compiled.returncode==0 else None
            output=sim.stdout+sim.stderr if sim else ''
            passed=bool(sim and sim.returncode==0 and f'PASS behavioral SRAM, alias test={alias}' in output.splitlines() and 'FATAL' not in output and 'FAIL' not in output)
            report['cases'].append(dict(id='sram-bench-alias' if alias else 'sram-bench-normal',alias=alias,simulationCommand=command,simulationExitCode=sim.returncode if sim else None,output=output,result='pass' if passed else ('fail' if sim else 'not-run')))
            print(report['cases'][-1]['id'],report['cases'][-1]['result'],flush=True)
        assert all(digest(SOURCE/p)==sha and digest(snapshot/p)==sha for p,sha in hashes.items())
    report['status']='complete';report['result']='pass' if all(c['result']=='pass' for c in report['cases']) else 'fail'
    (ROOT/'evidence/sram-bench-rtl-review-test-run.json').write_text(json.dumps(report,indent=2)+'\n')

if __name__=='__main__':run()
