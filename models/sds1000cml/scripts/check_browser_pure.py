#!/usr/bin/env python3
"""Run the reviewed binary-frame/FFT Node suites through their original Go runners."""
import hashlib
import json
import os
import shlex
import subprocess
from inventory import ROOT, SOURCE, REV

paths = ['app/internal/web/' + name for name in (
    'binframe.js', 'binframe.test.cjs', 'binframe_node_test.go',
    'peaks.js', 'peaks.test.cjs', 'peaks_node_test.go')]
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
before = {p: sha(SOURCE / p) for p in paths}
command = ['go', 'test', '-json', '-count=1', './internal/web', '-run', '^(TestBinframeJS|TestPeaksJS)$']
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, 'GOPROXY': 'off', 'CI_REQUIRE_BROWSER': '1'}, capture_output=True)
log = ROOT / 'evidence/test-runs/browser-pure.jsonl'
log.write_bytes(run.stdout)
assert run.returncode == 0, run.stderr.decode()
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = {e['Test']: e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')}
assert terminal == {'TestBinframeJS': 'pass', 'TestPeaksJS': 'pass'}, terminal
helper = ROOT / 'scripts/helpers/browser-pure-boundaries.cjs'
boundary = json.loads(subprocess.check_output(['node', str(helper), str(SOURCE / 'app/internal/web')]))
assert boundary['result'] == 'pass' and len(boundary['cases']) == 9
assert before == {p: sha(SOURCE / p) for p in paths}, 'source changed during test execution'
report = dict(commit=REV, result='pass', sourceSHA256=before, helperSHA256=sha(helper),
              nodeVersion=subprocess.check_output(['node', '--version'], text=True).strip(),
              originalTests=terminal, boundaryCharacterization=boundary,
              scope='Original Node assertions via two Go runners and nine additional deterministic boundary cases. No skipped tests, browser, HTTP server, device access or physical qualification.')
(ROOT / 'evidence/browser-pure-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
index = ROOT / 'evidence/test-runs/results.json'
rows = [r for r in json.loads(index.read_text()) if r['id'] != 'browser-pure']
rows.append(dict(id='browser-pure', repository='open-sds1000cml-acq2', commit=REV, workingDirectory='app',
                 command='CI_REQUIRE_BROWSER=1 GOPROXY=off ' + shlex.join(command), exitCode=0, result='pass',
                 log=log.name, sha256=sha(log), scope=report['scope']))
index.write_text(json.dumps(rows, indent=2) + '\n')
print('PASS: two original Go-to-Node test runners (no skips) and nine browser math/transport boundary cases')
