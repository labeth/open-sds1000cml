#!/usr/bin/env python3
"""Verify reviewed RTL simulation provenance and publish bounded native result summaries."""
import argparse
import hashlib
import json
from pathlib import Path
from inventory import ROOT, SOURCE, REV
from run_rtl_tests import CASES


def expected():
    evidence = ROOT / 'evidence'
    report = json.loads((evidence / 'test-runs/rtl-reviewed.json').read_text())
    assert report['commit'] == REV and report['result'] == 'pass'
    actual = {r['id']: r for r in report['cases']}
    assert len(actual) == len(report['cases']) and set(actual) == {r['id'] for r in CASES}
    plan = {r['path']: r for r in json.loads((evidence / 'rtl-annotation-plan.json').read_text())['files']}
    links = []
    native = {}
    for case in CASES:
        run = actual[case['id']]
        assert all(run[k] == v for k, v in case.items()), f'case differs: {case["id"]}'
        assert run['compileExitCode'] == run['simulationExitCode'] == 0 and run['result'] == 'pass'
        assert any(line.startswith(case['success']) for line in run['simulationOutput'].splitlines())
        sources = [case['test'], *case['sources']]
        assert set(run['sha256']) == set(sources)
        for path in sources:
            from rtl_source_provenance import validate
            validate(path, run['sha256'][path])
        command = run['compileCommand']
        assert command[:5] == ['iverilog', '-g2012', '-s', case['top'], '-o']
        assert command[6:] == [f'-P{case["top"]}.{k}={v}' for k,v in case.get('parameters', {}).items()] + sources
        assert run['simulationCommand'] == ['vvp', command[5]]
        assert plan[case['test']]['modules'][case['top']] == case['requirements']
        link = dict(path=case['test'], module=case['top'], run=case['id'], requirements=case['requirements'], result='pass', log='test-runs/rtl-reviewed.json')
        links.append(link)
        key = Path(case['test']).stem + '.json'
        artifact = native.setdefault(key, dict(source=case['test'], scope=report['scope'], runs=[], results=[]))
        artifact['runs'].append(case['id'])
        if not artifact['results']:
            artifact['results'] = [dict(requirement=req, status='partial', notes='Reviewed digital simulations pass. Full requirement-clause coverage and physical qualification remain unestablished. See evidence/test-runs/rtl-reviewed.json for exact commands, source hashes and output.') for req in case['requirements']]
    from acquisition_rtl_results import expected as acquisition_expected
    acquisition_links, acquisition_native = acquisition_expected()
    assert not (set(native) & set(acquisition_native)), 'ambiguous RTL result basename'
    links.extend(acquisition_links)
    native.update(acquisition_native)
    from precision_rtl_results import expected as precision_expected
    precision_links, precision_native = precision_expected()
    assert not (set(native) & set(precision_native)), 'ambiguous RTL result basename'
    links.extend(precision_links)
    native.update(precision_native)
    from stream_rtl_results import expected as stream_expected
    stream_links, stream_native = stream_expected()
    assert not (set(native) & set(stream_native)), 'ambiguous RTL result basename'
    links.extend(stream_links)
    native.update(stream_native)
    from board_rtl_results import expected as board_expected
    board_links, board_native = board_expected()
    assert not (set(native) & set(board_native)), 'ambiguous RTL result basename'
    links.extend(board_links)
    native.update(board_native)
    from common_rtl_results import expected as common_expected
    common_links, common_native = common_expected()
    assert not (set(native) & set(common_native)), 'ambiguous RTL result basename'
    links.extend(common_links)
    native.update(common_native)
    from sram_bench_rtl_results import expected as sram_bench_expected
    bench_links, bench_native = sram_bench_expected()
    assert not (set(native) & set(bench_native)), 'ambiguous RTL result basename'
    links.extend(bench_links)
    native.update(bench_native)
    from default_rtl_results import expected as default_expected
    default_links, default_native = default_expected()
    assert not (set(native) & set(default_native)), 'ambiguous RTL result name'
    links.extend(default_links)
    native.update(default_native)
    from timeslice_experiment_results import expected as timeslice_expected
    timeslice_links, timeslice_native = timeslice_expected()
    assert not (set(native) & set(timeslice_native)), 'ambiguous RTL result name'
    links.extend(timeslice_links)
    native.update(timeslice_native)
    return links, native


def run(sync=False):
    links, native = expected()
    evidence = ROOT / 'evidence'
    rows = json.loads((evidence / 'requirement-evidence.json').read_text())
    for row in rows:
        wanted = [{k:r[k] for k in ('path','module','run','result','log')} for r in links if row['requirement'] in r['requirements']]
        if sync:
            if wanted:
                row['reviewedRTLTests'] = wanted
                outcomes = [x['result'] for x in wanted + row.get('reviewedTests', [])]
                row['verification'] = 'fail' if 'fail' in outcomes else ('partial' if 'pass' in outcomes else 'not-run')
                row['verificationNote'] = 'Exact digital outcomes retain executed failures and unavailable simulations; complete clause coverage and physical qualification remain unestablished.'
        else:
            assert row.get('reviewedRTLTests', []) == wanted, f"RTL evidence summary differs: {row['requirement']}"
            outcomes = [x['result'] for x in wanted + row.get('reviewedTests', [])]
            expected_status = 'fail' if 'fail' in outcomes else ('partial' if 'pass' in outcomes else 'not-run')
            assert not wanted or row['verification'] == expected_status
    for name, data in native.items():
        path = ROOT / 'test-results/rtl' / name
        if sync:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(json.dumps(data, indent=2)+'\n')
        else:
            assert json.loads(path.read_text()) == data, f'native result summary differs: {name}'
    if sync:
        (evidence / 'requirement-evidence.json').write_text(json.dumps(rows,indent=2)+'\n')
    print(f'PASS: {len(links)} exact RTL simulation outcomes and {len(native)} native verification summaries')


if __name__ == '__main__':
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sync', action='store_true')
    run(parser.parse_args().sync)
