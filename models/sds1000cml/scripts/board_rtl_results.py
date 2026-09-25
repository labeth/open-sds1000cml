"""Attach bounded board cases, retaining failed metadata and vendor provenance."""
import hashlib,json
from pathlib import Path
from inventory import ROOT,SOURCE
from check_board_rtl_evidence import check

def expected():
    e=ROOT/'evidence';read=lambda n:json.loads((e/n).read_text());batch=read('board-rtl-annotation-batch.json')
    plan={x['path']:x for x in read('rtl-annotation-plan.json')['files']}
    if not any(x['path'] in plan for x in batch['files']):return [],{}
    assert all(x['path'] in plan for x in batch['files']),'partial board integration'
    checked=check();report=read('board-rtl-review-test-run.json');runs={x['id']:x for x in report['cases']};links=[];native={}
    for c in checked['cases']:
        path=c['path'];refs=list(plan[path]['modules'][c['module']])
        # Auto/normal capture variants do not execute the optional recall block.
        if path.endswith('tb_top_adc.v') and '+recall' not in runs[c['id']]['plusargs']:
            refs=[x for x in refs if x!='REQ-SDS-043']
        result='pass' if c['status']=='partial' else c['status']
        links.append(dict(path=path,module=c['module'],run=c['id'],requirements=refs,result=result,log=c['log']))
        key=Path(path).stem+'.json'
        item=native.setdefault(key,dict(source=path,scope='Original offline board/profile digital cases with explicit compile flags,PLL/ADC/DDR mocks or vendor libraries. Process/bench failures remain fail; assertions not reached by an earlier failure are not credited. Physical qualification is not established.',sourceSHA256=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest(),evidenceReports=checked['reportSHA256'],runs=[],results=[]))
        item['runs'].append(c['id'])
    for item in native.values():
        related=[x for x in links if x['path']==item['source']]
        for req in sorted({q for x in related for q in x['requirements']}):
            outcomes=[x['result'] for x in related if req in x['requirements']]
            status='fail' if 'fail' in outcomes else ('partial' if 'pass' in outcomes else 'not-run')
            item['results'].append(dict(requirement=req,status=status,notes=f'{outcomes.count("pass")} cases pass;{outcomes.count("fail")} cases fail;{outcomes.count("not-run")} could not compile. Capture-metadata assertions terminate two legacy benches before later checks. Continuation requires37 externally verified values. See exact variant and vendor provenance in board-rtl-evidence-check.json; no physical qualification.'))
    return links,native
