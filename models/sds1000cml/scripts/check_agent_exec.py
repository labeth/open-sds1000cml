#!/usr/bin/env python3
"""Reproduce execution-helper limits with test-owned short-lived processes."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    files = sorted((SOURCE / 'ota/internal/agent').glob('*.go'))
    snapshot = lambda: {str(p.relative_to(SOURCE)): hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    before = snapshot()
    target = SOURCE / 'ota/internal/agent/rpc_test.go'
    probe = (ROOT / 'scripts/helpers/agent-exec-boundaries.go.txt').read_text()
    env = {k: v for k, v in os.environ.items() if not k.startswith('OTA_')}
    env['GOPROXY'] = 'off'
    with tempfile.TemporaryDirectory(prefix='sds-agent-exec-') as directory:
        temp = Path(directory)
        replacement = temp / 'rpc_test.go'
        text = target.read_text().replace('import (', 'import (\n "time"', 1)
        replacement.write_text(text + '\n' + probe)
        overlay = temp / 'overlay.json'
        overlay.write_text(json.dumps({'Replace': {str(target): str(replacement)}}))
        command = ['go', 'test', '-race', '-overlay', str(overlay), '-run',
                   '^TestModelReviewAgentExecBoundaries$', '-timeout', '15s', '-count=1', '-v', './internal/agent']
        result = subprocess.run(command, cwd=SOURCE / 'ota', env=env,
                                capture_output=True, text=True, timeout=120)
    assert before == snapshot(), 'instrument source changed during characterization'
    report = dict(commit=REV, sourceSHA256=before, probe=probe, command=command,
                  workingDirectory='ota', exitCode=result.returncode, stdout=result.stdout,
                  stderr=result.stderr, result='known-limitations-reproduced' if result.returncode == 0 else 'failed',
                  scope='Temporary test fixture; a missing executable, printf/exit shell, and one two-second sleep descendant only. No device, network, factory launch, reboot, retained-descriptor or external process operation. Elapsed timing characterizes this run, not a latency guarantee.')
    (ROOT / 'evidence/agent-exec-characterization.json').write_text(json.dumps(report, indent=2)+'\n')
    assert result.returncode == 0, result.stdout + result.stderr
    print(result.stdout)


if __name__ == '__main__':
    run()
