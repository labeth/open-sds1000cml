#!/usr/bin/env python3
"""Bounded LCD Bode drawing and range preconditions; no nonfinite line drawing."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
review=json.loads((ROOT/'evidence/lcd-remaining-source-review.json').read_text())
assert all(sha(SOURCE/p)==h for p,h in review['sourceSHA256'].items()),'reviewed source changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();helper=ROOT/'scripts/helpers/lcd-bode-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-bode-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_bode_review_test.go'):str(replacement)}}))
 command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^TestModelLCDBodeBoundaries$']
 run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
events=[json.loads(l) for l in run.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
cases=['empty_curve_ignores_unused_arrays','missing_parallel_arrays_panic','decades_have_equal_pixel_spacing','nonpositive_frequency_clamps_to_left','unused_gain_tail_changes_scale','range_empty_and_constant','range_nonfinite_is_not_sanitized']
expected=[('TestModelLCDBodeBoundaries'+n,'pass') for n in ['']+['/'+c for c in cases]]
assert run.returncode==0 and sorted(terminal)==sorted(expected),run.stdout.decode()+run.stderr.decode()
assert before==snapshot(),'LCD inputs changed'
r=dict(result='pass-characterization',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=command,terminal=dict(terminal),stdout=run.stdout.decode(),stderr=run.stderr.decode(),scope='Seven bounded Bode cases under race detector. Nonfinite values only enter range calculation or unused empty-curve arrays, never numeric line drawing. Selected log-spaced centers and malformed-array/range behavior only; no measurement or physical gain/phase claim. Source and golden files unchanged.')
(ROOT/'evidence/lcd-bode-boundary-review.json').write_text(json.dumps(r,indent=2)+'\n');print('PASS: seven bounded LCD Bode cases; unchanged LCD source/golden files')
