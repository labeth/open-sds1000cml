#!/usr/bin/env python3
"""Characterize word-size behavior locally without contacting a server."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV


def run():
    target=SOURCE/'app/internal/vxi11srv/server_test.go'
    inputs=sorted((SOURCE/'app/internal/vxi11srv').glob('*.go'))
    sha={str(p.relative_to(SOURCE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    probe=(ROOT/'scripts/helpers/vxi-read-size.go.txt').read_text()
    cases=[]
    with tempfile.TemporaryDirectory(prefix='sds-vxi-read-size-') as directory:
        root=Path(directory);replacement=root/'server_test.go';replacement.write_bytes(target.read_bytes()+b'\n'+probe.encode())
        overlay=root/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(target):str(replacement)}}))
        for architecture in ['amd64','386']:
            command=['go','test','-overlay',str(overlay),'-run','^TestModelReviewReadSizeBoundary$','-count=1','-v','./internal/vxi11srv']
            env={**os.environ,'GOOS':'linux','GOARCH':architecture,'CGO_ENABLED':'0','GOPROXY':'off'}
            p=subprocess.run(command,cwd=SOURCE/'app',env=env,capture_output=True,text=True,timeout=120)
            cases.append(dict(architecture=architecture,command=command,exitCode=p.returncode,stdout=p.stdout,stderr=p.stderr))
    assert sha=={str(p.relative_to(SOURCE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in inputs}
    report=dict(commit=REV,scope='Test-only overlay directly invokes an owned connection with one pending byte. No listener, network request or device is used. 386 execution checks 32-bit integer behavior, not ARM machine-code execution.',sourceSHA256=sha,probe=probe,cases=cases,result='boundary-characterized' if all(c['exitCode']==0 for c in cases) else 'characterization-incomplete')
    (ROOT/'evidence/vxi-read-size-characterization.json').write_text(json.dumps(report,indent=2)+'\n')
    for c in cases:print(c['architecture'],c['exitCode'],c['stdout'])
    assert all(c['exitCode']==0 for c in cases),report


if __name__=='__main__':run()
