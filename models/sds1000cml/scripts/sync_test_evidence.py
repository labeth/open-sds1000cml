#!/usr/bin/env python3
"""Join reviewed declarations to exact outcomes in indexed Go JSON logs."""
import json
import re
from pathlib import Path
from inventory import ROOT, REV, git


def sync():
    evidence = ROOT / 'evidence'
    read = lambda name: json.loads((evidence / name).read_text())
    outcomes = {}
    for run in read('test-runs/results.json'):
        if run.get('repository') != 'open-sds1000cml-acq2' or run.get('commit') != REV or not run['log'].endswith('.jsonl'):
            continue
        events = [json.loads(line) for line in (evidence / 'test-runs' / run['log']).read_text().splitlines()]
        seen = set()
        for event in events:
            name = event.get('Test', '')
            if '/' in name or not name.startswith(('Test', 'Fuzz')) or event['Action'] not in ('pass', 'fail', 'skip'):
                continue
            key = (event['Package'], name)
            assert key not in seen, f'ambiguous repeated test in {run["log"]}: {key}'
            seen.add(key)
            outcomes[key] = (event['Action'], 'test-runs/' + run['log'], run['scope'])
    known = {r['path'] for r in read('source-inventory.json')}
    modules = {}
    links = []
    for row in read('annotation-plan.json')['files']:
        path = Path(row['path'])
        if not str(path).endswith('_test.go'):
            continue
        module = next(str(parent / 'go.mod') for parent in path.parents if str(parent / 'go.mod') in known)
        if module not in modules:
            modules[module] = re.search(r'^module\s+(\S+)', git('show', f'{REV}:{module}').decode(), re.M).group(1)
        relative = str(path.parent.relative_to(Path(module).parent))
        package = modules[module] + ('' if relative == '.' else '/' + relative)
        for name, requirements in row['functions'].items():
            if not name.startswith(('Test', 'Fuzz')) or (package, name) not in outcomes:
                continue
            result, log, scope = outcomes[(package, name)]
            links.append(dict(path=str(path), test=name, requirements=requirements,
                              result=result, log=log, scope=scope))
    (evidence / 'reviewed-test-links.json').write_text(json.dumps(links, indent=2) + '\n')
    rows = read('requirement-evidence.json')
    for row in rows:
        tests = [{k:link[k] for k in ('path', 'test', 'result', 'log')}
                 for link in links if row['requirement'] in link['requirements']]
        if tests:
            row['reviewedTests'] = tests
            row['verification'] = 'fail' if any(t['result'] == 'fail' for t in tests + row.get('reviewedRTLTests', [])) else 'partial'
            row['verificationNote'] = 'Exact offline test outcomes are recorded below. Complete clause coverage and any physical qualification remain unestablished.'
    (evidence / 'requirement-evidence.json').write_text(json.dumps(rows, indent=2) + '\n')
    print(f'Synchronized {len(links)} reviewed test outcomes')


if __name__ == '__main__':
    sync()
