#!/usr/bin/env python3
"""Index exact original remaining LCD regression outcomes for native traces."""
import hashlib,json,os,shlex,subprocess
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
reviews={n:json.loads((ROOT/f'evidence/{n}-source-review.json').read_text()) for n in ['lcd-remaining']}
for r in reviews.values(): assert all(sha(SOURCE/p)==h for p,h in r['sourceSHA256'].items()),'reviewed source changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();names=sorted(n for r in reviews.values() for n in r['tests']);assert len(names)==len(set(names))==16
command=['go','test','-race','-json','-count=1','./internal/lcd','-run','^('+'|'.join(names)+')$']
run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
events=[json.loads(l) for l in run.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
assert run.returncode==0 and sorted(terminal)==[(n,'pass') for n in names],run.stdout.decode()+run.stderr.decode()
assert before==snapshot(),'LCD inputs changed'
log=ROOT/'evidence/test-runs/lcd-remaining.jsonl';log.write_bytes(run.stdout)
report=dict(result='pass-partial-native-evidence',commit=REV,sourceSHA256=before,tests=dict(terminal),command=command,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(log),scope='Sixteen original tests: thirteen golden RGB comparisons with 64-pixel tolerance, two inversion tests and a fixed-seed 400-case ordinary no-panic test. Golden references unchanged. Passing images preserve existing defects and do not establish physical qualification.')
(ROOT/'evidence/lcd-remaining-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
for name,r in reviews.items():
 r.update(result='reviewed-and-characterized-native-partial-evidence',command=command,log=report['log'],logSHA256=sha(log),nativeEvidence='evidence/lcd-remaining-test-run.json',nextAction='Retain explicit limitations; device qualification and remaining LCD source coverage are separate work.')
 (ROOT/f'evidence/{name}-source-review.json').write_text(json.dumps(r,indent=2)+'\n')
index=ROOT/'evidence/test-runs/results.json';rows=[r for r in json.loads(index.read_text()) if r['id']!='lcd-remaining'];rows.append(dict(id='lcd-remaining',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope=report['scope']));index.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: sixteen original remaining LCD tests indexed with exact outcomes and unchanged LCD inputs')
