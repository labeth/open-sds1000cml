#!/usr/bin/env python3
"""Audit only the pinned inventory's RTL, excluding local build outputs."""
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
                   if r['path'].endswith('.v') and r['category'] in ('implementation', 'test'))
    hashes = {}
    with tempfile.TemporaryDirectory(prefix='sds-verilog-audit-') as tmp:
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
        for diagnostic in result['diagnostics']:
            diagnostic['path'] = diagnostic['path'].removeprefix(str(staging) + '/')
    categories = {r['path']: r['category'] for r in inventory}
    for symbol in result['symbols']:
        name = Path(symbol['Path']).name
        bench = name == 'tb.v' or name.startswith('tb_') or name.endswith('_tb.v')
        symbol['role'] = 'test' if bench else ('test-support' if categories[symbol['Path']] == 'test' else 'implementation')
    report = dict(commit=REV, scope='Pinned inventory implementation/test Verilog paths, read from current source and hashed below. Lexical module scan only; no preprocessing, elaboration or behavioral verification.',
                  result='incomplete' if result['diagnostics'] else 'pass', files=paths, sha256=hashes,
                  counts=dict(collections.Counter(d['code'] for d in result['diagnostics'])), **result)
    (ROOT / 'evidence/verilog-trace-audit.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f"{report['result']}: {len(paths)} Verilog files; {report['counts']}")
    return report


if __name__ == '__main__':
    audit()
