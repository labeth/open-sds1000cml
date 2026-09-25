#!/usr/bin/env python3
"""Check non-supervisor agent CLI paths; never invoke Run or Probe."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    source = SOURCE / 'ota/cmd/agent/main.go'
    before = hashlib.sha256(source.read_bytes()).hexdigest()
    environment = {k: v for k, v in os.environ.items() if not k.startswith('OTA_')}
    environment['GOPROXY'] = 'off'
    rows = []
    with tempfile.TemporaryDirectory(prefix='sds-agent-cli-') as directory:
        binary = Path(directory) / 'agent'
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/agent'],
                       cwd=SOURCE / 'ota', env=environment, check=True, timeout=120)
        for argument in ['version', '-v', '--version', 'help', '-h', '--help', 'status', 'unknown']:
            result = subprocess.run([str(binary), argument], env=environment,
                                    capture_output=True, text=True, timeout=5)
            expected = 2 if argument in ('status', 'unknown') else 0
            assert result.returncode == expected, (argument, result)
            if expected:
                assert 'unknown subcommand' in result.stderr
            elif argument in ('version', '-v', '--version'):
                assert result.stdout.startswith('open-sds ota agent ')
            else:
                assert 'agent probe [--gpmc]' in result.stdout
                assert 'agent status' not in result.stdout
            rows.append(dict(argument=argument, exitCode=result.returncode,
                             stdout=result.stdout, stderr=result.stderr))
    assert hashlib.sha256(source.read_bytes()).hexdigest() == before
    report = dict(result='pass-with-documentation-discrepancy', commit=REV,
                  source='ota/cmd/agent/main.go', sourceSHA256=before, checks=rows,
                  findings=['The introductory comment advertises status, but the switch rejects it with exit 2 and usage omits it.',
                            'Version and help aliases return before configuration, agent construction, probing or supervisor startup.',
                            'No-argument Run and probe/--gpmc paths were inspected but not executed. Probe ignores JSON encoder failure; extra flags other than --gpmc are not validated.',
                            'The supervisor path registers SIGINT/SIGTERM and invokes Stop from one signal receiver goroutine. This inspection does not prove graceful worker completion or watchdog behavior.'],
                  scope='Eight explicit version/help/refusal CLI cases only. No device access, supervisor, probe, signal handling, factory process or network operation exercised.')
    (ROOT / 'evidence/agent-cli-review.json').write_text(json.dumps(report, indent=2) + '\n')
    print('PASS: eight safe agent CLI cases; advertised status command is unsupported')


if __name__ == '__main__':
    run()
