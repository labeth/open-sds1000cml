#!/usr/bin/env python3
"""Run one real Icarus snapshot case without replacing the full RTL report."""
import hashlib
import json
from pathlib import Path
import tempfile
from unittest.mock import patch
import run_rtl_tests as runner


def check():
    model = runner.ROOT
    with tempfile.TemporaryDirectory(prefix='sds-snapshot-smoke-') as tmp:
        isolated = Path(tmp)
        evidence = isolated / 'evidence/test-runs'
        evidence.mkdir(parents=True)
        (evidence / 'results.json').write_text('[]')
        cases = [c for c in runner.CASES if c['id'] == 'ordinal-counter']
        assert len(cases) == 1
        with patch.object(runner, 'ROOT', isolated), patch.object(runner, 'CASES', cases):
            runner.run()
        data = (evidence / 'rtl-reviewed.json').read_bytes()
        report = json.loads(data)
        assert report['result'] == 'pass' and report['cases'][0]['sourceSnapshot']
    target = model / 'evidence/test-runs/rtl-snapshot-smoke.json'
    target.write_bytes(data)
    index = model / 'evidence/test-runs/results.json'
    rows = [r for r in json.loads(index.read_text()) if r['id'] != 'rtl-snapshot-smoke']
    rows.append(dict(id='rtl-snapshot-smoke', repository='open-sds1000cml-acq2', commit=report['commit'],
                     workingDirectory='.', command='python3 models/sds1000cml/scripts/check_rtl_snapshot.py',
                     exitCode=0, result='pass', log=target.name, sha256=hashlib.sha256(data).hexdigest(),
                     scope='Actual Icarus smoke test of frozen-source compilation; separate from the full reviewed RTL batch.'))
    index.write_text(json.dumps(rows, indent=2)+'\n')
    print('PASS: real Icarus snapshot smoke test; full RTL evidence unchanged')


if __name__ == '__main__':
    check()
