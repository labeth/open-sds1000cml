#!/usr/bin/env python3
"""Run reviewed LCD render tests and bounded overlay cases without device access."""
import hashlib,json,os,re,shlex,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
review=json.loads((ROOT/'evidence/lcd-render-source-review.json').read_text())
assert all(sha(SOURCE/p)==h for p,h in review['sourceSHA256'].items()),'reviewed source changed'
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot();env={**os.environ,'GOPROXY':'off'}
source=SOURCE/'app/internal/lcd/render_test.go'
names=re.findall(r'^func (Test\w+)\(t \*testing.T\)',source.read_text(),re.M)
assert len(names)==14
command=['go','test','-race','-json','-count=1','./internal/lcd','-run','^('+'|'.join(names)+')$']
run=subprocess.run(command,cwd=SOURCE/'app',env=env,capture_output=True,timeout=120)
def terminal(data):
 return [(e['Test'],e['Action']) for line in data.splitlines() if (e:=json.loads(line)).get('Test') and e['Action'] in ('pass','fail','skip')]
assert run.returncode==0 and sorted(terminal(run.stdout))==sorted((n,'pass') for n in names),run.stdout.decode()+run.stderr.decode()
helper=ROOT/'scripts/helpers/lcd-render-boundaries.go.txt'
with tempfile.TemporaryDirectory(prefix='sds-lcd-render-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes());overlay=temp/'overlay.json'
 overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_render_review_test.go'):str(replacement)}}))
 probe_command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^TestModelLCDRenderBoundaries$']
 probe=subprocess.run(probe_command,cwd=SOURCE/'app',env=env,capture_output=True,timeout=120)
cases=['same_sequence_ignores_signal_precision_guard_and_age','changed_sequence_throttled_then_expires','scale_change_recomputes_even_same_sequence','negative_valid_panics','negative_decoder_selectors_panic','zero_window_mask_panics','enabled_mask_rejects_geometry_mismatch','zones_draw_with_mode_zero','one_sample_xy_has_no_segment','persistence_requires_concrete_mem_surface','fft_matches_direct_dft','flat_fft_reports_lower_visible_frequency','fft_leaves_trailing_record_unsampled']
expected=[('TestModelLCDRenderBoundaries','pass')]+[('TestModelLCDRenderBoundaries/'+c,'pass') for c in cases]
assert probe.returncode==0 and sorted(terminal(probe.stdout))==sorted(expected),probe.stdout.decode()+probe.stderr.decode()
assert before==snapshot(),'LCD source/golden inputs changed'
log=ROOT/'evidence/test-runs/lcd-render.jsonl';log.write_bytes(run.stdout)
report=dict(result='pass-partial-native-evidence',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),originalTests=dict(terminal(run.stdout)),originalCommand=command,originalLog=str(log.relative_to(ROOT/'evidence')),originalLogSHA256=sha(log),characterization=dict(command=probe_command,terminal=dict(terminal(probe.stdout)),stdout=probe.stdout.decode(),stderr=probe.stderr.decode()),scope='Fourteen original in-memory rendering tests and thirteen overlay cases under race detector. No source/golden mutation, device access or physical qualification. Original tests contribute partial native verification and predominantly check color presence/counts; super-resolution and spectrogram callees remain partly unreviewed. Overlay cases are separate characterization evidence. Cache timestamps are manipulated deterministically; this is branch behavior, not wall-clock performance measurement.')
(ROOT/'evidence/lcd-render-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
index=ROOT/'evidence/test-runs/results.json'
rows=[r for r in json.loads(index.read_text()) if r['id']!='lcd-render']
rows.append(dict(id='lcd-render',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope='Fourteen original in-memory LCD renderer tests under race detector; predominantly presence/count assertions, not full pixel or spectral accuracy. Thirteen overlay characterizations are separate evidence in lcd-render-test-run.json. No physical qualification.'))
index.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: 14 original render tests, 13 boundary cases, unchanged LCD source/golden inputs; native partial evidence recorded')
