#!/usr/bin/env python3
"""REQ-SDS-018: validate complete oracle review and dependency-only outcomes."""
import hashlib
import json
from inventory import ROOT, SOURCE, REV, git


def check():
    read = lambda name: json.loads((ROOT/'evidence'/name).read_text())
    sha = lambda data: hashlib.sha256(data).hexdigest()
    covered = set()
    overlays = {r['path']:r for r in read('annotation-overlay.json')['files']}
    for family in ('clock','bus','framed'):
        review = read('decode-oracle-'+family+'-source-review.json')
        assert review['commit'] == REV and review['requirement'] == 'REQ-SDS-018'
        assert review['result'] == 'source-reviewed-external-comparison-not-run'
        assert set(review['completeReads']) == set(review['sourceSHA256'])
        assert not covered.intersection(review['completeReads'])
        count = 0
        for path, digest in review['sourceSHA256'].items():
            baseline = git('show', REV+':'+path)
            assert sha(baseline) == digest, 'review no longer describes baseline: '+path
            count += len(baseline.splitlines())
            current = (SOURCE/path).read_bytes()
            if current != baseline:
                overlay = overlays[path]
                assert overlay['baselineSHA256'] == digest
                assert overlay['annotatedSHA256'] == sha(current)
                assert overlay['exactBaselineRecovery']
        assert count == review['reviewedLines']
        covered.update(review['completeReads'])
    actual = {str(p.relative_to(SOURCE)) for p in (SOURCE/'app/internal/decode').glob('oracle*_test.go')}
    assert covered == actual and len(covered) == 7
    report = read('decode-oracle-all-availability.json')
    assert report['commit'] == REV and report['result'] == 'external-comparisons-not-run'
    assert set(report['runtimeInputSHA256']) == {str(p.relative_to(SOURCE)) for p in (SOURCE/'app/internal/decode').glob('*.go')}
    for path, digest in report['runtimeInputSHA256'].items():
        assert sha((SOURCE/path).read_bytes()) == digest, 'stale oracle run input: '+path
    names = {'TestOracle'+n for n in ('UART','SPI','I2C','CAN','USBLS','FlexRay')}
    modes = set()
    for run in report['availabilityChecks']:
        mode = run['environment']['CI_REQUIRE_SIGROK']
        assert mode in ('0','1') and mode not in modes
        modes.add(mode)
        action, code = ('skip',0) if mode == '0' else ('fail',1)
        assert run['exitCode'] == code and run['tests'] == dict.fromkeys(names,action)
        data = (ROOT/'evidence'/run['log']).read_bytes()
        assert sha(data) == run['logSHA256']
        events = [json.loads(line) for line in data.splitlines()]
        terminal = [e for e in events if e.get('Test') and e['Action'] in ('pass','fail','skip')]
        assert len(terminal) == 6
        assert {e['Test']:e['Action'] for e in terminal} == run['tests']
        assert not any('/' in e.get('Test','') for e in events), 'protocol subtests unexpectedly ran'
        assert len({e['Package'] for e in terminal}) == 1
        message = 'CI_REQUIRE_SIGROK=1 but sigrok-cli is not installed' if mode == '1' else 'sigrok-cli not installed; skipping oracle cross-check'
        assert ''.join(e.get('Output','') for e in events).count(message) == 6
    assert modes == {'0','1'}
    assert not any(r['log'] == 'decode-oracle-all-required-1.jsonl' for r in read('test-runs/results.json')), 'dependency failure indexed as decoder behavior'
    print('PASS: seven complete oracle source reviews and six dependency-only outcomes; no external qualification credit')

if __name__ == '__main__':
    check()
