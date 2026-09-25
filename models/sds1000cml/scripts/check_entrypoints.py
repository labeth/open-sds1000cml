#!/usr/bin/env python3
"""Review-only host helper tests and compile checks; command mains are never invoked."""
import hashlib,json,os,subprocess,sys
from inventory import ROOT,SOURCE,REV
sha=lambda b:hashlib.sha256(b).hexdigest()
evidence=ROOT/'evidence'
review=json.loads((evidence/'entrypoints-source-review.json').read_text())
baseline='--baseline' in sys.argv
suffix='review' if baseline else 'integrated'
overlays={x['path']:x for x in json.loads((evidence/'annotation-overlay.json').read_text())['files']}
for path,digest in review['sourceSHA256'].items():
    current=sha((SOURCE/path).read_bytes())
    if current==digest:continue
    assert not baseline,path
    row=overlays[path]
    assert row['baselineSHA256']==digest and row['annotatedSHA256']==current and row['exactBaselineRecovery'],path

def snapshot():
    paths=[x['path'] for x in json.loads((evidence/'source-inventory.json').read_text())]
    return {p:sha((SOURCE/p).read_bytes()) for p in paths if p.startswith('app/') or
            (p.startswith('fpga/') and (p.endswith('.go') or p.endswith('/go.mod'))) or p.startswith('tools/hw/sramburst/')}

before=snapshot();runs=[]
for module,packages in [('app',['./cmd/app','./cmd/acqsram','./cmd/srambench','./internal/buildinfo']),('fpga',['./cmd/buildfpga']),('tools/hw/sramburst',['.'])]:
    command=['go','test','-race','-json','-count=1','-timeout=120s',*packages,'-run','^('+'|'.join(review['uniqueReviewedTests'])+')$']
    run=subprocess.run(command,cwd=SOURCE/module,env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=150)
    log=evidence/'test-runs'/('entrypoints-'+suffix+'-'+module.replace('/','-')+'.jsonl');log.write_bytes(run.stdout)
    events=[json.loads(l) for l in run.stdout.splitlines()]
    terminal={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
    runs.append(dict(module=module,command=command,exitCode=run.returncode,tests=terminal,log=str(log.relative_to(evidence)),logSHA256=sha(run.stdout),stderr=run.stderr.decode(),packageOutcomes={e['Package']:e['Action'] for e in events if not e.get('Test') and e['Action'] in ('pass','fail','skip')}))
report=dict(commit=REV,result='pass' if all(x['exitCode']==0 for x in runs) else 'fail',runtimeInputSHA256=before,runs=runs,scope=review['scope'])
(evidence/('entrypoints-'+suffix+'-test-run.json')).write_text(json.dumps(report,indent=2)+'\n')
assert snapshot()==before,'inputs changed during execution'
assert all(x['exitCode']==0 for x in runs),runs
app=runs[0];top={k:v for k,v in app['tests'].items() if '/' not in k}
assert top==dict.fromkeys(review['uniqueReviewedTests'],'pass'),app
assert len(app['tests'])==7 and all(s=='pass' for s in app['tests'].values()),app
assert all(not x['tests'] for x in runs[1:]),runs
if not baseline:
    p=evidence/'test-runs/results.json';rows=[x for x in json.loads(p.read_text()) if x['id']!='entrypoints']
    rows.append(dict(id='entrypoints',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command=app['command'],environment={'GOPROXY':'off'},exitCode=0,result='pass',log=app['log'].split('/')[-1],sha256=app['logSHA256'],scope=review['scope']))
    p.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: two original helper tests and five subtests; six packages compile; command mains not invoked; unchanged inputs')
