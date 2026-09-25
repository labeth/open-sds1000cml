#!/usr/bin/env python3
"""Characterize the reviewed Quartus host tests before source annotation."""
import hashlib
import json
import os
import subprocess
from pathlib import Path
from inventory import ROOT, SOURCE, REV

sha = lambda data: hashlib.sha256(data).hexdigest()
evidence = ROOT / 'evidence'
review = json.loads((evidence / 'quartus-source-review.json').read_text())
for path, digest in review['sourceSHA256'].items():
    assert sha((SOURCE / path).read_bytes()) == digest, path
optional = [SOURCE / 'fpga/default/out/output_files/default.sta.rpt']
reference = Path('/home/labeth/ws/open-sds1000cml/fpga')
optional += [reference / 'standard/output_files/acq.fit.rpt',
             reference / 'standard/acq.qsf',
             reference / 'adcstrap/output_files/adcstrap.sta.rpt']

def snapshot():
    paths = sorted((SOURCE / 'fpga/internal/quartus').glob('*.go'))
    paths += [SOURCE / 'fpga/go.mod']
    return {'source': {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in paths},
            'optionalReports': {str(p): sha(p.read_bytes()) if p.exists() else None for p in optional}}

before = snapshot()
names = review['uniqueReviewedTests']
assert len(names) == len(set(names)) == 22
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=120s',
           './internal/quartus', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(command, cwd=SOURCE / 'fpga', env={**os.environ, 'GOPROXY': 'off'},
                     capture_output=True, timeout=150)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
log = evidence / 'test-runs/quartus-reviewed.jsonl'
log.write_bytes(run.stdout)
report = dict(commit=REV, result='pass' if run.returncode == 0 else 'fail',
              exitCode=run.returncode, runtimeInputSHA256=before, command=command,
              environment={'GOPROXY': 'off'}, tests=terminal,
              log=str(log.relative_to(evidence)), logSHA256=sha(run.stdout),
              nativeEligibleTests=[], scope=review['scope'])
(evidence / 'quartus-review-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
assert snapshot() == before, 'Inputs changed during execution'
assert run.returncode == 0, run.stdout.decode() + run.stderr.decode()
assert set(terminal) == set(names), terminal
assert all(status == 'pass' or (status == 'skip' and name in
           ('TestRealReports', 'TestRealDefaultSTAReport')) for name, status in terminal.items()), terminal
print(f'PASS: {sum(s == "pass" for s in terminal.values())} tests passed, '
      f'{sum(s == "skip" for s in terminal.values())} optional report tests skipped; unchanged inputs; no native trace credit yet')
