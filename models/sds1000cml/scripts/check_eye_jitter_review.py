#!/usr/bin/env python3
"""Preserve original eye/jitter suite outcomes from a frozen pinned snapshot."""
# ENGMODEL-OWNER-UNIT: FU-APP-WEB
# TRLC-LINKS: REQ-SDS-181
import hashlib
import json
import subprocess
import tempfile
import time
from pathlib import Path

from inventory import ROOT, REV, git


def sha(data):
    return hashlib.sha256(data).hexdigest()


def main():
    names = ('app_eye.js', 'eyejitter.js', 'eyejitter_analysis.js', 'eyejitter.test.cjs',
             'eyejitter_breaker.cjs')
    sources = {name: git('show', f'{REV}:app/internal/web/{name}') for name in names}
    destination = ROOT / 'evidence/test-runs'
    cases = []
    with tempfile.TemporaryDirectory(prefix='sds-eye-review-') as directory:
        work = Path(directory)
        for name, data in sources.items():
            (work / name).write_bytes(data)
        commands = [
            ('original-suite', ['node', 'eyejitter.test.cjs']),
            ('breaker-50', ['node', 'eyejitter_breaker.cjs', '0', '50', '--json', 'families.json']),
        ]
        for label, command in commands:
            start = time.monotonic()
            run = subprocess.run(command, cwd=work, capture_output=True, timeout=300)
            log = destination / f'eye-jitter-{label}.log'
            log.write_bytes(run.stdout + b'\n--- stderr ---\n' + run.stderr)
            cases.append(dict(id=label, command=command, exitCode=run.returncode,
                              elapsedSeconds=round(time.monotonic() - start, 3),
                              log=str(log.relative_to(ROOT)), logSHA256=sha(log.read_bytes())))
        families = json.loads((work / 'families.json').read_text())
        assert len(families) == 50 and [row['fam'] for row in families] == list(range(50))
        assert all(isinstance(row['fails'], list) for row in families)
        helper = ROOT / 'scripts/helpers/eye-jitter-guards.cjs'
        guards = json.loads(subprocess.check_output(
            ['node', str(helper), str(work / 'app_eye.js')], cwd=work, timeout=30))
        assert guards['result'] == 'pass' and len(guards['cases']) == 11
        assert sources == {name: (work / name).read_bytes() for name in names}
    suite_log = (ROOT / cases[0]['log']).read_text()
    cases[0]['result'] = 'pass' if cases[0]['exitCode'] == 0 and 'ALL PASS' in suite_log else 'fail'
    failures = [row for row in families if row['fails']]
    cases[1]['result'] = 'pass' if cases[1]['exitCode'] == 0 and not failures else 'fail'
    report = dict(
        commit=REV, status='executed-integration-pending',
        result='pass' if all(case['result'] == 'pass' for case in cases) else 'fail',
        sourceSHA256={f'app/internal/web/{name}': sha(data) for name, data in sources.items()},
        runnerSHA256=sha(Path(__file__).read_bytes()),
        nodeVersion=subprocess.check_output(['node', '--version'], text=True).strip(),
        cases=cases, families=families, failedFamilies=len(failures),
        guardCharacterization=guards, helperSHA256=sha(helper.read_bytes()),
        scope='Unmodified pinned Node suite and all 50 seeded adversarial families; frozen local files, no browser or device operations.',
        limitations=[
            'Guard characterization stubs the numerical feed and rendering; does not exercise browser rendering, fetch loop or autoset.',
            'Synthetic fixtures do not establish calibrated instrument accuracy or BER extrapolation.',
            'Breaker eye height is reported but not asserted; original clean height check is only a lower bound.',
            'Breaker tone checks are conditional on a reported spectral peak; absent spectra may escape them.',
            'Breaker includes a host-dependent 4000 ms per-family runtime threshold.',
            'Source links and native publication integration remain pending; passing these suites does not close REQ-SDS-067.',
        ])
    output = ROOT / 'evidence/eye-jitter-review-test-run.json'
    output.write_text(json.dumps(report, indent=2) + '\n')
    print(f"Recorded {cases[0]['result']} original suite; {len(families)} breaker families, {len(failures)} failed; 11 guard checks passed")
    for row in failures:
        print(row['fam'], row['name'], row['fails'])


if __name__ == '__main__':
    main()
