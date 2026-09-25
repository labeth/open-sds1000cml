#!/usr/bin/env python3
"""Bounded super-resolution display-helper cases without nonfinite line drawing."""
import hashlib,json,os,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
review=json.loads((ROOT/'evidence/lcd-superres-source-review.json').read_text())
assert all(sha(SOURCE/p)==h for p,h in review['sourceSHA256'].items()),'reviewed source changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();helper=ROOT/'scripts/helpers/lcd-superres-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-superres-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_superres_review_test.go'):str(replacement)}}))
 command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^TestModelLCDSuperresBoundaries$']
 run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
events=[json.loads(l) for l in run.stdout.splitlines()];terminal=[(e['Test'],e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
cases=['gap_fill_exact_values','all_gaps_fill_midscale_but_remain_invalid','nan_neighbor_precondition_panics','equal_size_resample_shifts_half_bin','nonfinite_sample_interval_returns_ok_zero_nyquist','fft_cap_resamples_without_antialias_filter','all_gap_second_channel_lifts_xy_pen','review_bode_and_spectrogram_fall_back_to_yt']
expected=[('TestModelLCDSuperresBoundaries'+n,'pass') for n in ['']+['/'+c for c in cases]]
assert run.returncode==0 and sorted(terminal)==sorted(expected),run.stdout.decode()+run.stderr.decode()
assert before==snapshot(),'LCD inputs changed'
r=dict(result='pass-characterization',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=command,terminal=dict(terminal),stdout=run.stdout.decode(),stderr=run.stderr.decode(),scope='Eight bounded display-helper cases under race detector. Nonfinite inputs are confined to fill/plan functions, not line drawing. Exact gap values and one capped alternating-grid example are tested; no general reconstruction, physical spectral or timing claim. Source and golden files unchanged.')
(ROOT/'evidence/lcd-superres-boundary-review.json').write_text(json.dumps(r,indent=2)+'\n');print('PASS: eight bounded super-resolution display cases; unchanged LCD source/golden files')
