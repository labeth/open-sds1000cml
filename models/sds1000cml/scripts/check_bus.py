#!/usr/bin/env python3
"""Run the source-reviewed bus batch; index only applied traces."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
batch = json.loads((evidence / 'bus-annotation-batch.json').read_text())
overlays = {row['path']: row for row in json.loads((evidence / 'annotation-overlay.json').read_text())['files']}
for path, baseline in batch['sourceSHA256'].items():
    current = sha((SOURCE / path).read_bytes())
    if current != baseline:
        overlay = overlays[path]
        assert overlay['baselineSHA256'] == baseline
        assert overlay['annotatedSHA256'] == current and overlay['exactBaselineRecovery']

def snapshot():
    paths = sorted((SOURCE / 'app/internal/bus').glob('*.go'))
    paths += sorted((SOURCE / 'app/internal/iface').glob('*.go'))
    paths += [SOURCE / 'app/internal/bus/dcinv/dcinv.ko', SOURCE / 'app/go.mod']
    return {str(path.relative_to(SOURCE)): sha(path.read_bytes()) for path in paths}

before = snapshot()
names = batch['uniqueReviewedTests']
assert len(names) == len(set(names)) == 33
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
           './internal/bus', '-run', '^(' + '|'.join(names) + ')$']
environment = {'GOPROXY': 'off'}
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, **environment},
                     capture_output=True, timeout=150)
assert run.returncode == 0, run.stdout.decode() + run.stderr.decode()
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {event['Test']: event['Action'] for event in events
            if event.get('Test') and event['Action'] in ('pass', 'fail', 'skip')}
assert terminal == dict.fromkeys(names, 'pass') and snapshot() == before
log = evidence / 'test-runs/bus-reviewed.jsonl'
log.write_bytes(run.stdout)
eligible = batch['nativeEligibleTests'] if batch['status'] == 'integrated' else []
report = dict(commit=REV, result='pass', runtimeInputSHA256=before,
              command=command, environment=environment, tests=terminal,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              nativeEligibleTests=eligible, scope=batch['scope'])
(evidence / 'bus-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
if eligible:
    index = evidence / 'test-runs/results.json'
    rows = [row for row in json.loads(index.read_text()) if row['id'] != 'bus']
    rows.append(dict(id='bus', repository='open-sds1000cml-acq2',
                     commit=REV, workingDirectory='app', command=command,
                     environment=environment, exitCode=0, result='pass',
                     log=log.name, sha256=sha(run.stdout), scope=batch['scope']))
    index.write_text(json.dumps(rows, indent=2) + '\n')
print(f'PASS: {len(terminal)} original tests; {len(eligible)} indexed outcomes; unchanged input snapshot')
