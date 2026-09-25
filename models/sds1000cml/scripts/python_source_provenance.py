"""Check exact current or reconstructed pinned Python inputs without execution."""
import json
import yaml
from inventory import ROOT,SOURCE,REV,git
from python_annotations import annotate,digest


def validate(path,expected):
    current=(SOURCE/path).read_bytes()
    if digest(current)==expected:return False
    original=git('show',f'{REV}:{path}');assert digest(original)==expected,'input is not pinned Python baseline'
    plan=json.loads((ROOT/'evidence/python-annotation-plan.json').read_text())['files']
    row=next(r for r in plan if r['path']==path)
    requirements={r['id'] for r in yaml.safe_load((ROOT/'model/requirements.yml').read_text())['requirements']}
    assert annotate(original,row,requirements)==current,'unapproved Python source overlay'
    return True
