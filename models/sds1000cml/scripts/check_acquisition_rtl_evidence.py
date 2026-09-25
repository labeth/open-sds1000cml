#!/usr/bin/env python3
"""Validate exact acquisition simulation inputs and distinguish build from assertion failures."""
import hashlib,json
from pathlib import Path
from inventory import ROOT,SOURCE,REV

def classify(case):
    if case['compileExitCode'] != 0:
        assert case['simulationExitCode'] is None and not case['simulationOutput'] and not case['timedOut']
        return 'not-run'
    assert case['simulationExitCode'] is not None, 'compiled case has no simulation outcome'
    if case['timedOut']:
        assert case['simulationExitCode'] == 124
        return 'fail'
    passed=case['simulationExitCode']==0 and any(line.startswith(case['success']) for line in case['simulationOutput'].splitlines())
    return 'partial' if passed else 'fail'

def check():
    evidence=ROOT/'evidence'
    read=lambda name:json.loads((evidence/name).read_text())
    matrix=read('acquisition-rtl-case-matrix.json')['cases']
    report=read('acquisition-rtl-review-test-run.json')
    assert report['status']=='complete' and report['commit']==REV, 'campaign incomplete or wrong revision'
    runs={r['id']:r for r in report['cases']}
    assert len(runs)==len(report['cases'])==len(matrix)==40
    assert set(runs)=={c['id'] for c in matrix}
    expected_sources={p for c in matrix for p in [c['test'],*c['sources'],'fpga/default/lanemap_seed.vh']}
    assert set(report['runtimeInputSHA256'])==expected_sources
    overlay={r['path']:r for r in read('rtl-annotation-overlay.json')['files']}
    equivalent=[]
    for path,expected in report['runtimeInputSHA256'].items():
        current=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()
        if current==expected:continue
        row=overlay.get(path,{})
        assert row.get('baselineSHA256')==expected and row.get('annotatedSHA256')==current and row.get('exactBaselineRecovery'), 'changed simulation input: '+path
        # Independently recreate the approved comment overlay from the pinned Git object.
        from rtl_annotations import annotate,digest
        from inventory import git
        import yaml
        plan=next(x for x in read('rtl-annotation-plan.json')['files'] if x['path']==path)
        requirements={x['id'] for x in yaml.safe_load((ROOT/'model/requirements.yml').read_text())['requirements']}
        original=git('show',f'{REV}:{path}')
        assert digest(original)==expected
        candidate=annotate(original,plan['owner'],plan['modules'],requirements)
        assert candidate==(SOURCE/path).read_bytes()
        equivalent.append(path)
    results=[]
    for expected in matrix:
        case=runs[expected['id']]
        assert all(case[k]==v for k,v in expected.items()), 'case changed: '+expected['id']
        command=case['compileCommand']
        assert command[:8]==['iverilog','-g2012','-DSIM','-I','fpga/default','-s',case['top'],'-o']
        assert command[9:]==[f'-P{case["top"]}.{k}={v}' for k,v in case['parameters'].items()]+[case['test'],*case['sources']]
        assert case['simulationCommand']==['vvp',command[8]] and case['sourceSnapshot']
        status=classify(case)
        assert case['result']==('pass' if status=='partial' else 'fail')
        results.append(dict(id=case['id'],path=case['test'],module=case['top'],status=status,compileExitCode=case['compileExitCode'],simulationExitCode=case['simulationExitCode'],timedOut=case['timedOut']))
    assert report['result']==('pass' if all(c['result']=='pass' for c in report['cases']) else 'fail')
    output=dict(result='pass',cases=results,annotationEquivalentSources=equivalent,reportSHA256=hashlib.sha256((evidence/'acquisition-rtl-review-test-run.json').read_bytes()).hexdigest(),scope='Exact original simulation case/input provenance. Compilation failures are not-run behavioral checks; executed assertion failures remain fail. Passing simulation assertions contribute partial evidence only. Post-run changes are allowed solely when exact pinned-baseline reconstruction proves the approved comment-only overlay.')
    (evidence/'acquisition-rtl-evidence-check.json').write_text(json.dumps(output,indent=2)+'\n')
    print('PASS: 40 exact acquisition cases; '+str({s:sum(r['status']==s for r in results) for s in ['partial','fail','not-run']}))
    return output

if __name__=='__main__':check()
