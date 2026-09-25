#!/usr/bin/env python3
"""Record bounded, reviewed word-protocol breaker evidence before native integration."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV, git

files = ['app/internal/decode/decode_break_' + name + '_test.go'
         for name in ('sent', 'arinc429', 'mil1553')]
sha = lambda data: hashlib.sha256(data).hexdigest()
for path in files:
    current = (SOURCE / path).read_bytes()
    baseline = git('show', REV + ':' + path)
    if current != baseline:
        overlays = {r['path']: r for r in json.loads((ROOT / 'evidence/annotation-overlay.json').read_text())['files']}
        row = overlays[path]
        assert hashlib.sha256(baseline).hexdigest() == row['baselineSHA256'] and hashlib.sha256(current).hexdigest() == row['annotatedSHA256'] and row['exactBaselineRecovery'], path

def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes())
            for p in sorted((SOURCE / 'app/internal/decode').glob('*.go'))}

before = snapshot()
names = ['TestBreakSent', 'TestBreakArinc429', 'TestBreakMil1553']
command = ['go', 'test', '-race', '-json', '-count=1', '-timeout=180s',
           './internal/decode', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(command, cwd=SOURCE / 'app', env={**os.environ, 'GOPROXY': 'off'},
                     capture_output=True, timeout=210)
log = ROOT / 'evidence/test-runs/decode-word-breakers-reviewed.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = [(e['Test'], e['Action']) for e in events
            if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')]
assert before == snapshot(), 'Sources changed during execution'
assert sorted(n for n, _ in terminal if '/' not in n) == sorted(names), terminal
findings = {
 'SENT': [
  '80 seeded round trips vary tick, nibble count, one to three frames, pause, jitter, idle and tick override; fixtures compute CRC using the production sentCRC4 helper, limiting independence.',
  'Noise assertion rejects only more than 25 of 50 captures with OK and at least eight output nibbles. Passing does not prove zero noise acceptance, a probability bound, or even rejection of every short false decode.',
  '150 deliberately incorrect CRC nibbles must be rejected or flagged, not necessarily return OK=false. The malformed-width case requires an error span without requiring rejection. Nyquist and square-wave assertions check output length rather than OK.',
  'Truncation checks forbid a full-length accepted output at one cut; the forty-frame long-record case checks OK only. Ten degenerate configurations check no panic, including selected NaN/infinite values, not semantic validity.',
  'Comments saying the decoder does not calculate CRC are stale: production decode_sent.go computes and checks sentCRC4. No universal false-positive claim is supported.'
 ],
 'ARINC429': [
  '60 seeded clean cases compare labels, data, SSM and raw bytes across explicit/automatic timing and selected thresholds, using existing repository waveform and extraction helpers.',
  '60 single-bit corruptions, 60 truncations, 60 non-signal shapes and 300 fixed-seed noise inputs check absence of confident acceptance under their particular configurations. These finite fixtures do not establish an operational false-positive probability.',
  'Back-to-back words without a gap assert only no panic and output length divisible by four. Degenerate cases exercise 66 arrays times six finite sample intervals times five bitrates; NaN/infinite values are not included.',
  'Header comments describing a missing pulse-density gate are stale: current production code rejects more than 34 hits in a 32-slot word. Test failure messages retain the historical explanation.'
 ],
 'MIL1553': [
  '60 seeded explicit-bitrate captures require exact words and no frame errors. Automatic-bitrate failures increment autoMiss and are only logged; wrong accepted words fail. Thus overall success does not prove every clean capture auto-decodes.',
  'Two named historical regression shapes require automatic recovery for repeated 0xAAAA and five-sample bit periods. Passing those cases does not establish universal timing recovery.',
  'Noise/DC/ramp/toggle/square/truncation/coding-violation cases test explicit and automatic modes through mbConfident, which compares error-span count with word count. Corrupted parity checks use explicit bitrate.',
  'Boundary and sixty seeded hostile-configuration cases check no panic and mutually exclusive OK/error; exact payload comparison is conditional on a non-nil expected word slice. No finite test set proves universal robustness or standards compliance.',
  'The shared waveform and parity builders are repository helpers, and mil1553OddParity uses the production popcount helper. Electrical bus timing and command/response transactions are outside this test scope.'
 ]}
report = dict(commit=REV, requirement='REQ-SDS-018',
              result='reviewed-host-tests-pass-native-partial-evidence' if run.returncode == 0 and all(a == 'pass' for _, a in terminal) else 'reviewed-host-test-failure',
              completeReads=files, sourceSHA256={p: before[p] for p in files},
              runtimeInputSHA256=before, command=command, environment={'GOPROXY': 'off'},
              exitCode=run.returncode, stderr=run.stderr.decode(), tests=dict(terminal),
              log=str(log.relative_to(ROOT / 'evidence')), logSHA256=sha(run.stdout), findings=findings,
              supportingReview='decode-implementation-source-review.json records prior complete reads of the three ordinary protocol test files and production implementations.',
              scope='Full inspection of 1174 lines in three breaker files and execution of exactly their three top-level tests. Ordinary deterministic tests, not Go fuzz targets. Passing original outcomes contribute partial native evidence; disabled assertions remain separate.',
              nextAction='Review the remaining seven breaker files, then batch source annotations and native verification integration with the preserved assertion limits.')
(ROOT / 'evidence/decode-word-breakers-source-review.json').write_text(json.dumps(report, indent=2) + '\n')
print(report['result'] + ': ' + str(len(terminal)) + ' terminal outcomes; unchanged source hashes')
if run.returncode:
    raise SystemExit(run.returncode)
