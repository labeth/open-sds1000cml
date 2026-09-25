#!/usr/bin/env python3
"""Audit only the pinned inventory's JavaScript, excluding local build outputs."""
import collections
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from inventory import ROOT, SOURCE, REV


def audit():
    inventory = json.loads((ROOT / 'evidence/source-inventory.json').read_text())
    paths = sorted(r['path'] for r in inventory
                   if r['path'].endswith(('.js', '.mjs', '.cjs')) and r['category'] != 'historical-validation')
    hashes = {}
    with tempfile.TemporaryDirectory(prefix='sds-javascript-audit-') as tmp:
        staging = Path(tmp) / 'source'
        for rel in paths:
            data = (SOURCE / rel).read_bytes()
            hashes[rel] = hashlib.sha256(data).hexdigest()
            target = staging / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
        helper = Path(tmp) / 'scan.go'
        helper.write_bytes((ROOT / 'scripts/helpers/verilog-scan.go.txt').read_bytes())
        tool_dir = os.environ.get('ENGINEERING_MODEL_GO_DIR', str(ROOT.parents[2] / 'engineering-model-go'))
        process = subprocess.run(['go', 'run', str(helper), str(staging)], cwd=tool_dir,
                                 env={**os.environ, 'GOPROXY': 'off'}, capture_output=True, text=True, check=True)
        result = json.loads(process.stdout)
        result['diagnostics'] = result.get('diagnostics') or []
        for diagnostic in result['diagnostics']:
            diagnostic['path'] = diagnostic['path'].removeprefix(str(staging) + '/')
    for symbol in result['symbols']:
        symbol['role'] = 'test' if '/e2e/' in symbol['Path'] or '.test.' in symbol['Path'] or '.spec.' in symbol['Path'] else 'implementation'
    report = dict(commit=REV, scope='All pinned nonhistorical .js/.mjs/.cjs paths, irrespective of inventory category, read from current source and hashed below. Named declarations and direct function bindings only; anonymous callbacks and top-level statements are excluded. No browser execution or behavioral verification.',
                  result='incomplete' if result['diagnostics'] else 'pass', files=paths, sha256=hashes,
                  counts=dict(collections.Counter(d['code'] for d in result['diagnostics'])), **result)
    (ROOT / 'evidence/javascript-trace-audit.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f"{report['result']}: {len(paths)} JavaScript files; {report['counts']}")
    return report


if __name__ == '__main__':
    audit()
