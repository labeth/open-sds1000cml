"""Validate original full-depth SRAM tester cases and retain bounded test credit."""
import hashlib,json,re
from inventory import ROOT,SOURCE,REV
from rtl_source_provenance import validate


def expected():
    evidence=ROOT/'evidence';report_path=evidence/'sram-bench-rtl-review-test-run.json'
    report=json.loads(report_path.read_text())
    sources=['fpga/sram_bench/sim/tb.v','fpga/sram_bench/bench.v','fpga/common/gpmc_slave.v']
    assert report['commit']==REV and report['status']=='complete'
    assert set(report['runtimeInputSHA256'])==set(sources)
    for path,sha in report['runtimeInputSHA256'].items():validate(path,sha)
    command=report['compileCommand']
    assert command[:6]==['iverilog','-g2012','-DBENCH_MHZ=10','-s','tb','-o'] and command[7:]==sources
    assert len(report['cases'])==2 and {c['alias'] for c in report['cases']}=={0,1}
    plan={x['path']:x for x in json.loads((evidence/'rtl-annotation-plan.json').read_text())['files']}
    path=sources[0];refs=plan[path]['modules']['tb'];links=[]
    for case in report['cases']:
        alias=case['alias'];assert case['id']==('sram-bench-alias' if alias else 'sram-bench-normal')
        assert case['simulationCommand']==['vvp',command[6]]+(['+alias'] if alias else [])
        out=case['output'];passed=report['compileExitCode']==0 and case['simulationExitCode']==0 and f'PASS behavioral SRAM, alias test={alias}' in out.splitlines() and 'FATAL' not in out and 'FAIL' not in out
        if passed:
            matches=re.findall(r'^writes=(\d+) reads=(\d+) checked=(\d+) errors=(\d+) first=(\d+) want=([0-9a-f]+) got=([0-9a-f]+)$',out,re.M)
            assert len(matches)==1,'missing or ambiguous full-depth scoreboard'
            writes,reads,checked,errors=map(int,matches[0][:4])
            assert (writes,reads,checked)==(524304,524320,524288)
            assert errors>0 if alias else errors==0
        outcome='pass' if passed else ('not-run' if report['compileExitCode'] else 'fail')
        assert outcome==case['result']
        links.append(dict(path=path,module='tb',run=case['id'],requirements=refs,result=outcome,log=report_path.name))
    status='fail' if any(x['result']=='fail' for x in links) else ('partial' if any(x['result']=='pass' for x in links) else 'not-run')
    assert report['result']==('pass' if all(x['result']=='pass' for x in links) else 'fail')
    native=dict(source=path,scope=report['scope'],sourceSHA256=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest(),reportSHA256=hashlib.sha256(report_path.read_bytes()).hexdigest(),runs=[x['run'] for x in links],results=[dict(requirement=req,status=status,notes='Original normal-memory and address-bit18 alias cases use a fixed 10 MHz ideal PLL/DDR/SRAM configuration. Full-depth comparison and error presence are asserted; host register access, first-error/bit-mask correctness, all latency settings and repeated runs are not verified. See evidence/sram-bench-rtl-review-test-run.json.') for req in refs])
    return links,{'tb.json':native}
