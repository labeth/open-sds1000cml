#!/usr/bin/env python3
"""Run thirteen original legacy-top variants from frozen inputs, with two workers."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-059
import concurrent.futures,hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import snapshot_sources
from board_rtl_cases import CASES,VENDOR_DIR,VENDOR_FILES,outcome

def run():
    sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
    review=json.loads((ROOT/'evidence/board-rtl-source-review.json').read_text())
    for path,h in review['sourceSHA256'].items():assert sha(SOURCE/path)==h,path
    target=ROOT/'evidence/board-rtl-review-test-run.json'
    report=dict(status='running',commit=REV,cases=[],scope='Thirteen original legacy-top bench variants. Behavioral PLL/ADC/DDR replacements remain explicit; vendor-dependent cases use copied local Quartus simulation libraries, replacing behavioral DDR definitions to avoid duplicate primitives. Passing digital cases are partial evidence only. Continuation prints are checked externally against all37 expected values. No device, fitted timing or analog qualification.',runtime=dict(iverilog=subprocess.run(['iverilog','-V'],capture_output=True,text=True,check=True).stdout.splitlines()[0]))
    with tempfile.TemporaryDirectory(prefix='sds-board-review-') as tmp:
        snapshot=Path(tmp)/'sources'
        hashes=snapshot_sources(SOURCE,snapshot,[dict(c,sources=c['sources']+['fpga/default/lanemap_seed.vh']) for c in CASES]);report['runtimeInputSHA256']=hashes
        vendor={}
        for name in VENDOR_FILES:
            source=Path(VENDOR_DIR)/name;dest=snapshot/'vendor'/name;dest.parent.mkdir(exist_ok=True);dest.write_bytes(source.read_bytes())
            vendor['vendor/'+name]=dict(source=str(source),sha256=sha(dest))
        report['vendorInputs']=vendor
        def execute(c):
            binary=str(Path(tmp)/c['id'])
            command=['iverilog','-g2012','-s',c['top'],'-I','fpga/default','-o',binary]+['-D'+d for d in c['defines']]+[c['test'],*c['sources']]+(list(vendor) if c['vendor'] else [])
            compiled=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=180)
            sim=None;timeout=False
            simcommand=['vvp',binary,*c['plusargs']]
            if compiled.returncode==0:
                try:sim=subprocess.run(simcommand,cwd=snapshot,capture_output=True,text=True,timeout=c['timeoutSeconds'])
                except subprocess.TimeoutExpired as exc:
                    timeout=True;decoded=lambda s:s.decode() if isinstance(s,bytes) else s or ''
                    sim=subprocess.CompletedProcess(simcommand,124,decoded(exc.stdout),decoded(exc.stderr))
            row=dict(**c,compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr,simulationCommand=simcommand,simulationExitCode=sim.returncode if sim else None,simulationOutput=sim.stdout+sim.stderr if sim else '',timedOut=timeout,sourceSnapshot=True)
            row['status']=outcome(row);row['result']='pass' if row['status']=='partial' else 'fail'
            return row
        completed={}
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
            futures={pool.submit(execute,c):c for c in CASES}
            for future in concurrent.futures.as_completed(futures):
                row=future.result();completed[row['id']]=row;report['cases']=[completed[c['id']] for c in CASES if c['id'] in completed]
                target.write_text(json.dumps(report,indent=2)+'\n');print(row['id'],row['status'],flush=True)
        assert all(sha(SOURCE/p)==h for p,h in hashes.items()),'source changed during board campaign'
        assert all(sha(Path(v['source']))==v['sha256'] for v in vendor.values()),'vendor library changed during campaign'
    report['status']='complete';report['result']='pass' if all(c['result']=='pass' for c in report['cases']) else 'fail';target.write_text(json.dumps(report,indent=2)+'\n')
    print('CHARACTERIZED',len(CASES),'board cases:',report['result'],'; native integration pending',flush=True)
if __name__=='__main__':run()
