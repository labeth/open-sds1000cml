#!/usr/bin/env python3
"""Reproduce baseline upload failed-commit retention and lazy eviction using temporary files and a test overlay."""
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
    target = SOURCE / 'ota/internal/agent/rpc_test.go'
    probe = (ROOT / 'scripts/helpers/agent-upload-boundaries.go.txt').read_text()
    with tempfile.TemporaryDirectory(prefix='sds-agent-upload-') as directory:
        temp = Path(directory)
        replacement = temp / 'rpc_test.go'
        replacement.write_bytes(target.read_bytes() + b'\n' + probe.encode())
        overlay = temp / 'overlay.json'
        overlay.write_text(json.dumps({'Replace': {str(target): str(replacement)}}))
        command = ['go', 'test', '-overlay', str(overlay), '-run', '^TestModelReviewAgentUploadBoundaries$',
                   '-count=1', '-v', './internal/agent']
        outcome = subprocess.run(command, cwd=SOURCE / 'ota', env={**os.environ, 'GOPROXY': 'off'},
                                 capture_output=True, text=True, timeout=120)
    assert before == {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    report = dict(commit=REV, sourceSHA256=before, probe=probe, command=command, workingDirectory='ota',
                  exitCode=outcome.returncode, stdout=outcome.stdout, stderr=outcome.stderr,
                  result='known-limitations-reproduced' if outcome.returncode == 0 else 'characterization-failed',
                  scope='Temporary agent fixture and destination files only, no Run call. Checks failed-commit session retention, capacity rejection side effects and lazy eviction with simulated session age. Sequential execution does not test concurrent eviction. No device, process kill or network operation occurs.')
    (ROOT / 'evidence/agent-upload-characterization.json').write_text(json.dumps(report, indent=2) + '\n')
    assert outcome.returncode == 0, outcome.stdout + outcome.stderr
    print(report['result'])


if __name__ == '__main__':
    run()
