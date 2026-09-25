#!/usr/bin/env python3
"""Characterize network-label helpers with injected address enumeration only."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
review=json.loads((ROOT/'evidence/lcd-netaddr-source-review.json').read_text())
assert all(sha(SOURCE/p)==h for p,h in review['sourceSHA256'].items()),'reviewed source changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();helper=ROOT/'scripts/helpers/lcd-netaddr-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-netaddr-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_netaddr_review_test.go'):str(replacement)}}))
 command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^TestModelLCDNetaddrBoundaries$']
 run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
events=[json.loads(l) for l in run.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')];expected=[('TestModelLCDNetaddrBoundaries'+n,'pass') for n in ['', '/typed_nil_addresses_panic','/enumeration_failure_cached_until_expiry']]
assert run.returncode==0 and sorted(terminal)==sorted(expected),run.stdout.decode()+run.stderr.decode()
assert before==snapshot(),'LCD source/golden inputs changed'
r=dict(result='pass-characterization',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=command,terminal=dict(terminal),stdout=run.stdout.decode(),stderr=run.stderr.decode(),scope='Two overlay cases under race detector; typed-nil panic and cached failure/expiry characterized. Enumeration is injected and timestamps manipulated deterministically; no network access, real-time latency or interface reachability established.')
(ROOT/'evidence/lcd-netaddr-boundary-review.json').write_text(json.dumps(r,indent=2)+'\n');print('PASS: two network-label boundary cases; no network access or source/golden mutation')
