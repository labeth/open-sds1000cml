#!/usr/bin/env python3
"""Apply reviewed Go trace comments; prove exact baseline recovery and token identity."""
import argparse
import hashlib
import json
import subprocess
import tempfile
from pathlib import Path

import yaml
from inventory import ROOT, SOURCE, REV, git


def digest(data):
    return hashlib.sha256(data).hexdigest()


def run(apply=False):
    plan = json.loads((ROOT / 'evidence/annotation-plan.json').read_text())
    requirements = {r['id'] for r in yaml.safe_load((ROOT / 'model/requirements.yml').read_text())['requirements']}
    owners = json.loads((ROOT / 'evidence/source-units.json').read_text())
    overlay_path = ROOT / 'evidence/annotation-overlay.json'
    previous = {r['path']: r for r in json.loads(overlay_path.read_text())['files']} if overlay_path.exists() else {}
    report = []
    with tempfile.TemporaryDirectory(prefix='sds-annotations-') as tmp:
        helper = Path(tmp) / 'symbols.go'
        helper.write_bytes((ROOT / 'scripts/helpers/go-symbols.go.txt').read_bytes())

        def inspect(path):
            result = subprocess.run(['go', 'run', str(helper)], input=json.dumps([str(path)]),
                                    text=True, capture_output=True, check=True)
            return json.loads(result.stdout)[0]

        pending = []
        for row in plan['files']:
            path = row['path']
            assert path in owners[row['owner']], f'owner mismatch: {path}'
            original = git('show', f'{REV}:{path}')
            baseline = Path(tmp) / 'baseline.go'
            baseline.write_bytes(original)
            symbols = inspect(baseline)
            found = {}
            for fn in symbols['functions'] or []:
                found.setdefault(fn['name'], []).append(fn)
                if fn.get('key', fn['name']) != fn['name']:
                    found.setdefault(fn['key'], []).append(fn)
                else:
                    found.setdefault('package.' + fn['name'], []).append(fn)
            inserts = [(symbols['packageOffset'], f"// ENGMODEL-OWNER-UNIT: {row['owner']}\n")]
            for name, refs in row['functions'].items():
                assert refs and set(refs) <= requirements, f'unknown requirement: {name}'
                assert len(found.get(name, [])) == 1, f'ambiguous or absent declaration: {path}:{name}'
                inserts.append((found[name][0]['offset'], '// TRLC-LINKS: ' + ', '.join(refs) + '\n'))
            types = {decl['name']: decl for decl in symbols.get('types') or []}
            for name, refs in row.get('types', {}).items():
                assert refs and set(refs) <= requirements, f'unknown type requirement: {name}'
                assert name in types, f'absent type declaration: {path}:{name}'
                inserts.append((types[name]['offset'], '// TRLC-LINKS: ' + ', '.join(refs) + '\n'))
            expected = original
            for offset, comment in sorted(inserts, reverse=True):
                expected = expected[:offset] + comment.encode() + expected[offset:]
            # Remove only the inserted bytes at their known offsets, never arbitrary comments.
            recovered = expected
            shift = 0
            positions = []
            for offset, comment in sorted(inserts):
                positions.append((offset + shift, comment.encode()))
                shift += len(comment.encode())
            for offset, comment in reversed(positions):
                assert recovered[offset:offset+len(comment)] == comment
                recovered = recovered[:offset] + recovered[offset+len(comment):]
            assert recovered == original, f'baseline recovery failed: {path}'
            candidate = Path(tmp) / 'candidate.go'
            candidate.write_bytes(expected)
            after = inspect(candidate)
            assert after['tokenHash'] == symbols['tokenHash'], f'Go tokens changed: {path}'
            target = SOURCE / path
            current = target.read_bytes()
            old = previous.get(path, {})
            reviewed_overlay = (old.get('baselineSHA256') == digest(original)
                                and old.get('annotatedSHA256') == digest(current)
                                and old.get('tokenSHA256') == symbols['tokenHash'])
            assert current == expected or (apply and (current == original or reviewed_overlay)), f'unexpected worktree changes: {path}'
            pending.append((target, expected))
            report.append(dict(path=path, baselineSHA256=digest(original), annotatedSHA256=digest(expected),
                               tokenSHA256=after['tokenHash'], declarations=len(row['functions']),
                               totalDeclarations=len(symbols['functions'] or []),
                               typeDeclarations=len(row.get('types', {})),
                               totalTypeDeclarations=len(symbols.get('types') or []), exactBaselineRecovery=True))
        # All files have been checked before any instrument source is changed.
        if apply:
            for target, expected in pending:
                target.write_bytes(expected)
        for target, expected in pending:
            assert target.read_bytes() == expected, f'annotation mismatch: {target}'
    result = dict(commit=REV, scope=plan['scope'], files=report,
                  linkedDeclarations=sum(r['declarations'] for r in report),
                  linkedTypeDeclarations=sum(r['typeDeclarations'] for r in report), result='pass')
    (ROOT / 'evidence/annotation-overlay.json').write_text(json.dumps(result, indent=2) + '\n')
    print(json.dumps(result, indent=2))
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    run(parser.parse_args().apply)
