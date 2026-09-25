#!/usr/bin/env python3
"""Characterize boot-anchor limits using stub scripts in private namespaces."""
import hashlib,json,os,subprocess
from inventory import ROOT,SOURCE,REV

anchor=SOURCE/'ota/boot/startup.sh'
helper=ROOT/'scripts/helpers/boot-boundaries.py'
before=hashlib.sha256(anchor.read_bytes()).hexdigest()
env={k:v for k,v in os.environ.items() if not k.startswith('OTA_')};env['TMPDIR']='/tmp'
command=['bwrap','--unshare-all','--die-with-parent','--new-session','--ro-bind','/','/',
         '--tmpfs','/tmp','--dev','/dev','--proc','/proc','/usr/bin/python3',str(helper),str(anchor)]
result=subprocess.run(command,env=env,capture_output=True,text=True,timeout=60)
assert before==hashlib.sha256(anchor.read_bytes()).hexdigest()
rows=json.loads(result.stdout) if result.returncode==0 else []
report=dict(commit=REV,sourceSHA256={'ota/boot/startup.sh':before},helperSHA256=hashlib.sha256(helper.read_bytes()).hexdigest(),command=command,exitCode=result.returncode,stderr=result.stderr,cases=rows,result='known-limitations-reproduced' if result.returncode==0 and len(rows)==4 else 'failed',scope='Unchanged startup.sh; private user/PID/mount/network namespaces, read-only root, private /tmp, synthetic /dev. Only temporary shell stubs execute; no vendor remount, real agent, device, factory or network operation. Reproduces limits, not successful recovery qualification.')
(ROOT/'evidence/boot-anchor-characterization.json').write_text(json.dumps(report,indent=2)+'\n')
assert report['result']=='known-limitations-reproduced',result.stdout+result.stderr
print('PASS: four boot-anchor limitations reproduced in private namespaces')
