"""Validate immutable RTL input hashes across an approved, exact comment overlay."""
import hashlib,json
from inventory import ROOT,SOURCE,REV,git

def validate(path,expected):
    current=(SOURCE/path).read_bytes()
    digest=lambda b:hashlib.sha256(b).hexdigest()
    if digest(current)==expected:return False
    import yaml
    from rtl_annotations import annotate
    evidence=ROOT/'evidence'
    overlay={x['path']:x for x in json.loads((evidence/'rtl-annotation-overlay.json').read_text())['files']}
    row=overlay.get(path,{})
    assert row.get('annotatedSHA256')==digest(current) and row.get('exactBaselineRecovery'), 'changed RTL input: '+path
    plan=next(x for x in json.loads((evidence/'rtl-annotation-plan.json').read_text())['files'] if x['path']==path)
    original=git('show',f'{REV}:{path}')
    assert digest(original)==row.get('baselineSHA256'),'overlay baseline is not pinned: '+path
    requirements={x['id'] for x in yaml.safe_load((ROOT/'model/requirements.yml').read_text())['requirements']}
    assert annotate(original,plan['owner'],plan['modules'],requirements)==current,'unapproved source overlay: '+path
    if digest(original)!=expected:
        history_path=evidence/'rtl-annotation-history.json'
        history=json.loads(history_path.read_text())['files'] if history_path.exists() else []
        candidates=[r for r in history if r['path']==path and r['owner']==plan['owner']]
        assert any(digest(annotate(original,r['owner'],r['modules'],requirements))==expected for r in candidates), 'input is neither pinned baseline nor exact historical overlay: '+path
    return True
