#!/usr/bin/env python3
"""Apply reviewed JavaScript line comments, preserving baseline bytes and parser tokens."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import yaml
from inventory import ROOT, SOURCE, REV, git


def digest(data):
    return hashlib.sha256(data).hexdigest()


def run(apply=False):
    plan = json.loads((ROOT / 'evidence/javascript-annotation-plan.json').read_text())
    requirements = {r['id'] for r in yaml.safe_load((ROOT / 'model/requirements.yml').read_text())['requirements']}
    owners = json.loads((ROOT / 'evidence/source-units.json').read_text())
    overlay = ROOT / 'evidence/javascript-annotation-overlay.json'
    previous = {r['path']: r for r in json.loads(overlay.read_text())['files']} if overlay.exists() else {}
    pending, rows = [], []
    with tempfile.TemporaryDirectory(prefix='sds-js-comments-') as tmp:
        helper = Path(tmp) / 'tokens.go'
        helper.write_bytes((ROOT / 'scripts/helpers/javascript-tokens.go.txt').read_bytes())
        binary = Path(tmp) / 'tokens'
        tool_dir = os.environ.get('ENGINEERING_MODEL_GO_DIR', str(ROOT.parents[2] / 'engineering-model-go'))
        subprocess.run(['go', 'build', '-o', str(binary), str(helper)], cwd=tool_dir,
                       env={**os.environ, 'GOPROXY': 'off'}, check=True)

        def inspect(data):
            candidate = Path(tmp) / 'candidate.js'
            candidate.write_bytes(data)
            return json.loads(subprocess.check_output([str(binary), str(candidate)]))

        for row in plan['files']:
            path = row['path']
            assert path.endswith(('.js', '.mjs', '.cjs')) and path in owners[row['owner']], path
            original = git('show', f'{REV}:{path}')
            lines = original.splitlines(keepends=True)
            inserts = [(0, f"// ENGMODEL-OWNER-UNIT: {row['owner']}\n".encode())]
            seen = set()
            for decl in row['declarations']:
                number, refs = decl['line'], decl['requirements']
                assert number not in seen and refs and set(refs) <= requirements, decl
                seen.add(number)
                line = lines[number-1]
                assert line.decode().strip() == decl['sourceLine'], (path, number)
                offset = sum(map(len, lines[:number-1]))
                indent = line[:len(line)-len(line.lstrip())]
                inserts.append((offset, indent + ('// TRLC-LINKS: ' + ', '.join(refs) + '\n').encode()))
            expected = original
            ordered = sorted(enumerate(inserts), key=lambda x: (x[1][0], x[0]))
            for _, (offset, comment) in reversed(ordered):
                expected = expected[:offset] + comment + expected[offset:]
            recovered, shift, positions = expected, 0, []
            for _, (offset, comment) in ordered:
                positions.append((offset + shift, comment)); shift += len(comment)
            for offset, comment in reversed(positions):
                assert recovered[offset:offset+len(comment)] == comment
                recovered = recovered[:offset] + recovered[offset+len(comment):]
            assert recovered == original, f'baseline recovery failed: {path}'
            before, after = inspect(original), inspect(expected)
            assert before == after, f'JavaScript tokens changed: {path}'
            current = (SOURCE / path).read_bytes()
            old = previous.get(path, {})
            prior = (old.get('baselineSHA256') == digest(original)
                     and old.get('annotatedSHA256') == digest(current)
                     and old.get('tokenSHA256') == before['tokenSHA256'])
            assert current == expected or (apply and (current == original or prior)), f'unexpected changes: {path}'
            pending.append((SOURCE / path, expected))
            rows.append(dict(path=path, baselineSHA256=digest(original), annotatedSHA256=digest(expected),
                             **after, declarations=len(row['declarations']), exactBaselineRecovery=True))
        # Check all candidates before writing any instrument source.
        if apply:
            for target, expected in pending: target.write_bytes(expected)
        for target, expected in pending: assert target.read_bytes() == expected, str(target)
    report = dict(commit=REV, files=rows, linkedDeclarations=sum(r['declarations'] for r in rows), result='pass',
                  scope='Reviewed declaration comments only; exact insertion removal recovers the pinned bytes and tree-sitter non-comment terminal tokens are identical. No browser or hardware qualification.')
    overlay.write_text(json.dumps(report, indent=2) + '\n')
    print(f"PASS: {len(rows)} JavaScript files, {report['linkedDeclarations']} linked declarations; exact baseline recovery and unchanged parser tokens")
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true')
    run(parser.parse_args().apply)
