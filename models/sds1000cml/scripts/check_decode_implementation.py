#!/usr/bin/env python3
"""Run reviewed ordinary decoder tests and separate acceptance characterizations."""
import hashlib,json,os,shlex,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
p=ROOT/'evidence/decode-implementation-source-review.json';review=json.loads(p.read_text());assert all(sha(SOURCE/n)==h for n,h in review['sourceSHA256'].items()),'reviewed source changed'
def snapshot():return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/decode').glob('*.go'))}
before=snapshot();env={**os.environ,'GOPROXY':'off'};names=sorted(review['tests']);assert len(names)==33
command=['go','test','-race','-json','-count=1','-timeout=120s','./internal/decode','-run','^('+'|'.join(names)+')$']
def run(cmd):
 r=subprocess.run(cmd,cwd=SOURCE/'app',env=env,capture_output=True,timeout=150);events=[json.loads(l) for l in r.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ['pass','fail','skip']];assert r.returncode==0,r.stdout.decode()+r.stderr.decode();return r,terminal
original,outcomes=run(command);assert sorted(outcomes)==[(n,'pass') for n in names],outcomes
helper=ROOT/'scripts/helpers/decode-acceptance-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-decode-') as td:
 temp=Path(td);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/decode/model_acceptance_review_test.go'):str(replacement)}}));overlaycmd=['go','test','-race','-json','-count=1','-timeout=120s','-overlay',str(overlay),'./internal/decode','-run','^TestModelDecoderAcceptanceBoundaries$'];boundary,terminal=run(overlaycmd)
cases=['flexray_length_controls_frame_crc_check','sent_clamped_coding_error_can_retain_ok','classic_can_without_delimiter_or_ack_is_retained','canfd_accepts_data_without_validated_trailer'];expected=[('TestModelDecoderAcceptanceBoundaries'+s,'pass') for s in ['']+['/'+n for n in cases]];assert sorted(terminal)==sorted(expected)
assert before==snapshot(),'decoder sources changed'
log=ROOT/'evidence/test-runs/decode-implementation.jsonl';log.write_bytes(original.stdout)
report=dict(result='pass-partial-native-evidence',commit=REV,sourceSHA256=before,tests=dict(outcomes),command=command,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(log),scope='33 original tests in eight fully read ordinary decoder test files under race detector. Selected synthetic round trips and seeded robustness assertions, with reused CRC/extraction fixtures; no physical or independent standards qualification. Remaining test families are not credited by this run.')
(ROOT/'evidence/decode-implementation-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
br=dict(result='pass-characterization',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=overlaycmd,terminal=dict(terminal),stdout=boundary.stdout.decode(),stderr=boundary.stderr.decode(),scope='Four bounded acceptance cases under race detector: FlexRay header-only acceptance at three mismatched lengths versus exact-length bad-CRC rejection, SENT accepted CRC alongside coding error, classic CAN body without delimiter/ACK, and CAN-FD body without trailer. These preserve source limitations, not protocol qualification.')
(ROOT/'evidence/decode-acceptance-boundary-review.json').write_text(json.dumps(br,indent=2)+'\n')
review.update(result='reviewed-and-characterized-native-partial-evidence',tests=dict(outcomes),nativeEvidence='evidence/decode-implementation-test-run.json',boundaryEvidence='evidence/decode-acceptance-boundary-review.json');p.write_text(json.dumps(review,indent=2)+'\n')
p=ROOT/'evidence/test-runs/results.json';rows=[x for x in json.loads(p.read_text()) if x['id']!='decode-implementation'];rows.append(dict(id='decode-implementation',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope=report['scope']));p.write_text(json.dumps(rows,indent=2)+'\n');print('PASS: 33 original decoder tests and four acceptance cases; source unchanged')
