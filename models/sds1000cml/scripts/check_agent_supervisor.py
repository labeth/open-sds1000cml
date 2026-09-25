#!/usr/bin/env python3
"""Reproduce supervisor confirmation and start-failure limits using temporary files and a test overlay."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    files = sorted((SOURCE / 'ota/internal/agent').glob('*.go'))
    before = {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    target = SOURCE / 'ota/internal/agent/supervise_test.go'
    probe = (ROOT / 'scripts/helpers/agent-supervisor-boundaries.go.txt').read_text()
    with tempfile.TemporaryDirectory(prefix='sds-agent-supervisor-') as directory:
        temp = Path(directory)
        replacement = temp / 'supervise_test.go'
        replacement.write_bytes(target.read_bytes() + b'\n' + probe.encode())
        overlay = temp / 'overlay.json'
        overlay.write_text(json.dumps({'Replace': {str(target): str(replacement)}}))
        command = ['go', 'test', '-overlay', str(overlay), '-race', '-run', '^TestModelReviewSupervisor',
                   '-count=1', '-v', './internal/agent']
        outcome = subprocess.run(command, cwd=SOURCE / 'ota', env={**{k: v for k, v in os.environ.items() if not k.startswith('OTA_')}, 'GOPROXY': 'off'},
                                 capture_output=True, text=True, timeout=120)
    assert before == {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    report = dict(commit=REV, sourceSHA256=before, probe=probe, command=command, workingDirectory='ota',
                  exitCode=outcome.returncode, stdout=outcome.stdout, stderr=outcome.stderr,
                  result='known-limitations-reproduced' if outcome.returncode == 0 else 'characterization-failed',
                  scope='Temporary slot directories and local short-lived shell fixtures only; no Run or supervisor loop call. Exercises stale-health termination of its own child group, nonzero exit confirmation and missing-executable start failure. No instrument payload, factory process, device or network operation. Characterization is not successful health/rollback qualification.')
    (ROOT / 'evidence/agent-supervisor-characterization.json').write_text(json.dumps(report, indent=2) + '\n')
    assert outcome.returncode == 0, outcome.stdout + outcome.stderr
    print(report['result'])


if __name__ == '__main__':
    run()
