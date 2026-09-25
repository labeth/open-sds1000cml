#!/usr/bin/env python3
"""Run only the reviewed localhost NATS test with isolated OTA settings."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    paths = sorted((SOURCE / 'ota/internal/agent').glob('*.go'))
    paths += sorted((SOURCE / 'ota/internal/otactl').glob('*.go'))
    hashes = lambda: {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest() for p in paths}
    before = hashes()
    env = {k: v for k, v in os.environ.items() if not k.startswith('OTA_')}
    env['GOPROXY'] = 'off'
    command = ['go', 'test', '-race', '-json', '-run',
               '^TestNATSEndToEndSubjectsEventsAndTCPParity$', '-count=1', './internal/otactl']
    with tempfile.TemporaryDirectory(prefix='sds-nats-isolation-') as directory:
        env.update(OTA_AUTO_TAKEOVER='0', OTA_WD_DEV=directory+'/watchdog',
                   OTA_GPMC=directory+'/Gpmc', OTA_FPGA_KEY=directory+'/fpga_key')
        Path(env['OTA_WD_DEV']).write_bytes(b'')
        result = subprocess.run(command, cwd=SOURCE/'ota', env=env, capture_output=True,
                                text=True, timeout=120)
    assert hashes() == before, 'source changed during broker test'
    evidence = ROOT/'evidence'
    log = evidence/'test-runs/agent-nats-isolated.jsonl'
    log.write_text(result.stdout)
    report = dict(commit=REV, sourceSHA256=before, command=command, exitCode=result.returncode,
                  stderr=result.stderr, log='test-runs/'+log.name,
                  scope='Inherited OTA_* removed; coexist forced; device paths redirected to temporary directory. Embedded broker and TCP listener bind localhost. Only deterministic printf command executed. No external broker or device qualification.')
    (evidence/'agent-nats-isolated-run.json').write_text(json.dumps(report, indent=2)+'\n')
    assert result.returncode == 0, result.stdout[-4000:] + result.stderr
    events = [json.loads(line) for line in result.stdout.splitlines()]
    assert events[-1]['Action'] == 'pass'
    assert sum(e['Action'] == 'pass' and 'Test' in e for e in events) == 1
    index = evidence/'test-runs/results.json'
    rows = [r for r in json.loads(index.read_text()) if r['id'] != 'agent-nats-isolated']
    rows.append(dict(id='agent-nats-isolated', repository='open-sds1000cml-acq2', commit=REV,
                     workingDirectory='ota', command='python3 models/sds1000cml/scripts/check_agent_nats.py (isolated go test command in agent-nats-isolated-run.json)',
                     exitCode=0, result='pass', log=log.name,
                     sha256=hashlib.sha256(log.read_bytes()).hexdigest(), scope=report['scope']))
    index.write_text(json.dumps(rows, indent=2)+'\n')
    print('PASS: isolated NATS end-to-end test; source unchanged and log indexed')


if __name__ == '__main__':
    run()
