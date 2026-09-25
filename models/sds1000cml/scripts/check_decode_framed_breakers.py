#!/usr/bin/env python3
"""Record remaining breaker assertions and explicitly exercise disabled USB checks."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV, git
files = ['app/internal/decode/decode_break_' + p + '_test.go' for p in ('manchester','canfd','usbls','flexray')]
sha = lambda data: hashlib.sha256(data).hexdigest()
for path in files:
    current = (SOURCE / path).read_bytes()
    baseline = git('show', REV + ':' + path)
    if current != baseline:
        overlays = {r['path']: r for r in json.loads((ROOT / 'evidence/annotation-overlay.json').read_text())['files']}
        row = overlays[path]
        assert hashlib.sha256(baseline).hexdigest() == row['baselineSHA256'] and hashlib.sha256(current).hexdigest() == row['annotatedSHA256'] and row['exactBaselineRecovery'], path
def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in sorted((SOURCE / 'app/internal/decode').glob('*.go'))}
before = snapshot()
names = ['TestBreakManchester','TestBreakCanfd','TestBreakUsbls','TestBreakFlexray']
cmd = ['go','test','-race','-json','-count=1','-timeout=180s','./internal/decode','-run','^('+'|'.join(names)+')$']
def run(command):
    result = subprocess.run(command,cwd=SOURCE/'app',env={**os.environ,'GOPROXY':'off'},capture_output=True,timeout=210)
    events = [json.loads(line) for line in result.stdout.splitlines()]
    terminal = {e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
    return result,events,terminal
original,events,terminal = run(cmd)
assert sorted(n for n in terminal if '/' not in n)==sorted(names), terminal
log=ROOT/'evidence/test-runs/decode-framed-breakers-reviewed.jsonl';log.write_bytes(original.stdout)
usb_path='app/internal/decode/decode_break_usbls_test.go'
usb=(SOURCE/usb_path).read_text();old='const pinKnownBugs = false';new='const pinKnownBugs = true';assert usb.count(old)==1
replacement=usb.replace(old,new)
with tempfile.TemporaryDirectory(prefix='sds-usb-disabled-assertions-') as tmp:
    temp=Path(tmp);patched=temp/'usb_test.go';patched.write_text(replacement)
    overlay=temp/'overlay.json';overlay.write_text(json.dumps({'Replace':{str(SOURCE/usb_path):str(patched)}}))
    probe_cmd=['go','test','-race','-json','-count=1','-timeout=120s','-overlay',str(overlay),'./internal/decode','-run','^TestBreakUsbls$/^known_bug_']
    probe,probe_events,probe_terminal=run(probe_cmd)
probe_log=ROOT/'evidence/test-runs/decode-usb-enabled-assertions.jsonl';probe_log.write_bytes(probe.stdout)
assert snapshot()==before, 'Sources changed'
expected={'TestBreakUsbls':'fail','TestBreakUsbls/known_bug_tight_interpacket_gap':'fail','TestBreakUsbls/known_bug_crc_not_validated':'fail'}
assert probe.returncode==1 and probe_terminal==expected, probe.stdout.decode()+probe.stderr.decode()
findings={
'Manchester':[
'80 explicit-bitrate cases vary one through sixteen bits, conventions, bit order and preambled payloads; 60 automatic cases restrict widths to eight bits and periods to eight through sixty-four samples. Separate five-sample and constant-bit regression assertions are enabled.',
'60 noise inputs only require a nonempty error when not OK; acceptance is allowed. The square-wave loop repeats the identical period 80 three times and only checks equal output bytes if accepted.',
'60 flattened-cell cases require a frame-error for accepted corruption. The 50 truncation cuts occur only 1..5 whole cells plus a fraction after the six-cell idle, within the first eight-bit preamble word, despite the comment describing data-region cuts. This is not broad payload-tail truncation coverage.',
'The 201-word long-record case asserts exact bytes but has no scaling comparison or local time bound, so it does not prove linear complexity. Sixty degraded synthetic cases reject successful sampling periods below 0.75 of the clean value; this is not a replay of physical FPGA capture data.',
'Historical bug labels are retained in comments. Passing named regressions establishes only their particular generated shapes; it does not establish all phase ambiguities resolved.'
],
'CAN/CAN-FD':[
'60 seeded clean cases select classic base/extended or base FD with/without rate switching. Fifteen automatic-baud cases and twelve concatenated-classic cases check payloads. FD fixtures stop after data and add recessive padding, without a valid FD CRC/stuff-count trailer.',
'Classic fixtures use production canCRC15 and fdDataLen helpers. A shared mistake can pass. The confidence predicate searches for any clean classic CRC span and does not qualify FD frames or every frame of a mixed result.',
'60 corrupted-CRC and 60 truncated inputs fail only if a clean CRC coexists with bytes identical to the entire original payload. This permits other output shapes and does not require every malformed frame be rejected.',
'Garbage and seven Nyquist cases reject confident classic CRC acceptance. Comments saying stuffing polarity is never checked are stale: current code records stuffErr and marks classic CRC spans as errors. Fixed delimiters/EOF and FD integrity remain outside that gate.',
'Degenerate timing/rate combinations check no panic on selected finite values. The forty-frame long record checks exact payload, not resource scaling or physical bus performance.'
],
'USB':[
'60 synthetic cases test explicit and automatic decoding with gaps of 12..59 bit times, random PID names and arbitrary zero through six byte payloads. These are extractor fixtures, not necessarily legal packets for every PID, and do not construct valid USB CRCs.',
'60 negative shapes are checked at two time intervals and two rate settings. Selected SYNC corruption and mid-PID truncation require rejection; corrupt PID complement requires a frame-error but need not clear OK.',
'pinKnownBugs is false in the committed source. The tight-gap and CRC-named subtests only log results during the original green run; neither enforces its desired behavior.',
'A temporary overlay enables those two assertions only. Both fail, separately recorded as expected characterization failures, not passing qualification. The CRC fixture uses arbitrary trailing bytes rather than a computed valid CRC and single-bit corruption, so its observation is acceptance without error of that byte sequence; production inspection separately establishes the absence of CRC validation.',
'Hostile scalar tests allow OK and only require error text when not OK. Single-line synthetic EOP cannot establish differential SE0, physical low/full-speed signaling, or standards compliance.'
],
'FlexRay':[
'80 nominal round trips rewrite header CRC in random 5..24-byte arrays, without enforcing header-implied payload length or supplying frame CRC. They test the current header-only acceptance path rather than complete valid FlexRay frames.',
'Short-TSS, BSS corruption and truncation fixtures have random headers without a clean accepted control; rejection may follow invalid CRC independently of the targeted framing defect. Their assertions only forbid accepted output identical to the full input.',
'20 noise inputs are logged, with no rejection requirement. Minimum-period automatic decoding and constant-fill automatic decoding allow complete failure and check bytes only if OK.',
'The enabled 24-case integrity block checks corrupted header CRC against header-plus-payload controls that omit the three-byte frame CRC. It proves these header checks, not full frame CRC validation. Comments claiming no CRC validation are stale.',
'The forty-frame long-record assertion requires exact bytes but does not establish asymptotic resource bounds. Prior acceptance characterizations already document frame CRC being conditional on exact header-implied length.'
]}
report=dict(commit=REV,requirement='REQ-SDS-018',completeReads=files,reviewedLines=sum(len(git('show', REV + ':' + p).decode().splitlines()) for p in files),
 sourceSHA256={p:before[p] for p in files},runtimeInputSHA256=before,command=cmd,environment={'GOPROXY':'off'},exitCode=original.returncode,stderr=original.stderr.decode(),tests=terminal,
 log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(original.stdout),findings=findings,
 result='reviewed-original-tests-pass-disabled-usb-assertions-fail' if original.returncode==0 and all(a=='pass' for a in terminal.values()) else 'reviewed-original-test-failure',
 disabledUSBAssertions=dict(result='two-expected-assertion-failures',edit=dict(path=usb_path,before=old,after=new),replacementSHA256=sha(replacement.encode()),command=probe_cmd,exitCode=probe.returncode,tests=probe_terminal,log=str(probe_log.relative_to(ROOT/'evidence')),logSHA256=sha(probe.stdout),stderr=probe.stderr.decode()),
 scope='Four original deterministic tests under race detector, plus isolated activation of two disabled USB assertions. Original source unchanged. All ten reviewed breaker files contribute partial native evidence; no browser/hardware/standards qualification.',
 nextAction='Integrate all ten reviewed breaker files, preserving the disabled-assertion distinction and the three review reports; refresh source snapshots after comment-only annotation.')
(ROOT/'evidence/decode-framed-breakers-source-review.json').write_text(json.dumps(report,indent=2)+'\n')
print(report['result']+': '+str(len(terminal))+' original terminal outcomes; two disabled USB assertions fail when enabled; source unchanged')
if original.returncode: raise SystemExit(original.returncode)
