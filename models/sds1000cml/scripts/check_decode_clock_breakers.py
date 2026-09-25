#!/usr/bin/env python3
"""Run the reviewed UART/SPI/I2C breaker tests without overstating assertions."""
import hashlib
import json
import os
import subprocess
from inventory import ROOT, SOURCE, REV, git

files = ['app/internal/decode/decode_break_' + p + '_test.go' for p in ('uart', 'spi', 'i2c')]
for path in files:
    current = (SOURCE / path).read_bytes()
    baseline = git('show', REV + ':' + path)
    if current != baseline:
        overlays = {r['path']: r for r in json.loads((ROOT / 'evidence/annotation-overlay.json').read_text())['files']}
        row = overlays[path]
        assert hashlib.sha256(baseline).hexdigest() == row['baselineSHA256'] and hashlib.sha256(current).hexdigest() == row['annotatedSHA256'] and row['exactBaselineRecovery'], path
sha = lambda data: hashlib.sha256(data).hexdigest()
def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in sorted((SOURCE / 'app/internal/decode').glob('*.go'))}
before = snapshot()
names = ['TestBreakUartRingyEdges', 'TestBreakUart', 'TestBreakSpi', 'TestBreakSpiSamplingCadence', 'TestBreakI2c']
cmd = ['go', 'test', '-race', '-json', '-count=1', '-timeout=180s', './internal/decode', '-run', '^(' + '|'.join(names) + ')$']
run = subprocess.run(cmd, cwd=SOURCE / 'app', env={**os.environ, 'GOPROXY': 'off'}, capture_output=True, timeout=210)
log = ROOT / 'evidence/test-runs/decode-clock-breakers-reviewed.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = [(e['Test'], e['Action']) for e in events if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')]
assert sorted(n for n, _ in terminal if '/' not in n) == sorted(names), terminal
assert snapshot() == before
findings = {
 'UART': [
  'The main test uses 80 seeded clean cases at five through nine data bits, with automatic baud enabled only on selected even iterations containing at least four words. Separate boundary cases exercise one and sixteen bits.',
  'The ringing test checks exact payload and absence of framing/parity errors on 60 seeded eight-bit captures; ringing injects one through three samples after transitions. One explicit-baud case and one alternating 11/17-sample ambiguity case are also asserted.',
  '66 negative cases cycle over flat/ramp/toggle and stop/parity/data corruption. The corruption assertions allow no output or require error spans if output exists; they do not require OK=false. The main negative loop contains no random-noise class despite the header wording.',
  'Six truncated parity-frame regressions require rejection or flagged output. Historical comments claiming missing parity allowance are stale: current code includes pcNeed in the length guard and separately handles an out-of-range stop sample.',
  'The shortest-record test checks only no panic; the 400-byte record requires exact recovery. Invalid width assertions reject only the conjunction of OK and nonempty bytes. Hostile timing tests use finite values, not NaN/infinity. Synthesized timing is derived from the decoder integer baud, limiting independence.'
 ],
 'SPI': [
  '60 seeded valid cases require exact bytes across chosen CPOL/CPHA, bit order, half-period and one through three bursts. The builder holds data constant across each full bit, so mode coverage tests selected sampling-edge logic rather than all real device setup/hold timing.',
  '66 negative cases include clock-flat/toggle/ramp rejection, no-panic random noise, truncated byte-count checks and a nominal corruption case. That corruption mutates samples [2*h,3*h) within the leading idle of length 4*h; it does not corrupt a sampled payload bit. Its post-decode branch makes no behavioral assertion.',
  'The constant-low all-zero data test explicitly preserves rejection due to absent data amplitude, even with a valid clock. Other cases recover a 1500-byte record and use fifty seeded clean/junk edge inputs.',
  'The main duty-cycle sweep is informational only. Separate cadence subtests assert the partial-first-cycle shape, approximate samples-per-bit within 370..380, asymmetric 2/12 duty and exact final two-byte recovery after an idle gap. The misaligned prefix is unconstrained.',
  'No chip-select or integrity field is observed. Passing cannot establish payload-corruption detection or real hardware timing qualification. eqInts is supplied by the fully reviewed I2C breaker file.'
 ],
 'I2C': [
  '120 seeded transactions require exact starts/stops, seven-bit address, payload and direction. A subset substitutes the ordinary i2cWave helper, which does not preserve randomized ACK/NAK choices. The test never compares decoded ACK/NAK values.',
  '60 remapped/noisy rail cases require exact address/payload and framing. Noise is deliberately kept inside a selected amplitude margin; this is not a general noise-rejection or electrical-timing qualification.',
  'Thirty truncation/no-STOP cases require no confident complete frame and conditional frame-error reporting, with an explicit guard requiring at least twenty cases to reach an open-frame state. The confidence helper checks global presence of START/STOP and absence of frame-error, not transaction-by-transaction ordering.',
  'Sixty random-noise cases and hostile time/threshold calls check only no panic. The latter include NaN/infinite timing and a NaN threshold; no semantic result is asserted. Channel-length mismatch also checks only no panic.',
  'Selected boundary tests compare payload only: 24-code amplitude, repeated payload values and 200-byte record. Subminimum amplitude/clock cases reject only OK with nonempty bytes. No ten-bit addressing, arbitration, multi-master behavior or general repeated-START coverage is established.'
 ]}
report = dict(commit=REV, requirement='REQ-SDS-018', completeReads=files,
              reviewedLines=sum(len(git('show', REV + ':' + p).decode().splitlines()) for p in files),
              sourceSHA256={p:before[p] for p in files}, runtimeInputSHA256=before,
              result='reviewed-host-tests-pass-native-partial-evidence' if run.returncode == 0 and all(a == 'pass' for _,a in terminal) else 'reviewed-host-test-failure',
              command=cmd, environment={'GOPROXY':'off'}, exitCode=run.returncode, stderr=run.stderr.decode(),
              tests=dict(terminal), log=str(log.relative_to(ROOT / 'evidence')), logSHA256=sha(run.stdout), findings=findings,
              supportingReview='decode-implementation-source-review.json includes complete reads of core implementations and decode_test.go, including i2cWave.',
              scope='Five original deterministic Go tests under race detector. No new behavior or source annotations. Passing original outcomes contribute partial native evidence.',
              nextAction='Review Manchester, CAN/CAN-FD, USB and FlexRay breaker files, then integrate the full reviewed breaker batch.')
(ROOT / 'evidence/decode-clock-breakers-source-review.json').write_text(json.dumps(report,indent=2)+'\n')
print(report['result'] + ': ' + str(len(terminal)) + ' terminal outcomes; unchanged source hashes')
if run.returncode: raise SystemExit(run.returncode)
