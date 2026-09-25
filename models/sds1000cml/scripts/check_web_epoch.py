#!/usr/bin/env python3
"""Characterize HTTP epoch policy in memory; no listener, engine or device."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
paths=['app/internal/web/web.go','app/internal/web/web_test.go','app/internal/web/web_binframe.go','app/internal/web/app_init.js','app/internal/web/app_core.js','app/cmd/app/main.go']
hashes=lambda:{f:hashlib.sha256((SOURCE/f).read_bytes()).hexdigest() for f in paths}
before=hashes();target=SOURCE/'app/internal/web/web_test.go';helper=ROOT/'scripts/helpers/web-epoch-boundaries.go.txt'
env={k:v for k,v in os.environ.items() if not k.startswith(('OTA_','SCOPE_'))};env['GOPROXY']='off'
with tempfile.TemporaryDirectory(prefix='sds-web-epoch-') as directory:
 temp=Path(directory);replacement=temp/'web_test.go';replacement.write_bytes(target.read_bytes()+b'\n'+helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(target):str(replacement)}}));command=['go','test','-race','-overlay',str(overlay),'-run','^TestModelReviewWebEpochPolicy$','-count=1','-v','./internal/web'];result=subprocess.run(command,cwd=SOURCE/'app',env=env,capture_output=True,text=True,timeout=120)
assert hashes()==before
report=dict(commit=REV,sourceSHA256=before,helperSHA256=hashlib.sha256(helper.read_bytes()).hexdigest(),command=command,exitCode=result.returncode,stdout=result.stdout,stderr=result.stderr,result='known-limitations-reproduced' if result.returncode==0 else 'failed',scope='In-memory HTTP recorder and zero-value Server; no engine, bus, browser, listener, device or network operation. Only claim registration and superseded parsing are exercised. Full transport/authentication qualification is not implied.')
(ROOT/'evidence/web-epoch-characterization.json').write_text(json.dumps(report,indent=2)+'\n');assert result.returncode==0,result.stdout+result.stderr;print('PASS: in-memory claim and epoch policy characterization')
