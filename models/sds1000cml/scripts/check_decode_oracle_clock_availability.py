#!/usr/bin/env python3
"""Record missing-sigrok behavior for the reviewed UART/SPI oracle tests."""
import hashlib
import json
import os
import shutil
import subprocess
from inventory import ROOT, SOURCE, REV, git

files = ['app/internal/decode/oracle_test.go', 'app/internal/decode/oracle_uart_test.go', 'app/internal/decode/oracle_spi_test.go']
sha = lambda data: hashlib.sha256(data).hexdigest()
for rel in files:
    assert (SOURCE / rel).read_bytes() == git('show', REV + ':' + rel), rel
binary = shutil.which('sigrok-cli')
assert binary is None, 'sigrok-cli is now available; replace this missing-runner probe with actual oracle execution'
def snapshot():
    return {str(p.relative_to(SOURCE)): sha(p.read_bytes()) for p in sorted((SOURCE / 'app/internal/decode').glob('*.go'))}
before = snapshot()
cmd = ['go','test','-race','-json','-count=1','-timeout=30s','./internal/decode','-run','^TestOracle(UART|SPI)$']
runs = []
for required, action, exit_code in [('0','skip',0),('1','fail',1)]:
    settings = {'GOPROXY':'off', 'CI_REQUIRE_SIGROK':required}
    proc = subprocess.run(cmd,cwd=SOURCE/'app',env={**os.environ,**settings},capture_output=True,timeout=60)
    events = [json.loads(line) for line in proc.stdout.splitlines()]
    terminal = {e['Test']:e['Action'] for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')}
    assert terminal == {'TestOracleUART':action,'TestOracleSPI':action}, terminal
    assert proc.returncode == exit_code, proc.stderr.decode()
    output = ''.join(e.get('Output','') for e in events)
    assert ('CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed' if required=='1' else 'sigrok-cli not installed; skipping oracle cross-check') in output
    log = ROOT / ('evidence/test-runs/decode-oracle-clock-required-' + required + '.jsonl')
    log.write_bytes(proc.stdout)
    runs.append(dict(environment=settings,exitCode=proc.returncode,tests=terminal,log=str(log.relative_to(ROOT/'evidence')),logSHA256=sha(proc.stdout),stderr=proc.stderr.decode()))
assert before == snapshot()
findings = {
 'harness': [
  'Each top-level test calls needSigrok before any protocol subtest. Absence skips the whole protocol unless CI_REQUIRE_SIGROK=1, which fails immediately; neither outcome executes protocol comparisons.',
  'Both decoders consume synthetic logic, encoded as CSV for sigrok and fixed ADC codes 56/200 for repository code. Timeline duration boundaries are floored. This does not exercise analog acquisition or electrical behavior.',
  'Annotation classes are queried separately; unmatched output lines are silently ignored. Numeric position conversion errors are discarded. Missing annotations can be caught by positive length/payload assertions, but empty-output negative assertions alone cannot establish successful parser/class interpretation.',
  'eqAligned compares span counts and both endpoints within caller tolerances. UART/SPI selected checks allow one bit period; optional unequal endpoint tolerances are unused here. The external subprocess has no internal context deadline, while the enclosing Go test command has a timeout.'
 ],
 'UART': [
  'Source assertions compare selected 7/8/9-bit payloads against generated expectations and sigrok, with separate parity/framing errors, inferred baud and ringing cases. Wider 10..16-bit values are outside this oracle scope.',
  'oracleUARTBits always adds (1+gapBits) idle bit-times after the stop bit. Therefore the case named back-to-back with gapBits=0 has an extra idle bit and does not test minimum stop-to-start spacing, despite its comment.',
  'Parity and frame-error location assertions use broad frame/bit windows rather than exact coordinates. Only selected clean data and BREAK-adjacent spans use eqAligned.',
  'BREAK is an intentional behavioral distinction: repository output is expected to include a zero pseudo-frame with one frame-error and no break-specific span; sigrok is expected to add a dedicated break annotation. These expectations are inspected, not externally executed on this host.',
  'The ringing fixture requires at least twenty mutated transitions, inferred samples-per-bit 24..26, no error spans and exact payload. It uses one constructed waveform, not a measured electrical ringing distribution.'
 ],
 'SPI': [
  'Selected tests compare data-span values for all four mode combinations, both bit orders, continuous/fractional clocks and idle gaps. They compare span coordinates only on selected cases; they do not uniformly assert Result.Bytes against data-span values.',
  'The mixed-payload case containing 0x00 and 0xFF still has overall DATA transitions. It does not establish decoding of an entirely constant DATA line; the reviewed breaker explicitly records that limitation.',
  'Byte-boundary gaps 1.2x and 2.0x are checked against 1.5 times the actual estimated period. Such boundaries reset no partial word; a separate 1.4x mid-word pause tests assembly across the smaller gap.',
  'The long mid-word gap deliberately expects different payloads: repository discards the partial prefix and produces A5 3C, while sigrok without CS stitches clocks into FA 53. This is explicit contract divergence rather than universal oracle equality.',
  'Generators derive aligned ideal binary timelines and supply no chip-select. No general CS behavior, setup/hold limits, metastability, fault detection or hardware timing qualification is established.'
 ]}
report = dict(commit=REV,requirement='REQ-SDS-018',result='source-reviewed-external-comparison-not-run',completeReads=files,
 reviewedLines=sum(len((SOURCE/p).read_text().splitlines()) for p in files),sourceSHA256={p:before[p] for p in files},runtimeInputSHA256=before,
 executable=binary,command=cmd,availabilityChecks=runs,findings=findings,
 scope='Complete inspection of common harness and UART/SPI oracle files. Two expected skips and two required-dependency failures establish availability gating only. No protocol oracle subtest executed, no passing qualification or native test credit.',
 nextAction='Review I2C, CAN, USB and FlexRay oracle source files before batching annotations and explicit not-run verification records. Actual external execution requires sigrok-cli availability.')
(ROOT/'evidence/decode-oracle-clock-source-review.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: expected optional skips and required-dependency failures; UART/SPI external comparisons not run')
