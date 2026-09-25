#!/usr/bin/env python3
"""Publish bounded native Go test outcomes only from verified, indexed evidence."""
import argparse
import hashlib
import json
import re
from collections import defaultdict
from pathlib import Path
from inventory import ROOT, SOURCE, REV, git


def expected():
    read = lambda name: json.loads((ROOT / 'evidence' / name).read_text())
    runs = {'test-runs/' + x['log']: x for x in read('test-runs/results.json')}
    plan = {x['path']: x['functions'] for x in read('annotation-plan.json')['files']}
    overlays = {x['path']: x for x in read('annotation-overlay.json')['files']}
    known = {x['path'] for x in read('source-inventory.json')}
    groups = defaultdict(list)
    logs, modules = {}, {}
    for row in read('reviewed-test-links.json'):
        path, test, log = row['path'], row['test'], row['log']
        assert path.endswith('_test.go') and test.startswith(('Test', 'Fuzz'))
        assert row['requirements'] == plan[path][test]
        assert hashlib.sha256((SOURCE / path).read_bytes()).hexdigest() == overlays[path]['annotatedSHA256'], f'stale source: {path}'
        run = runs[log]
        assert run['commit'] == REV and run['repository'] == 'open-sds1000cml-acq2'
        if log not in logs:
            data = (ROOT / 'evidence' / log).read_bytes()
            assert hashlib.sha256(data).hexdigest() == run['sha256'], f'stale log: {log}'
            logs[log] = [json.loads(line) for line in data.splitlines()]
        module = next(str(parent / 'go.mod') for parent in Path(path).parents if str(parent / 'go.mod') in known)
        if module not in modules:
            modules[module] = re.search(r'^module\s+(\S+)', git('show', f'{REV}:{module}').decode(), re.M).group(1)
        relative = str(Path(path).parent.relative_to(Path(module).parent))
        package = modules[module] + ('' if relative == '.' else '/' + relative)
        terminal = [e for e in logs[log] if e.get('Package') == package and e.get('Test') == test and e['Action'] in ('pass', 'fail', 'skip')]
        assert len(terminal) == 1 and terminal[0]['Action'] == row['result'], f'outcome mismatch: {path}:{test}'
        groups[path].append(row)
    identities = {}
    output = {}
    for path, rows in sorted(groups.items()):
        # Native inference ranks basename keys, then requirement overlap.
        # Permit duplicate basenames only with disjoint requirement sets;
        # check_go_publication additionally verifies exact source attachment.
        identity = re.sub('[^a-z0-9]+', '-', Path(path).stem.lower()).strip('-')
        refs = sorted({ref for row in rows for ref in row['requirements']})
        for other, other_refs in identities.get(identity, []):
            assert set(refs).isdisjoint(other_refs), f'ambiguous native test identity: {path} and {other}'
        identities.setdefault(identity, []).append((path, refs))
        results = []
        for ref in refs:
            outcomes = [row['result'] for row in rows if ref in row['requirements']]
            status = 'fail' if 'fail' in outcomes else ('partial' if 'pass' in outcomes else 'not-run')
            results.append(dict(requirement=ref, status=status, notes=f'{outcomes.count("pass")} reviewed test outcomes pass; {outcomes.count("fail")} fail; {outcomes.count("skip")} skip. This summarizes exercised assertions only, not complete clause coverage or physical qualification. Exact outcomes and indexed logs are in evidence/reviewed-test-links.json.'))
        output[str(Path(path).with_suffix('.json'))] = dict(source=path, commit=REV,
            scope='Reviewed Go test outcomes; passing assertions contribute partial requirement evidence.',
            sourceSHA256=overlays[path]['annotatedSHA256'],
            logs={row['log']: runs[row['log']]['sha256'] for row in rows},
            tests=[dict(name=row['test'], result=row['result'], requirements=row['requirements']) for row in rows], results=results)
    return output


def run(sync=False):
    output = expected()
    base = ROOT / 'test-results/go'
    if sync:
        for name, data in output.items():
            target = base / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(json.dumps(data, indent=2) + '\n')
    actual = {str(p.relative_to(base)) for p in base.rglob('*.json')}
    assert actual == set(output), 'native Go summary file set differs'
    for name, data in output.items():
        assert json.loads((base / name).read_text()) == data, f'native Go summary differs: {name}'
    print(f'PASS: {len(output)} native Go test summaries match exact indexed outcomes')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sync', action='store_true')
    run(parser.parse_args().sync)
