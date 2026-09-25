"""Join the reviewed precision case matrix to module links without crediting missing simulations."""
import hashlib,json
from pathlib import Path
from inventory import ROOT
from check_precision_rtl_evidence import check

def expected():
    evidence=ROOT/'evidence'
    read=lambda name:json.loads((evidence/name).read_text())
    batch=read('precision-rtl-annotation-batch.json')
    plan={x['path']:x for x in read('rtl-annotation-plan.json')['files']}
    if not any(x['path'] in plan for x in batch['files']):return [],{}
    assert all(x['path'] in plan for x in batch['files']), 'partly integrated precision batch'
    checked=check();report=read('precision-rtl-review-test-run.json');actual={x['id']:x for x in report['cases']}
    links=[];native={}
    for outcome in checked['cases']:
        case=actual[outcome['id']];path=case['test'];refs=plan[path]['modules'][case['top']]
        result='pass' if outcome['status']=='partial' else outcome['status']
        links.append(dict(path=path,module=case['top'],run=case['id'],requirements=refs,result=result,log='precision-rtl-review-test-run.json'))
        key=Path(path).stem+'.json'
        item=native.setdefault(key,dict(source=path,scope=report['scope'],sourceSHA256=hashlib.sha256((ROOT.parents[1]/path).read_bytes()).hexdigest(),reportSHA256=checked['reportSHA256'],runs=[],results=[]))
        item['runs'].append(case['id'])
    for item in native.values():
        related=[x for x in links if x['path']==item['source']]
        for req in sorted({q for x in related for q in x['requirements']}):
            outcomes=[x['result'] for x in related if req in x['requirements']]
            status='fail' if 'fail' in outcomes else ('partial' if 'pass' in outcomes else 'not-run')
            item['results'].append(dict(requirement=req,status=status,notes=f'{outcomes.count("pass")} original precision cases pass; {outcomes.count("fail")} executed cases fail; {outcomes.count("not-run")} could not execute because compilation failed. Passing cases provide partial assertion evidence only. Frozen pre-annotation source inputs remain auditable through exact approved comment overlays; no physical qualification is established. See evidence/precision-rtl-review-test-run.json.'))
    return links,native
