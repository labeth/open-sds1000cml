#!/usr/bin/env python3
"""Run bounded boot stub tests and compile command packages; no device commands."""
import hashlib,json,os,subprocess,sys
from inventory import ROOT,SOURCE,REV
sha=lambda b:hashlib.sha256(b).hexdigest()
evidence=ROOT/'evidence';review=json.loads((evidence/'ota-entrypoints-source-review.json').read_text());baseline='--baseline' in sys.argv
suffix='review' if baseline else 'integrated'
overlays={x['path']:x for x in json.loads((evidence/'annotation-overlay.json').read_text())['files']}
for path,digest in review['sourceSHA256'].items():
 current=sha((SOURCE/path).read_bytes())
 if current==digest:continue
 assert not baseline,path
 row=overlays[path];assert row['baselineSHA256']==digest and row['annotatedSHA256']==current and row['exactBaselineRecovery'],path
def snapshot():
 return {x['path']:sha((SOURCE/x['path']).read_bytes()) for x in json.loads((evidence/'source-inventory.json').read_text()) if x['path'].startswith('ota/')}
before=snapshot();names=review['uniqueReviewedTests'];command=['go','test','-race','-json','-count=1','-timeout=90s','./boot','./cmd/otactl','./cmd/stubapp','./internal/buildinfo','-run','^('+'|'.join(names)+')$']
env={k:v for k,v in os.environ.items() if not k.startswith(('OTA_','SCOPE_'))};env['GOPROXY']='off'
run=subprocess.run(command,cwd=SOURCE/'ota',env=env,capture_output=True,timeout=120);log=evidence/'test-runs'/('ota-entrypoints-'+suffix+'.jsonl');log.write_bytes(run.stdout)
events=[json.loads(l) for l in run.stdout.splitlines()];terminal={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
report=dict(commit=REV,result='pass' if run.returncode==0 else 'fail',exitCode=run.returncode,command=command,environment={'GOPROXY':'off','removedPrefixes':['OTA_','SCOPE_']},runtimeInputSHA256=before,tests=terminal,log=str(log.relative_to(evidence)),logSHA256=sha(run.stdout),stderr=run.stderr.decode(),scope=review['scope'],packageOutcomes={e['Package']:e['Action'] for e in events if not e.get('Test') and e['Action'] in ('pass','fail','skip')})
(evidence/('ota-entrypoints-'+suffix+'-test-run.json')).write_text(json.dumps(report,indent=2)+'\n')
assert snapshot()==before,'inputs changed during execution'
assert run.returncode==0 and terminal==dict.fromkeys(names,'pass'),report
if not baseline:
 p=evidence/'test-runs/results.json';rows=[x for x in json.loads(p.read_text()) if x['id']!='ota-entrypoints'];rows.append(dict(id='ota-entrypoints',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='ota',command=command,environment=report['environment'],exitCode=0,result='pass',log=log.name,sha256=sha(run.stdout),scope=review['scope']));p.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: six bounded original boot tests; four packages compile; OTA/SCOPE overrides removed; unchanged source inputs')
