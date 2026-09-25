#!/usr/bin/env python3
"""Run the completely reviewed web API test batch against host fakes."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
review = json.loads((evidence / 'webapi-source-review.json').read_text())
batch = json.loads((evidence / 'webapi-annotation-batch.json').read_text())
overlays = {row['path']: row for row in json.loads((evidence / 'annotation-overlay.json').read_text())['files']}
for path, digest in review['sourceSHA256'].items():
    current = sha((SOURCE / path).read_bytes())
    if current != digest:
        overlay = overlays[path]
        assert overlay['baselineSHA256'] == digest and overlay['annotatedSHA256'] == current
        assert overlay['exactBaselineRecovery']

def snapshot():
    inventory = json.loads((evidence / 'source-inventory.json').read_text())
    return {row['path']: sha((SOURCE / row['path']).read_bytes()) for row in inventory
            if row['path'].startswith('app/')}

before = snapshot()
names = review['uniqueReviewedTests']
assert len(names) == len(set(names)) == 34
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
           './internal/web', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, 'GOPROXY': 'off'},
                     capture_output=True, timeout=150)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
top = {name: status for name, status in terminal.items() if '/' not in name}
log = evidence / 'test-runs/webapi-integrated.jsonl'
log.write_bytes(run.stdout)
report = dict(commit=REV, result='pass' if run.returncode == 0 else 'fail',
              exitCode=run.returncode, runtimeInputSHA256=before, command=command,
              environment={'GOPROXY': 'off'}, tests=terminal, topLevelTests=top,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              nativeEligibleTests=batch['nativeEligibleTests'], scope=review['scope'])
(evidence / 'webapi-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
assert snapshot() == before, 'Inputs changed during execution'
assert top == dict.fromkeys(names, 'pass') and run.returncode == 0, run.stdout.decode()[-15000:] + run.stderr.decode()
assert all(v == 'pass' for v in terminal.values()), terminal
index = evidence / 'test-runs/results.json'
rows = [row for row in json.loads(index.read_text()) if row['id'] != 'webapi']
rows.append(dict(id='webapi', repository='open-sds1000cml-acq2', commit=REV,
                 workingDirectory='app', command=command, environment={'GOPROXY': 'off'},
                 exitCode=run.returncode, result=report['result'], log=log.name,
                 sha256=sha(run.stdout), scope=review['scope']))
index.write_text(json.dumps(rows, indent=2) + '\n')
print(f'PASS: {len(top)} original tests and {len(terminal)-len(top)} subtests; unchanged app inputs')
