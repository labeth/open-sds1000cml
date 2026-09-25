#!/usr/bin/env python3
"""Freeze and execute the original default RTL suite before trace integration."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
from inventory import ROOT, SOURCE, REV


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def run():
    paths = sorted(str(p.relative_to(SOURCE)) for folder in ['fpga/default', 'fpga/common'] for p in (SOURCE/folder).rglob('*') if p.is_file() and p.suffix in {'.v', '.vh', '.sh'})
    hashes = {path: digest(SOURCE / path) for path in paths}
    names = ['tb_adc_front', 'tb_capture', 'tb_drain', 'tb_diag', 'tb_top']
    report = dict(commit=REV, runtimeInputSHA256=hashes,
                  scope='Original default suite including its common prerequisite benches, SIM behavioral clocks and ideal bus stimulus. Five default outcomes are recorded separately from the previously integrated common cases. No physical CDC, fitted timing, IO placement or hardware qualification.',
                  runtime={}, cases=[])
    for executable in ['iverilog', 'vvp']:
        version = subprocess.run([executable, '-V'], capture_output=True, text=True, check=True)
        report['runtime'][executable] = (version.stdout + version.stderr).splitlines()[0]
    with tempfile.TemporaryDirectory(prefix='sds-default-review-') as temporary:
        snapshot = Path(temporary) / 'sources'
        for path in paths:
            target = snapshot / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes((SOURCE / path).read_bytes())
            target.chmod((SOURCE/path).stat().st_mode & 0o777)
        output = Path(temporary) / 'output'
        output.mkdir()
        command = ['bash', 'fpga/default/sim/run.sh']
        execution = subprocess.run(command, cwd=snapshot, env=dict(os.environ, SIM_OUT=str(output)), capture_output=True, text=True, timeout=600)
        report.update(command=command, exitCode=execution.returncode, stdout=execution.stdout, stderr=execution.stderr)
        for name in names:
            log = output / (name + '.log')
            text = log.read_text() if log.exists() else ''
            passed = ('[PASS] ' + name) in execution.stdout.splitlines() and any(line.startswith('PASS ' + name) for line in text.splitlines()) and 'FAIL' not in text and 'error' not in text
            report['cases'].append(dict(id=name, source='fpga/default/sim/' + name + '.v', result='pass' if passed else 'fail', output=text, sourceSnapshot=True))
        assert all(digest(snapshot / p) == h and digest(SOURCE / p) == h for p, h in hashes.items()), 'Source changed during execution'
    report['status'] = 'complete'
    report['result'] = 'pass' if report['exitCode'] == 0 and all(c['result'] == 'pass' for c in report['cases']) else 'fail'
    target = ROOT / 'evidence/default-rtl-review-test-run.json'
    target.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(dict(result=report['result'], exitCode=report['exitCode'], cases=[dict(id=c['id'], result=c['result']) for c in report['cases']]), indent=2))


if __name__ == '__main__':
    run()
