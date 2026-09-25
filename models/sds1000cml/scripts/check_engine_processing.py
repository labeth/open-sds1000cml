#!/usr/bin/env python3
"""Run the reviewed engine processing batch once and index exact eligible tests."""
import hashlib,json,os,subprocess
from inventory import ROOT,SOURCE,REV
sha=lambda b:hashlib.sha256(b).hexdigest()
read=lambda n:json.loads((ROOT/'evidence'/n).read_text())
batch=read('engine-processing-annotation-batch.json')
overlays={r['path']:r for r in read('annotation-overlay.json')['files']}
for path,h in batch['sourceSHA256'].items():
    o=overlays[path]
    assert o['baselineSHA256']==h and o['annotatedSHA256']==sha((SOURCE/path).read_bytes()) and o['exactBaselineRecovery']
inputs=sorted(p for folder in ('engine','dsp','sramcapture') for p in (SOURCE/'app/internal'/folder).glob('*.go'))
def snapshot(): return {str(p.relative_to(SOURCE)):sha(p.read_bytes()) for p in inputs}
before=snapshot()
names=batch['uniqueReviewedTests']
cmd=['go','test','-race','-json','-count=1','-timeout=120s','./internal/engine','-run','^('+'|'.join(names)+')$']
env={'GOPROXY':'off'}
p=subprocess.run(cmd,cwd=SOURCE/'app',env={**os.environ,**env},capture_output=True,timeout=150)
assert p.returncode==0,p.stdout.decode()+p.stderr.decode()
events=[json.loads(x) for x in p.stdout.splitlines()]
terminal={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
assert terminal==dict.fromkeys(names,'pass') and before==snapshot()
log=ROOT/'evidence/test-runs/engine-processing-reviewed.jsonl';log.write_bytes(p.stdout)
report=dict(commit=REV,result='pass',runtimeInputSHA256=before,command=cmd,environment=env,tests=terminal,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(p.stdout),nativeEligibleTests=batch['nativeEligibleTests'],scope='27 unique original host tests; 27 linked to requirements including source-derived uniformity telemetry REQ-SDS-126. No hardware qualification or runSRAM execution. Source-review limitations remain applicable.')
(ROOT/'evidence/engine-processing-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
index=ROOT/'evidence/test-runs/results.json'; rows=[r for r in json.loads(index.read_text()) if r['id']!='engine-processing']
rows.append(dict(id='engine-processing',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command=cmd,environment=env,exitCode=0,result='pass',log=log.name,sha256=sha(p.stdout),scope=report['scope']))
index.write_text(json.dumps(rows,indent=2)+'\n')
for name in ('sram','interleave','modes','qualify'):
    path=ROOT/'evidence'/('engine-'+name+'-source-review.json'); review=json.loads(path.read_text())
    review['executionSupersededBy']='engine-processing-test-run.json'
    review['nativeIntegration']='27 deduplicated tests across the combined batch; uniformity has partial evidence only. Original source review and preannotation hashes retained.'
    path.write_text(json.dumps(review,indent=2)+'\n')
print('PASS: 27 unique tests; 27 eligible native outcomes; unchanged input snapshot')
