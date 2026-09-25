#!/usr/bin/env python3
"""Bounded LCD spectrogram state evidence, independent of native trace credit."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
review=json.loads((ROOT/'evidence/lcd-spectrogram-source-review.json').read_text())
assert all(sha(SOURCE/p)==h for p,h in review['sourceSHA256'].items()),'reviewed inputs changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();helper=ROOT/'scripts/helpers/lcd-spectrogram-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-lcd-spectrogram-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_spectrogram_review_test.go'):str(replacement)}}))
 command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^TestModelLCDSpectrogramBoundaries$']
 run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
events=[json.loads(line) for line in run.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
cases=['invalid_floor_falls_back_without_updating_field','negative_infinite_floor_paints_white','clear_keeps_scale_and_floor','helper_accepts_repeated_sequence_envelope_and_flat','rate_change_retains_prior_row','channel_change_retains_prior_row','nonfinite_nyquist_retained','spectrum_ignores_trailing_nonpower_of_two_data','row_counter_saturates','zero_value_requires_constructor']
expected=[('TestModelLCDSpectrogramBoundaries','pass')]+[('TestModelLCDSpectrogramBoundaries/'+c,'pass') for c in cases]
assert run.returncode==0 and sorted(terminal)==sorted(expected),run.stdout.decode()+run.stderr.decode()
assert before==snapshot(),'LCD inputs changed'
report=dict(result='pass-characterization',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=command,terminal=dict(terminal),stdout=run.stdout.decode(),stderr=run.stderr.decode(),scope='Ten overlay helper-state cases under race detector; no source/golden changes or hardware operations. Repeated sequence/envelope acceptance is helper behavior: the reviewed lcdLoop caller separately gates these inputs. Counter saturation starts one row below the limit. No physical timing, concurrency qualification or general spectral accuracy asserted.')
(ROOT/'evidence/lcd-spectrogram-boundary-review.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: ten spectrogram boundary cases; unchanged LCD source/golden inputs; overlay characterization separate from native original-test outcomes')
