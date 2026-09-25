#!/usr/bin/env python3
"""Validate exact precision simulation inputs and distinguish build from assertion failures."""
import hashlib,json
from pathlib import Path
from inventory import ROOT,SOURCE,REV

from check_acquisition_rtl_evidence import classify

def check():
    evidence=ROOT/'evidence'
    read=lambda name:json.loads((evidence/name).read_text())
    from check_precision_rtl_review import CASES, REFERENCE, REF_HASH
    matrix=CASES
    report=read('precision-rtl-review-test-run.json')
    assert report['status']=='complete' and report['commit']==REV, 'campaign incomplete or wrong revision'
    runs={r['id']:r for r in report['cases']}
    assert len(runs)==len(report['cases'])==len(matrix)==7
    assert set(runs)=={c['id'] for c in matrix}
    expected_sources={p for c in matrix for p in [c['test'],*c['sources'],REFERENCE]}
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
    from inventory import git
    original_reference=git('show',f'{REV}:{REFERENCE}').decode()
    assert hashlib.sha256(original_reference.encode()).hexdigest()==REF_HASH
    derived='module reference_adc_precision'+original_reference.split('module adc_precision',1)[1]
    assert report['derivedReference']['source']==REFERENCE and report['derivedReference']['sourceSHA256']==REF_HASH
    assert report['derivedReference']['sha256']==hashlib.sha256(derived.encode()).hexdigest()
    original_precision=git('show',f'{REV}:fpga/acq_sram/precision.v').decode()
    assert original_reference.split('module adc_precision')[0]==original_precision.split('module adc_precision')[0]
    results=[]
    for expected in matrix:
        case=runs[expected['id']]
        assert all(case[k]==v for k,v in expected.items()), 'case changed: '+expected['id']
        command=case['compileCommand']
        assert command[:5]==['iverilog','-g2012','-s',case['top'],'-o']
        sources=list(case['sources'])
        if case['id'] in ['precision_shared','precision_config']:sources+=['reference.v']
        assert command[6:]==[case['test'],*sources]
        assert case['simulationCommand']==['vvp',command[5]] and case['sourceSnapshot']
        status=classify(case)
        assert case['result']==('pass' if status=='partial' else 'fail')
        results.append(dict(id=case['id'],path=case['test'],module=case['top'],status=status,compileExitCode=case['compileExitCode'],simulationExitCode=case['simulationExitCode'],timedOut=case['timedOut']))
    assert report['result']==('pass' if all(c['result']=='pass' for c in report['cases']) else 'fail')
    output=dict(result='pass',cases=results,annotationEquivalentSources=equivalent,reportSHA256=hashlib.sha256((evidence/'precision-rtl-review-test-run.json').read_bytes()).hexdigest(),scope='Exact original simulation case/input provenance. Compilation failures are not-run behavioral checks; executed assertion failures remain fail. Passing simulation assertions contribute partial evidence only. Post-run changes are allowed solely when exact pinned-baseline reconstruction proves the approved comment-only overlay.')
    (evidence/'precision-rtl-evidence-check.json').write_text(json.dumps(output,indent=2)+'\n')
    print('PASS: 7 exact precision cases; '+str({s:sum(r['status']==s for r in results) for s in ['partial','fail','not-run']}))
    return output

if __name__=='__main__':check()
