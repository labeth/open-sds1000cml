#!/usr/bin/env python3
"""Execute the complete reviewed diag test batch with native trace indexing."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
review = json.loads((evidence / 'diag-annotation-batch.json').read_text())
overlays = {r['path']: r for r in json.loads((evidence / 'annotation-overlay.json').read_text())['files']}
for path, expected in review['sourceSHA256'].items():
    current = sha((SOURCE / path).read_bytes())
    overlay = overlays[path]
    assert overlay['baselineSHA256'] == expected and overlay['annotatedSHA256'] == current and overlay['exactBaselineRecovery'], path

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
log = evidence / 'test-runs/diag-integrated.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
parents = {name: status for name, status in terminal.items() if '/' not in name}
unchanged = snapshot() == before
expected = dict.fromkeys(names, 'pass')
expected['TestGpmcSweepPersistApply'] = 'fail'
output = ''.join(e.get('Output', '') for e in events)
characterized = run.returncode == 1 and parents == expected and unchanged and 'WARNING: DATA RACE' in output
passed = run.returncode == 0
report = dict(commit=REV, result='pass' if passed else 'fail', exitCode=run.returncode,
              runtimeInputSHA256=before, command=command, environment={'GOPROXY': 'off'},
              tests=terminal, topLevelTests=parents, inputsUnchanged=unchanged,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              stderr=run.stderr.decode(), nativeEligibleTests=names, scope=review['scope'])
(evidence / 'diag-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
assert characterized, run.stdout.decode()[-12000:] + run.stderr.decode()
index = evidence / 'test-runs/results.json'
rows = [row for row in json.loads(index.read_text()) if row['id'] != 'diag']
rows.append(dict(id='diag', repository='open-sds1000cml-acq2', commit=REV, workingDirectory='app', command=command, environment={'GOPROXY':'off'}, exitCode=run.returncode, result='fail', log=log.name, sha256=sha(run.stdout), scope=review['scope']))
index.write_text(json.dumps(rows, indent=2) + '\n')
print('CHARACTERIZED:13 passes,1 failing sweep test with race reports; unchanged inputs; exact outcomes indexed')
