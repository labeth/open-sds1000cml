#!/usr/bin/env python3
"""Record bounded SRAM-engine source review and original host assertions."""
import hashlib
import json
import os
import re
import subprocess
from inventory import ROOT, SOURCE, REV, git

files = ['app/internal/engine/'+n for n in ('sram.go','sram_math.go','sram_test.go','sram_math_test.go')]
sha = lambda b: hashlib.sha256(b).hexdigest()
for path in files:
    assert (SOURCE/path).read_bytes() == git('show', REV+':'+path), path
inputs = sorted(list((SOURCE/'app/internal/engine').glob('*.go')) + list((SOURCE/'app/internal/sramcapture').glob('*.go')) + list((SOURCE/'app/internal/dsp').glob('*.go')))
def snapshot():
    return {str(p.relative_to(SOURCE)):sha(p.read_bytes()) for p in inputs}
before = snapshot()
names = sorted(n for p in files if p.endswith('_test.go') for n in re.findall(r'^func (Test\w+)\(', (SOURCE/p).read_text(), re.M))
assert len(names) == 12
cmd = ['go','test','-race','-json','-count=1','-timeout=90s','./internal/engine','-run','^('+'|'.join(names)+')$']
env = {'GOPROXY':'off'}
p = subprocess.run(cmd,cwd=SOURCE/'app',env={**os.environ,**env},capture_output=True,timeout=120)
assert p.returncode == 0, p.stdout.decode()+p.stderr.decode()
events = [json.loads(line) for line in p.stdout.splitlines()]
terminal = {e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
assert terminal == dict.fromkeys(names,'pass'), terminal
assert snapshot() == before
log = ROOT/'evidence/test-runs/engine-sram-reviewed.jsonl'; log.write_bytes(p.stdout)
findings = [
'PlanSRAM selects a full 1048576-sample raw record at 2 ns/sample when the screen fits, otherwise 524288 precision words beginning at log4 and increasing through log20. Invalid nonpositive, NaN or infinite timebase falls back to 500 us/div. Tests exercise all supported timebases, but do not prove arbitrary huge finite timebases can fit after the maximum reduction.',
'PlanPrecisionSRAM clamps log to 4..20 and retains that rate independently of view; screen count is bounded to the record. Tests explicitly check selected rates and all seventeen logs across a fixed timebase list. SetPrecisionRate is inspected as a supporting excerpt: it rounds log2 rate ratio, clamps the result and increments average generation.',
'sramConfig uses pending timebase, full physical depth regardless of selected memDepth, words based on raw/precision format and prehistory clamped below total words. Hardware Normal is enabled only for edge trigger outside serial-trigger mode. Three raw-depth examples are checked; trigger configuration combinations are not exhaustively tested here.',
'sramFrameWriter checks four-byte alignment and predicted frame overflow, then interprets Q8 little-endian channel pairs or two raw interleaved pairs. It trusts metadata consistency and allocated buffers. Tests cover one raw word, two Q8 words, rounded saturation and a second-write overrun; they omit misalignment and inconsistent metadata/buffer cases.',
'runSRAM services controls while waiting, arms with timeout, handles status faults and config changes, and uses freeze/read/rearm. AUTO force waits for prehistory plus 100 ms; the actual condition also permits force when running becomes false. The nearby SINGLE/NORM comment must be read with that condition. No reviewed test executes this loop.',
'Live capture retains metadata and reads a bounded trigger-centred window; stopping can replay the retained record with full recall. Revision10 chooses RecallForward. Recall errors or short frame writes prevent publication. Successful full recall clears retained state. These transitions are source-inspected only in this batch.',
'liveSRAMWindow uses max(2048,screen+512) raw samples or max(256,screen+128) Q8 samples, caps to record length and centres/clamps around trigger. Tests cover raw-format metadata with one/two samples per word, two lengths, three anchors and two screens; FractionBits8 preview sizing is not directly covered.',
'Conditioning retains Q8, applies precision FIR, optional interleave calibration and optional ERES, then rounds display bytes. Response fields include ideal noise gain and bandwidth. Stopped full recall bypasses block averaging. Integration with DSP/calibration is source-inspected, not executed by these focused tests.',
'ERES uses odd clamped boxcar lengths and shrinking end windows with rounded Q8 output. Twelve small combinations of four signal lengths and three filter lengths are compared to a direct reference sum. Valid scratch capacity and nonaliasing are caller preconditions, not guarded here.',
'sramAverage stores uint32 channel sums, resets disjoint blocks at target count, rounds trigger-anchor shifts to integer samples and crops to common overlap without wrap. Tests check fractional means, reset, a 256-frame extreme sum and shifts 0,+3,-2. This does not establish fractional-delay alignment or arbitrary target safety.',
'runSRAM averages only qualified live average/precision records, resets on configuration-key changes or unavailable crossing, and gates SINGLE completion on the average target. Software pulse/slope/video, serial and zone qualification operate on the recalled window; publication permits qualified, non-normal or full-recall frames. No gapless trigger claim follows.',
'The twelve passing tests supply partial host evidence only. They neither operate hardware nor validate capture-loop cancellation, retained replay, bus faults, trigger interactions, timing or all processing-mode combinations.'
]
report = dict(commit=REV,requirements=['REQ-SDS-003','REQ-SDS-012','REQ-SDS-036','REQ-SDS-037','REQ-SDS-040'],owner='FU-APP-ENGINE',completeReads=files,reviewedLines=sum(len((SOURCE/p).read_text().splitlines()) for p in files),sourceSHA256={p:before[p] for p in files},runtimeInputSHA256=before,command=cmd,environment=env,exitCode=0,tests=terminal,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(p.stdout),findings=findings,result='reviewed-host-tests-pass-not-yet-native-integrated',nextAction='Review capture-loop collaborators and assign declaration-level requirement links before batched native integration. Preserve the distinction between helper tests and unexecuted runSRAM behavior.')
(ROOT/'evidence/engine-sram-source-review.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: twelve SRAM-engine tests under race detector; 813 reviewed source lines; capture loop remains source-inspected only')
