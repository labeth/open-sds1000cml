"""Bound the testbench scheduling experiment separately from controller tests."""
import hashlib,json,re
from inventory import ROOT,SOURCE,REV
from rtl_source_provenance import validate
from check_timeslice_experiment import CASES


def expected():
    evidence=ROOT/'evidence';report_path=evidence/'timeslice-experiment-test-run.json';report=json.loads(report_path.read_text())
    assert report['commit']==REV and report['status']=='complete' and len(report['cases'])==1
    spec=CASES[0];case=report['cases'][0]
    assert all(case[k]==v for k,v in spec.items())
    paths=[spec['test'],*spec['sources']]
    assert set(report['runtimeInputSHA256'])==set(paths)
    for path,sha in report['runtimeInputSHA256'].items():validate(path,sha)
    command=case['compileCommand'];assert command[:5]==['iverilog','-g2012','-s','tb','-o'] and command[6:]==paths
    assert case['simulationCommand']==['vvp',command[5]] and case['sourceSnapshot']
    output=case['simulationOutput'];passed=case['compileExitCode']==0 and case['simulationExitCode']==0 and not case['timedOut'] and 'FATAL' not in output and 'FAIL' not in output and any(l.startswith('PASS timeslice:') for l in output.splitlines())
    if passed:
        rounds=re.findall(r'^round=(\d+) excursion_clocks=(\d+) pending=(\d+) backlog=(\d+)$',output,re.M)
        assert [int(x[0]) for x in rounds]==list(range(16))
        assert all(int(x[1])<=524288+256 and int(x[2])<=4608 for x in rounds)
        totals=re.findall(r'^PASS timeslice: read=(\d+) written=(\d+) max_ingress=(\d+)/(\d+)$',output,re.M)
        assert len(totals)==1
        read,written,maximum,capacity=map(int,totals[0]);assert read==81920 and written>0 and maximum<=capacity==4608
    result='pass' if passed else ('not-run' if case['compileExitCode'] else 'fail')
    assert case['result']==('pass' if passed else 'fail')
    assert report['result']==('pass' if passed else 'fail')
    path=spec['test'];plan=next(x for x in json.loads((evidence/'rtl-annotation-plan.json').read_text())['files'] if x['path']==path);refs=plan['modules']['tb']
    link=dict(path=path,module='tb',run=case['id'],requirements=refs,result=result,log=report_path.name)
    native=dict(source=path,scope=report['scope'],sourceSHA256=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest(),reportSHA256=hashlib.sha256(report_path.read_bytes()).hexdigest(),runs=[case['id']],results=[dict(requirement=req,status='partial' if passed else result,notes='Original testbench scheduler exercises full19-bit geometry,16 rounds and a2.5-million-cycle host stall with actual ingress/transport and ideal SRAM. Assertions cover order, markers, physical writes, read batches and excursion bounds. This is not a production-controller execution or physical timing qualification.') for req in refs])
    return [link],{'tb_sram_timeslice.json':native}
