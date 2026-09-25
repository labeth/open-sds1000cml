#!/usr/bin/env python3
"""REQ-SDS-018: record dependency gating, never protocol qualification."""
import hashlib
import json
import os
import shutil
import subprocess
from inventory import ROOT, SOURCE, REV, git

sha = lambda data: hashlib.sha256(data).hexdigest()
files = sorted((SOURCE / 'app/internal/decode').glob('oracle*_test.go'))
assert len(files) == 7
for path in files:
    rel = str(path.relative_to(SOURCE))
    baseline = git('show', REV + ':' + rel)
    if path.read_bytes() != baseline:
        overlays = {r['path']:r for r in json.loads((ROOT/'evidence/annotation-overlay.json').read_text())['files']}
        row = overlays[rel]
        assert row['baselineSHA256'] == sha(baseline) and row['annotatedSHA256'] == sha(path.read_bytes()) and row['exactBaselineRecovery']
assert shutil.which('sigrok-cli') is None, 'Dependency now available: run actual protocol comparisons instead.'
def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in sorted((SOURCE/'app/internal/decode').glob('*.go'))}
before = snapshot()
names = ['TestOracle' + p for p in ['UART','SPI','I2C','CAN','USBLS','FlexRay']]
cmd = ['go','test','-race','-json','-count=1','-timeout=30s','./internal/decode','-run','^TestOracle(UART|SPI|I2C|CAN|USBLS|FlexRay)$']
runs = []
for required, action, code in [('0','skip',0),('1','fail',1)]:
    env = {'GOPROXY':'off','CI_REQUIRE_SIGROK':required}
    p = subprocess.run(cmd,cwd=SOURCE/'app',env={**os.environ,**env},capture_output=True,timeout=60)
    events = [json.loads(line) for line in p.stdout.splitlines()]
    terminal = {e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
    assert terminal == dict.fromkeys(names,action), terminal
    assert p.returncode == code, p.stderr.decode()
    output = ''.join(e.get('Output','') for e in events)
    message = 'CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed' if required=='1' else 'sigrok-cli not installed; skipping oracle cross-check'
    assert output.count(message) == 6
    log = ROOT/'evidence/test-runs'/('decode-oracle-all-required-'+required+'.jsonl')
    log.write_bytes(p.stdout)
    runs.append(dict(environment=env,exitCode=p.returncode,tests=terminal,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(p.stdout)))
assert before == snapshot()
report = dict(commit=REV,requirement='REQ-SDS-018',result='external-comparisons-not-run',command=cmd,runtimeInputSHA256=before,availabilityChecks=runs,scope='Six optional skips and six required-dependency failures before protocol subtests. No passing protocol evidence; required-mode failures must not be indexed as functional decoder failures.')
(ROOT/'evidence/decode-oracle-all-availability.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: all six oracle suites skip optionally and fail on required missing dependency; no protocol subtests executed')
