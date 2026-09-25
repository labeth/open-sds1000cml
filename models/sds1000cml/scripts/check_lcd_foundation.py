#!/usr/bin/env python3
"""Bounded LCD host characterization; no framebuffer or GPIO access."""
import hashlib,json,os,shlex,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,SOURCE,REV
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def snapshot():
 return {str(p.relative_to(SOURCE)):sha(p) for p in sorted((SOURCE/'app/internal/lcd').rglob('*')) if p.is_file()}
before=snapshot()
helper=ROOT/'scripts/helpers/lcd-foundation-boundaries.go.txt'
original_command=['go','test','-race','-json','-count=1','./internal/lcd','-run','^TestSiScaleNoScientific$']
original=subprocess.run(original_command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
original_events=[json.loads(line) for line in original.stdout.splitlines()]
assert original.returncode==0 and [(e['Test'],e['Action']) for e in original_events if e.get('Test') and e['Action'] in ('pass','fail','skip')]==[('TestSiScaleNoScientific','pass')],original.stdout.decode()+original.stderr.decode()
log=ROOT/'evidence/test-runs/lcd-foundation.jsonl';log.write_bytes(original.stdout)
with tempfile.TemporaryDirectory(prefix='sds-lcd-foundation-') as directory:
 temp=Path(directory);replacement=temp/'review_test.go';replacement.write_bytes(helper.read_bytes())
 overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/'app/internal/lcd/model_foundation_review_test.go'):str(replacement)}}))
 command=['go','test','-race','-json','-count=1','-overlay',str(overlay),'./internal/lcd','-run','^(TestModelLCDFoundation|TestSiScaleNoScientific)$']
 run=subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=120)
assert before==snapshot(),'LCD inputs changed'
events=[json.loads(line) for line in run.stdout.splitlines()]
terminal={e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
cases=['packing_clipping_fill','fade_components_and_odd_tail','blit_threshold_and_short_source','png_white_quantization','bmp_layout_and_short_buffer_mismatch','short_surface_panics','present_memory_fallback_only','unknown_rune_advances_blank','formatting_outside_prefix_range']
expected={name:'pass' for name in ['TestSiScaleNoScientific','TestModelLCDFoundation']+['TestModelLCDFoundation/'+c for c in cases]}
assert run.returncode==0 and terminal==expected,run.stdout.decode()+run.stderr.decode()
report=dict(result='pass-partial-native-evidence',commit=REV,sourceSHA256=before,helperSHA256=sha(helper),command=command,terminal=terminal,stdout=run.stdout.decode(),stderr=run.stderr.decode(),scope='One original formatting test and nine overlay characterizations with race detector. Source and golden files unchanged. No hardware entry point invoked: Present uses synthetic memory with panOK false. Only the separately executed original formatting test contributes partial native verification; the nine overlay cases are characterization evidence.',completeReads=['surface.go','font.go','render_fmt.go','engfmt_test.go','lcd.go'],partialReads={'render.go':'SI unit definitions at lines 303–313 only for this characterization'},findings=['MemSurface requires a full-sized buffer; in-range access to a short buffer panics.','EncodeBMP appends the supplied buffer without validating its length, while its header declares a full page.','PNG expands component bits by left shift: white is RGB 248,252,248.','SI formatting can emit scientific notation beyond its supported prefix range and retains NaN/Inf text.','Bringup ignores GPIO write failures; success alone does not establish panel enable. Inspection only.','OpenFB assumes fixed 800-wide RGB565 and 32-bit ARM vinfo offsets; driver compatibility and tear-free output remain unqualified.'])
(ROOT/'evidence/lcd-foundation-review.json').write_text(json.dumps(report,indent=2)+'\n')
index=ROOT/'evidence/test-runs/results.json'
rows=[r for r in json.loads(index.read_text()) if r['id']!='lcd-foundation']
rows.append(dict(id='lcd-foundation',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command='GOPROXY=off '+shlex.join(original_command),exitCode=0,result='pass',log=log.name,sha256=sha(log),scope='One original SI-prefix formatting test under race detector. Nine overlay characterizations are separate evidence in lcd-foundation-review.json; no framebuffer, GPIO or physical panel qualification.'))
index.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: original formatting test and nine LCD overlay cases; all LCD source/golden inputs unchanged; hardware not exercised')
