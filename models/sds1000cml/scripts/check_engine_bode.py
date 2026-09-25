#!/usr/bin/env python3
"""Record synthetic Bode outcomes and overlay-only state characterization."""
import hashlib,json,os,re,shlex,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
files=sorted((SOURCE/'app/internal/engine').glob('*.go'))
before={str(p.relative_to(SOURCE)):sha(p) for p in files}
names=['TestFundamentalHz','TestBodePointGainPhase','TestBodePureDelay','TestBodeRejectsFloorAndSubCycle','TestBodeAccumulationBinsByFrequency','TestBodeBreaker50']
env={**os.environ,'GOPROXY':'off'}
command=['go','test','-race','-json','-count=1','./internal/engine','-run','^('+'|'.join(names)+')$']
run=subprocess.run(command,cwd=SOURCE/'app',env=env,capture_output=True,timeout=120)
log=ROOT/'evidence/test-runs/engine-bode.jsonl';log.write_bytes(run.stdout)
assert run.returncode==0,run.stdout.decode()+run.stderr.decode()
events=[json.loads(line) for line in run.stdout.splitlines()]
terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
assert sorted(terminal)==sorted((n,'pass') for n in names),terminal
helper=ROOT/'scripts/helpers/engine-bode-boundaries.go.txt';target=SOURCE/'app/internal/engine/bode_test.go'
with tempfile.TemporaryDirectory(prefix='sds-engine-bode-') as directory:
 temp=Path(directory);replacement=temp/'bode_test.go';replacement.write_bytes(target.read_bytes()+b'\n'+helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(target):str(replacement)}}))
 probe_command=['go','test','-race','-json','-overlay',str(overlay),'-count=1','./internal/engine','-run','^TestModelReviewBodeBoundaries$']
 probe=subprocess.run(probe_command,cwd=SOURCE/'app',env=env,capture_output=True,timeout=120)
assert before=={str(p.relative_to(SOURCE)):sha(p) for p in files},'engine input changed during test execution'
probe_events=[json.loads(line) for line in probe.stdout.splitlines()]
subtests=[(e['Test'],e['Action']) for e in probe_events if '/' in e.get('Test','') and e['Action'] in ('pass','fail','skip')]
assert probe.returncode==0 and len(subtests)==9 and all(a=='pass' for _,a in subtests),probe.stdout.decode()+probe.stderr.decode()
# This is an exact direct-call check, not whole-program reachability analysis.
call_sites={str(p.relative_to(SOURCE)):[i for i,line in enumerate(p.read_text().splitlines(),1) if re.search(r'\.bodeEval\s*\(',line)] for p in files if not p.name.endswith('_test.go')}
call_sites={p:lines for p,lines in call_sites.items() if lines}
caller='app/internal/engine/engine_loop.go'
assert set(call_sites)=={caller} and len(call_sites[caller])==1,call_sites
lines=(SOURCE/caller).read_text().splitlines()
call_line=call_sites[caller][0]
enclosing=[line for line in lines[:call_line] if line.startswith('func ')]
assert enclosing and enclosing[-1]=='func (e *Engine) oneFrame(norm bool) {',enclosing[-1:]
reviewed=['app/internal/engine/'+n for n in ('bode.go','bode_test.go','bode_breaker_test.go')]
report=dict(result='pass',commit=REV,sourceSHA256={p:before[p] for p in reviewed},engineInputSHA256=before,helperSHA256=sha(helper),originalTests=dict(terminal),directProductionCalls=call_sites,boundaryCharacterization=dict(result='known-limitations-reproduced',cases=dict(subtests),command=probe_command,stdout=probe.stdout.decode(),stderr=probe.stderr.decode()),scope='Six original synthetic tests and nine overlay state cases with race detector. Injected bus/clock or zero-value engine; no owner acquisition loop or physical hardware. Direct source call check supports the reviewed SRAM integration gap, not arbitrary whole-program reachability. Passing characterizations preserve known limitations.')
(ROOT/'evidence/engine-bode-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
index=ROOT/'evidence/test-runs/results.json';rows=[r for r in json.loads(index.read_text()) if r['id']!='engine-bode'];rows.append(dict(id='engine-bode',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope='Six original synthetic engine Bode tests under race detector; no skipped tests, acquisition-loop execution or physical qualification. Separate overlay characterization is recorded in engine-bode-test-run.json.'));index.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: six original tests, nine overlay cases, unchanged engine inputs and sole legacy direct-call site')
