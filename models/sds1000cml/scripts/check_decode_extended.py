#!/usr/bin/env python3
"""Re-run the reviewed extended decoder tests without claiming exhaustive parity."""
from pathlib import Path
import hashlib
import json
import os
import re
import subprocess
from inventory import ROOT, SOURCE, REV

sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
review_path = ROOT / 'evidence/decode-extended-source-review.json'
review = json.loads(review_path.read_text())
overlays = {r['path']: r for r in json.loads((ROOT / 'evidence/annotation-overlay.json').read_text())['files']}
for path, digest in list(review['sourceSHA256'].items()):
    current = sha(SOURCE / path)
    if current != digest:
        overlay = overlays[path]
        assert digest == overlay['baselineSHA256'] and current == overlay['annotatedSHA256'], path
        review.setdefault('preannotationSHA256', {})[path] = digest
        review['sourceSHA256'][path] = current
    assert current == review['sourceSHA256'][path], 'Reviewed source changed: ' + path
inputs = sorted(list((SOURCE / 'app/internal/decode').glob('*.go')) +
                list((SOURCE / 'app/internal/web').glob('decode*.js')) +
                [SOURCE / 'app/internal/web/decparity_check.mjs', SOURCE / 'app/internal/testenv/testenv.go'])
def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p) for p in inputs}
before = snapshot()
names = sorted(n for p in review['completeReads'] if p.endswith('_test.go')
               for n in re.findall(r'^func (Test\w+)\(', (SOURCE / p).read_text(), re.M))
assert len(names) == 7
settings = {'GOPROXY': 'off', 'CI_REQUIRE_BROWSER': '1', 'DECODE_FUZZ_MULT': '1', 'DECODE_FUZZ_SEED': '0'}
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=240s', './internal/decode', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, **settings}, capture_output=True, timeout=270)
log = ROOT / 'evidence/test-runs/decode-extended-exploratory.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = [(e['Test'], e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')]
assert run.returncode == 0, run.stdout.decode() + run.stderr.decode()
assert sorted((n, a) for n, a in terminal if '/' not in n) == [(n, 'pass') for n in names]
assert all(a == 'pass' for _, a in terminal)
assert before == snapshot(), 'Inputs changed during execution'
output = ''.join(e.get('Output', '') for e in events)
match = re.search(r'ALL PARITY OK: (\d+)/(\d+) vectors', output)
assert match and match[1] == match[2]
review.update(runtimeInputSHA256=before, tests=dict(terminal), command=command, environment=settings,
              log=str(log.relative_to(ROOT / 'evidence')), logSHA256=sha(log), parityVectors=int(match[1]),
              result='reviewed-host-tests-pass-native-partial-evidence', nextAction='Review remaining oracle sources; all ten breaker files are reviewed and integrated. JavaScript implementations are reviewed separately in javascript-decode-source-review.json.')
review_path.write_text(json.dumps(review, indent=2) + '\n')
print(f'PASS: seven original tests, {len(terminal)} terminal outcomes, {match[1]} Node parity vectors; unchanged inputs')

index = ROOT / 'evidence/test-runs/results.json'
rows = [r for r in json.loads(index.read_text()) if r['id'] != 'decode-extended']
rows.append(dict(id='decode-extended', repository='open-sds1000cml-acq2', commit=REV,
                 workingDirectory='app', command=command, environment=settings, exitCode=0,
                 result='pass', log=log.name, sha256=sha(log),
                 scope='Seven reviewed original tests under race detector: two fixed-seed ordinary robustness tests (1500 sampled cases), four literal-vector tests, and 286 Node parity vectors. No Go fuzz target, external-publication attribution, browser UI or hardware qualification.'))
index.write_text(json.dumps(rows, indent=2) + '\n')
