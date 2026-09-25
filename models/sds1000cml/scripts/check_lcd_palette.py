#!/usr/bin/env python3
"""Check original palette parity and isolated generation without rewriting artifacts."""
import hashlib,json,os,shlex,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
p=ROOT/'evidence/lcd-palette-source-review.json';review=json.loads(p.read_text());before=review['sourceSHA256'];assert all(sha(SOURCE/p)==h for p,h in before.items()),'reviewed palette source changed'
env={**os.environ,'GOPROXY':'off'};command=['go','test','-race','-json','-count=1','./internal/web','-run','^TestPaletteParity$']
r=subprocess.run(command,cwd=SOURCE/'app',env=env,capture_output=True,timeout=120);events=[json.loads(l) for l in r.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ['pass','fail','skip']];assert r.returncode==0 and terminal==[('TestPaletteParity','pass')],r.stdout.decode()+r.stderr.decode()
log=ROOT/'evidence/test-runs/lcd-palette.jsonl';log.write_bytes(r.stdout)
with tempfile.TemporaryDirectory(prefix='sds-palette-') as td:
 base=Path(td);web=base/'web';lcd=base/'lcd';web.mkdir();lcd.mkdir();(web/'gen_tokens.go').write_bytes((SOURCE/'app/internal/web/gen_tokens.go').read_bytes());original=(SOURCE/'app/internal/web/tokens.json').read_bytes();(web/'tokens.json').write_bytes(original)
 def generate(): return subprocess.run(['go','run','gen_tokens.go'],cwd=web,env=env,capture_output=True,timeout=120)
 run=generate();assert run.returncode==0,run.stderr.decode()
 assert (web/'tokens.css').read_bytes()==(SOURCE/'app/internal/web/tokens.css').read_bytes()
 assert (lcd/'palette_gen.go').read_bytes()==(SOURCE/'app/internal/lcd/palette_gen.go').read_bytes()
 # An invalid LCD reference is rejected after CSS was already written.
 bad=json.loads(original);bad['tokens']['c1']='#010203';bad['lcd']['colC1']='missing';(web/'tokens.json').write_text(json.dumps(bad));old=(lcd/'palette_gen.go').read_bytes();run=generate();assert run.returncode!=0 and b'unknown token' in run.stderr;assert b'--c1: #010203;' in (web/'tokens.css').read_bytes() and (lcd/'palette_gen.go').read_bytes()==old
 # Malformed hexadecimal source is silently emitted as black in Go.
 bad=json.loads(original);bad['tokens']['c1']='#nothex';(web/'tokens.json').write_text(json.dumps(bad));run=generate();assert run.returncode==0 and b'rgb(0, 0, 0)' in (lcd/'palette_gen.go').read_bytes() and b'--c1: #nothex;' in (web/'tokens.css').read_bytes()
assert all(sha(SOURCE/p)==h for p,h in before.items()),'palette inputs/artifacts changed'
report=dict(result='pass-partial-native-evidence',commit=REV,sourceSHA256=before,tests=dict(terminal),command=command,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(log),isolatedGeneratorCases=['valid artifacts byte-identical','unknown LCD reference fails after CSS write; Go artifact retained','malformed hex succeeds with black Go color and invalid CSS literal'],scope='Original parity assertions plus three isolated generator cases. No original generated artifact rewritten. Parsed RGB equality is not optical/display qualification.')
(ROOT/'evidence/lcd-palette-test-run.json').write_text(json.dumps(report,indent=2)+'\n');review.update(result='reviewed-and-characterized-native-partial-evidence',tests=dict(terminal),nativeEvidence='evidence/lcd-palette-test-run.json');p.write_text(json.dumps(review,indent=2)+'\n')
p=ROOT/'evidence/test-runs/results.json';rows=[x for x in json.loads(p.read_text()) if x['id']!='lcd-palette'];rows.append(dict(id='lcd-palette',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope=report['scope']));p.write_text(json.dumps(rows,indent=2)+'\n');print('PASS: original palette parity and three isolated generation cases; source artifacts unchanged')
