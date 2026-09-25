#!/usr/bin/env python3
"""Execute unchanged original UI harnesses, retaining failures and optional skips."""
import hashlib,json,os,re,subprocess,shutil
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
evidence=ROOT/'evidence';review=json.loads((evidence/'browser-harness-source-review.json').read_text())
overlays={x['path']:x for x in json.loads((evidence/'annotation-overlay.json').read_text())['files']}
for p,d in review['sourceSHA256'].items():
 current=sha(SOURCE/p)
 if current==d:continue
 row=overlays[p];assert row['baselineSHA256']==d and row['annotatedSHA256']==current and row['exactBaselineRecovery'],p
def snapshot():
 return {x['path']:sha(SOURCE/x['path']) for x in json.loads((evidence/'source-inventory.json').read_text()) if x['path'].startswith('app/')}
before=snapshot();discovery="""import {pathToFileURL} from 'node:url';import fs from 'node:fs';import path from 'node:path';const h=await import(pathToFileURL(process.argv[1]));const p=h.findPlaywright();if(!p)throw Error('No installed Playwright');console.log(JSON.stringify({playwright:p,version:JSON.parse(fs.readFileSync(path.join(path.dirname(p),'package.json'),'utf8')).version}));"""
runtime=json.loads(subprocess.check_output(['node','--input-type=module','-e',discovery,str(SOURCE/'app/internal/web/scope_po.mjs')],text=True));runtime['nodeVersion']=subprocess.check_output(['node','--version'],text=True).strip();runtime['goVersion']=subprocess.check_output(['go','version'],text=True).strip();runtime['packageSHA256']=sha(Path(runtime['playwright']).parent/'package.json');runtime['sigrokPath']=shutil.which('sigrok-cli')
env={k:v for k,v in os.environ.items() if k not in ['PERFSERVE','SHOT_DIR','FUZZ_ITERS','FUZZ_SEED','DEBUG_AT','LOG_ACTIONS','CI_REQUIRE_SIGROK','SCOPE_URL']};env.update(GOPROXY='off',CI_REQUIRE_BROWSER='1',DEBUG='pw:browser',PLAYWRIGHT_DIR=str(Path(runtime['playwright']).parents[1]))
names=review['uniqueReviewedTests'];command=['go','test','-race','-json','-count=1','-timeout=20m','./internal/web','-run','^('+'|'.join(names)+')$'];log=evidence/'test-runs/browser-harness-integrated.jsonl'
with log.open('wb') as f:run=subprocess.run(command,cwd=SOURCE/'app',env=env,stdout=f,stderr=subprocess.PIPE,timeout=1250)
events=[json.loads(l) for l in log.read_bytes().splitlines()];terminal={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')};output=''.join(e.get('Output','') for e in events);launches=sorted(set(re.findall(r'<launching> ([^\s]+)',output)));runtime['launchedExecutables']={p:sha(Path(p)) for p in launches}
report=dict(commit=REV,result='pass' if run.returncode==0 else 'fail',exitCode=run.returncode,command=command,environment={k:env[k] for k in ['GOPROXY','CI_REQUIRE_BROWSER','DEBUG','PLAYWRIGHT_DIR']},runtimeInputSHA256=before,runtime=runtime,tests=terminal,log=str(log.relative_to(evidence)),logSHA256=sha(log),stderr=run.stderr.decode(),scope=review['scope'])
(evidence/'browser-harness-integrated-test-run.json').write_text(json.dumps(report,indent=2)+'\n');assert snapshot()==before,'inputs changed during execution';assert set(terminal)==set(names),'missing terminal outcomes';assert launches,'browser launch evidence missing'
for name,status in terminal.items():
 if status=='skip':assert name in ['TestPerfServe','TestSigrokExportLogicE2E'],(name,status)
 if status=='pass':assert not any('SKIP:' in e.get('Output','') for e in events if e.get('Test')==name),(name,'hidden browser skip')
print('CHARACTERIZED:',{s:list(terminal.values()).count(s) for s in ['pass','fail','skip']},'unchanged app inputs; original failures retained')

p=evidence/'test-runs/results.json';rows=[x for x in json.loads(p.read_text()) if x['id']!='browser-harness'];rows.append(dict(id='browser-harness',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command=command,environment=report['environment'],exitCode=run.returncode,result=report['result'],log=log.name,sha256=sha(log),scope=review['scope']));p.write_text(json.dumps(rows,indent=2)+'\n')
