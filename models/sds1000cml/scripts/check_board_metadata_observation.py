#!/usr/bin/env python3
"""REQ-SDS-083: observe metadata before unchanged failing legacy assertions."""
# ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
# TRLC-LINKS: REQ-SDS-083
import hashlib,json,subprocess,tempfile,concurrent.futures
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from run_rtl_tests import snapshot_sources
from board_rtl_cases import CASES
OBSERVER='''`timescale 1ns/1ps
module board_metadata_observer;
 initial begin
  wait(tb.dut.record_done && tb.dut.ready);
  #9;
  $display("OBSERVED metadata length=%0d origin=%0d position=%0d",tb.dut.record_length,tb.dut.origin,tb.dut.position);
 end
endmodule
'''

def run():
    originals=json.loads((ROOT/'evidence/board-rtl-review-test-run.json').read_text())
    wanted=[c for c in CASES if c['id'] in ['legacy-base','legacy-interleave']]
    rows=[]
    with tempfile.TemporaryDirectory(prefix='sds-board-metadata-') as tmp:
        snapshot=Path(tmp)/'sources';hashes=snapshot_sources(SOURCE,snapshot,[dict(c,sources=c['sources']+['fpga/default/lanemap_seed.vh']) for c in wanted])
        assert all(originals['runtimeInputSHA256'][p]==h for p,h in hashes.items())
        (snapshot/'observer.v').write_text(OBSERVER)
        def execute(c):
            binary=str(Path(tmp)/c['id']);command=['iverilog','-g2012','-s',c['top'],'-s','board_metadata_observer','-I','fpga/default','-o',binary]+['-D'+v for v in c['defines']]+[c['test'],*c['sources'],'observer.v']
            compiled=subprocess.run(command,cwd=snapshot,capture_output=True,text=True,timeout=180)
            sim=subprocess.run(['vvp',binary],cwd=snapshot,capture_output=True,text=True,timeout=900) if compiled.returncode==0 else None
            return dict(id=c['id'],compileCommand=command,compileExitCode=compiled.returncode,compileOutput=compiled.stdout+compiled.stderr,simulationCommand=['vvp',binary],simulationExitCode=sim.returncode if sim else None,simulationOutput=sim.stdout+sim.stderr if sim else '')
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:rows=list(pool.map(execute,wanted))
        assert all(hashlib.sha256((SOURCE/p).read_bytes()).hexdigest()==h for p,h in hashes.items())
    report=dict(commit=REV,scope='Supplementary read-only observation9ns after first record_done && ready. Original bench assertions and all instrument source bytes remain unchanged. This does not replace original results or establish the source of the mismatch.',observerSource=OBSERVER,observerSHA256=hashlib.sha256(OBSERVER.encode()).hexdigest(),runtimeInputSHA256=hashes,cases=rows)
    (ROOT/'evidence/board-metadata-observation.json').write_text(json.dumps(report,indent=2)+'\n')
    for row in rows:print(row['id'],row['simulationOutput'],flush=True)
if __name__=='__main__':run()
