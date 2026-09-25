#!/usr/bin/env python3
"""Execute the complete reviewed diag test batch without native trace credit."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
review = json.loads((evidence / 'diag-source-review.json').read_text())
for path, expected in review['sourceSHA256'].items():
    assert sha((SOURCE / path).read_bytes()) == expected, path

def snapshot():
    paths = sorted((SOURCE / 'app').rglob('*.go'))
    paths += [SOURCE / 'app/go.mod', SOURCE / 'app/internal/bus/dcinv/dcinv.ko']
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in paths}

before = snapshot()
names = review['uniqueReviewedTests']
assert len(names) == len(set(names)) == 14
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
           './internal/diag', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, 'GOPROXY': 'off'},
                     capture_output=True, timeout=150)
log = evidence / 'test-runs/diag-reviewed.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
parents = {name: status for name, status in terminal.items() if '/' not in name}
unchanged = snapshot() == before
passed = run.returncode == 0 and parents == dict.fromkeys(names, 'pass') and unchanged
report = dict(commit=REV, result='pass' if passed else 'fail', exitCode=run.returncode,
              runtimeInputSHA256=before, command=command, environment={'GOPROXY': 'off'},
              tests=terminal, topLevelTests=parents, inputsUnchanged=unchanged,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              stderr=run.stderr.decode(), nativeEligibleTests=[], scope=review['scope'])
(evidence / 'diag-review-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
assert passed, run.stdout.decode()[-12000:] + run.stderr.decode()
print(f'PASS: {len(parents)} reviewed tests and {len(terminal)-len(parents)} subtests; race detector enabled; unchanged inputs; native trace credit pending')
