"""Accept only exact pinned JavaScript bytes or the approved comment overlay."""
import hashlib
import json
from inventory import ROOT, SOURCE, REV, git


def validate(path, expected):
    current = (SOURCE / path).read_bytes()
    digest = lambda data: hashlib.sha256(data).hexdigest()
    if digest(current) == expected:
        return False
    evidence = ROOT / 'evidence'
    overlay = json.loads((evidence / 'javascript-annotation-overlay.json').read_text())
    row = next(r for r in overlay['files'] if r['path'] == path)
    assert row['baselineSHA256'] == expected and row['annotatedSHA256'] == digest(current)
    assert row['exactBaselineRecovery']
    plan = next(r for r in json.loads((evidence / 'javascript-annotation-plan.json').read_text())['files'] if r['path'] == path)
    baseline = git('show', f'{REV}:{path}')
    assert digest(baseline) == expected
    lines = baseline.splitlines(keepends=True)
    for decl in sorted(plan['declarations'], key=lambda d: d['line'], reverse=True):
        line = lines[decl['line'] - 1]
        assert line.decode().strip() == decl['sourceLine']
        indent = line[:len(line) - len(line.lstrip())]
        lines.insert(decl['line'] - 1, indent + ('// TRLC-LINKS: ' + ', '.join(decl['requirements']) + '\n').encode())
    annotated = ('// ENGMODEL-OWNER-UNIT: ' + plan['owner'] + '\n').encode() + b''.join(lines)
    assert current == annotated, 'unapproved JavaScript source change: ' + path
    return True
