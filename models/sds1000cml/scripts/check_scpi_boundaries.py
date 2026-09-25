#!/usr/bin/env python3
"""Characterize handler limits with a test overlay; never edit instrument source."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    files=sorted((SOURCE/'app/internal/scpi').glob('*.go'))+sorted((SOURCE/'app/internal/analog').glob('*.go'))
    before={str(p.relative_to(SOURCE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in files}
    probe=(ROOT/'scripts/helpers/scpi-boundaries.go.txt').read_text()
    target=SOURCE/'app/internal/scpi/scpi_test.go'
    with tempfile.TemporaryDirectory(prefix='sds-scpi-characterization-') as directory:
        temp=Path(directory)
        replacement=temp/'scpi_test.go'
        replacement.write_bytes(target.read_bytes()+b'\n'+probe.encode())
        overlay=temp/'overlay.json'
        overlay.write_text(json.dumps({'Replace':{str(target):str(replacement)}}))
        command=['go','test','-overlay',str(overlay),'-run','^TestModelReviewSCPIBoundaries$','-count=1','-v','./internal/scpi']
        outcome=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,text=True,timeout=120)
    assert before=={str(p.relative_to(SOURCE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in files}, 'source changed during characterization'
    report=dict(commit=REV,scope='Test-only overlay on the reviewed source. Passing assertions reproduce existing limitations; they do not certify requirement satisfaction or repair behavior.',sourceSHA256=before,probe=probe,command=command,workingDirectory='app',exitCode=outcome.returncode,stdout=outcome.stdout,stderr=outcome.stderr,result='known-limitations-reproduced' if outcome.returncode==0 else 'characterization-failed')
    (ROOT/'evidence/scpi-boundary-characterization.json').write_text(json.dumps(report,indent=2)+'\n')
    assert outcome.returncode==0,outcome.stdout+outcome.stderr
    print(report['result'])


if __name__ == '__main__':
    run()
