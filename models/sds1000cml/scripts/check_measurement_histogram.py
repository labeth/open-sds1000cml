#!/usr/bin/env python3
"""Reproduce the pinned measurement discrepancy without editing instrument source."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, REV, git

PROBE = 'package measure\nimport "testing"\nfunc TestReviewQ8HistogramParity(t *testing.T) {\n raw:=make([]uint8,800); q:=make([]uint16,800)\n for i:=range raw { raw[i]=56; if i%100>=50 {raw[i]=200}; if i%100==50 {raw[i]=230}; q[i]=uint16(raw[i])*256 }\n a,b:=Compute(raw,1,0,1e-6),ComputeQ8(q,1,0,1e-6)\n t.Logf("raw top=%v base=%v overshoot=%v; Q8 top=%v base=%v overshoot=%v",a.Vtop,a.Vbase,a.Overshoot,b.Vtop,b.Vbase,b.Overshoot)\n if a.Vtop!=72 || b.Vtop!=102 || a.Overshoot<=0 || b.Overshoot!=0 { t.Fatalf("unexpected characterization: raw=%+v q=%+v",a,b) }\n}\n'


def run():
    source = 'app/internal/measure/measure.go'
    data = git('show', f'{REV}:{source}')
    with tempfile.TemporaryDirectory(prefix='sds-measure-review-') as directory:
        root = Path(directory)
        (root/'go.mod').write_text('module measurement-review\n\ngo 1.24\n')
        (root/'measure.go').write_bytes(data)
        (root/'review_test.go').write_text(PROBE)
        command = ['go', 'test', '-v', '-count=1', './...']
        outcome = subprocess.run(command, cwd=root, env={**os.environ, 'GOWORK':'off', 'GOPROXY':'off'}, capture_output=True, text=True, timeout=60)
    report = dict(scope='Isolated pinned-source characterization. A passing probe confirms the discrepancy, not requirement satisfaction or a fix.',
        commit=REV, source=source, sourceKind='pinned Git blob, without annotation comments', sourceSHA256=hashlib.sha256(data).hexdigest(),
        probe=PROBE, command=command, environment=dict(GOWORK='off', GOPROXY='off'), exitCode=outcome.returncode,
        stdout=outcome.stdout, stderr=outcome.stderr, result='known-discrepancy-reproduced' if outcome.returncode==0 else 'characterization-failed',
        finding='modeInRange caps hi at 255 for Q8 histograms. Equivalent records yield raw overshoot 20.833333333333336 percent and Q8 overshoot zero.')
    (ROOT/'evidence/measurement-histogram-characterization.json').write_text(json.dumps(report,indent=2)+'\n')
    assert outcome.returncode==0, outcome.stdout+outcome.stderr
    print(report['result'])


if __name__ == '__main__':
    run()
