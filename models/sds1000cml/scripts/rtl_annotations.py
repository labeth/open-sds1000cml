#!/usr/bin/env python3
"""Apply reviewed module comments, preserving exact original bytes and lexical tokens."""
import argparse
import hashlib
import json
import re
import yaml
from inventory import ROOT, SOURCE, REV, git

TOKEN = re.compile(rb'//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\])*"|\\[^\s]+|[A-Za-z_$][A-Za-z0-9_$]*|[^\s]', re.S)


def tokens(data):
    return [(m.group(), m.start()) for m in TOKEN.finditer(data)
            if not m.group().startswith((b'//', b'/*'))]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def token_hash(data):
    return digest(json.dumps([t.decode() for t, _ in tokens(data)]).encode())


def annotate(original, owner, modules, requirements):
    lexemes = tokens(original)
    declarations = {}
    for i, (token, offset) in enumerate(lexemes):
        if token not in (b'module', b'macromodule'):
            continue
        j = i + 1
        if lexemes[j][0] in (b'automatic', b'static'):
            j += 1
        declarations.setdefault(lexemes[j][0].decode(), []).append(offset)
    assert set(declarations) == set(modules), 'review plan must cover every literal module'
    inserts = [(0, f'// ENGMODEL-OWNER-UNIT: {owner}\n'.encode())]
    for name, links in modules.items():
        assert links and set(links) <= requirements, f'unknown requirement: {name}'
        assert len(declarations[name]) == 1, f'ambiguous module: {name}'
        offset = declarations[name][0]
        assert original[original.rfind(b'\n', 0, offset)+1:offset].strip() == b'', 'module must begin its own line'
        inserts.append((offset, ('// TRLC-LINKS: ' + ', '.join(links) + '\n').encode()))
    # Preserve insertion order for a module at byte zero (owner precedes its link).
    expected = original
    for _, (offset, comment) in sorted(enumerate(inserts), key=lambda x: (x[1][0], x[0]), reverse=True):
        expected = expected[:offset] + comment + expected[offset:]
    shift, positions = 0, []
    for _, (offset, comment) in sorted(enumerate(inserts), key=lambda x: (x[1][0], x[0])):
        positions.append((offset + shift, comment)); shift += len(comment)
    recovered = expected
    for offset, comment in reversed(positions):
        assert recovered[offset:offset+len(comment)] == comment
        recovered = recovered[:offset] + recovered[offset+len(comment):]
    assert recovered == original, 'exact baseline recovery failed'
    assert token_hash(original) == token_hash(expected), 'Verilog lexical tokens changed'
    return expected


def run(apply=False):
    plan = json.loads((ROOT / 'evidence/rtl-annotation-plan.json').read_text())
    requirements = {r['id'] for r in yaml.safe_load((ROOT / 'model/requirements.yml').read_text())['requirements']}
    owners = json.loads((ROOT / 'evidence/source-units.json').read_text())
    overlay = ROOT / 'evidence/rtl-annotation-overlay.json'
    previous = {r['path']: r for r in json.loads(overlay.read_text())['files']} if overlay.exists() else {}
    pending, rows = [], []
    for row in plan['files']:
        path = row['path']
        assert path.endswith('.v') and path in owners[row['owner']], f'owner mismatch: {path}'
        original = git('show', f'{REV}:{path}')
        expected = annotate(original, row['owner'], row['modules'], requirements)
        current = (SOURCE / path).read_bytes()
        old = previous.get(path, {})
        prior = (old.get('baselineSHA256') == digest(original) and old.get('annotatedSHA256') == digest(current)
                 and old.get('tokenSHA256') == token_hash(original))
        assert current == expected or (apply and (current == original or prior)), f'unexpected worktree changes: {path}'
        pending.append((SOURCE / path, expected))
        rows.append(dict(path=path, baselineSHA256=digest(original), annotatedSHA256=digest(expected),
                         tokenSHA256=token_hash(expected), modules=len(row['modules']), exactBaselineRecovery=True))
    if apply:
        for target, expected in pending:
            target.write_bytes(expected)
    for target, expected in pending:
        assert target.read_bytes() == expected
    report = dict(commit=REV, scope='Reviewed literal module links. Exact insertion removal and lexical token comparison prove comment-only edits; no elaboration or hardware proof.',
                  files=rows, linkedModules=sum(r['modules'] for r in rows), result='pass')
    overlay.write_text(json.dumps(report, indent=2) + '\n')
    print(f"PASS: {len(rows)} RTL files, {report['linkedModules']} module links, exact baseline recovery and unchanged tokens")
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    run(parser.parse_args().apply)
