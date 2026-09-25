#!/usr/bin/env python3
"""Preserve original codegen test outcomes and all generation inputs."""
import hashlib
import json
import os
import shutil
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
review = json.loads((evidence / 'codegen-source-review.json').read_text())
for path, digest in review['sourceSHA256'].items():
    assert sha((SOURCE / path).read_bytes()) == digest, path

def snapshot():
    paths = set((SOURCE / 'codegen').rglob('*.go'))
    paths.update(SOURCE / p for p in ['codegen/go.mod', 'codegen/Makefile', 'codegen/README.md',
                  'fpga/default/regs.vh', 'fpga/default/regmux.vh',
                  'app/internal/iface/iface.go', 'fpga/default/docs/REGISTER-MAP.md'])
    for pattern in ['fpga/common/*.v', 'fpga/default/*.v', 'fpga/default/*.sdc',
                    'fpga/default/*.qsf', 'fpga/default/lanemap_seed.vh']:
        paths.update(SOURCE.glob(pattern))
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in sorted(paths)}

before = snapshot()
assert shutil.which('iverilog') and shutil.which('vvp'), 'Original simulator tests must execute'
names = review['uniqueReviewedTests']
assert len(names) == len(set(names)) == 37
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s', './...']
run = subprocess.run(command, cwd=SOURCE / 'codegen', env={**os.environ, 'GOPROXY': 'off'},
                     capture_output=True, timeout=150)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
log = evidence / 'test-runs/codegen-reviewed.jsonl'
log.write_bytes(run.stdout)
report = dict(commit=REV, result='pass' if run.returncode == 0 else 'fail',
              exitCode=run.returncode, runtimeInputSHA256=before, command=command,
              environment={'GOPROXY': 'off'}, tests=terminal,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              nativeEligibleTests=[], scope=review['scope'])
(evidence / 'codegen-review-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
assert snapshot() == before, 'Generation/test inputs changed during execution'
expected = dict.fromkeys(names, 'pass')
expected['TestNoDrift'] = 'fail'
assert run.returncode == 1 and terminal == expected, run.stdout.decode() + run.stderr.decode()
assert b'generated artifacts are stale' in run.stdout
assert b'REGMUX PASS' in run.stdout
print('CHARACTERIZED: 36 original tests pass, TestNoDrift fails; simulator ran; all inputs unchanged')
