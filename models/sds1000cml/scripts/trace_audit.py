#!/usr/bin/env python3
"""Inventory remaining declaration trace gaps; --require-complete fails on gaps."""
import argparse
import json
import re
import subprocess
import tempfile
from pathlib import Path

import yaml
from inventory import ROOT, SOURCE, REV, git


def requirement_gaps(requirements, implemented, verified):
    proposed = {id for id, req in requirements.items() if req.get('status') == 'proposed'}
    implementation = sorted(set(requirements) - proposed - implemented)
    verification = sorted({id for id, req in requirements.items() if 'test' in req.get('verificationMethods', [])} - proposed - verified)
    return proposed, implementation, verification


def needs_non_go_review(row):
    # User scope: Go, TypeScript and Verilog. Go is audited separately.
    return (row['category'] != 'historical-validation'
            and Path(row['path']).suffix in ('.ts', '.tsx', '.v', '.sv'))


def blocks_declaration_review(diagnostic):
    # Expression expansion affects build semantics, not lexical module boundaries.
    return not (diagnostic['code'] == 'code.verilog_expression_macro'
                and diagnostic['severity'] == 'warning')


def audit():
    inventory = json.loads((ROOT / 'evidence/source-inventory.json').read_text())
    requirements = {r['id']: r for r in yaml.safe_load((ROOT / 'model/requirements.yml').read_text())['requirements']}
    paths, excluded, non_go = [], [], []
    for row in inventory:
        path = row['path']
        if row['category'] == 'historical-validation':
            continue
        if not path.endswith('.go'):
            if needs_non_go_review(row):
                non_go.append(path)
            continue
        if re.search(rb'^// Code generated .* DO NOT EDIT\.', git('show', f'{REV}:{path}'), re.M):
            excluded.append(dict(path=path, reason='Generated source; trace its generator and input contract.'))
        else:
            paths.append(str(SOURCE / path))
    with tempfile.TemporaryDirectory(prefix='sds-trace-audit-') as tmp:
        helper = Path(tmp) / 'symbols.go'
        helper.write_bytes((ROOT / 'scripts/helpers/go-symbols.go.txt').read_bytes())
        result = subprocess.run(['go', 'run', str(helper)], input=json.dumps(paths),
                                text=True, capture_output=True, check=True)
    rows, type_rows, implemented, verified, invalid = [], [], set(), set(), []
    for file in json.loads(result.stdout):
        path = str(Path(file['path']).relative_to(SOURCE))
        for fn in file['functions'] or []:
            links = sorted(set(re.findall(r'REQ-[A-Z0-9-]+', '\n'.join(
                line for line in fn['doc'].splitlines() if 'TRLC-LINKS:' in line))))
            for ref in links:
                if ref not in requirements:
                    invalid.append(dict(path=path, function=fn['name'], requirement=ref))
            role = 'implementation'
            if path.endswith('_test.go'):
                role = 'test' if fn['name'].startswith('Test') else ('fuzz-target' if fn['name'].startswith('Fuzz') else 'test-support')
                if role in ('test','fuzz-target'):
                    verified.update(links)
            else:
                implemented.update(links)
            rows.append(dict(path=path, function=fn['name'], line=fn['line'], role=role, requirements=links))
        for decl in file.get('types') or []:
            links = sorted(set(re.findall(r'REQ-[A-Z0-9-]+', '\n'.join(
                line for line in decl['doc'].splitlines() if 'TRLC-LINKS:' in line))))
            for ref in links:
                if ref not in requirements:
                    invalid.append(dict(path=path, type=decl['name'], requirement=ref))
            role = 'test-support' if path.endswith('_test.go') else 'implementation'
            if role == 'implementation':
                implemented.update(links)
            type_rows.append(dict(path=path, type=decl['name'], line=decl['line'], role=role, requirements=links))
    missing = [r for r in rows if not r['requirements']]
    missing_types = [r for r in type_rows if not r['requirements']]
    go_unimplemented = sorted(set(requirements) - implemented)
    go_unverified = sorted(set(requirements) - verified)
    rtl = json.loads((ROOT / 'evidence/verilog-trace-audit.json').read_text())
    assert rtl['commit'] == REV
    import hashlib
    for path, sha in rtl['sha256'].items():
        assert hashlib.sha256((SOURCE / path).read_bytes()).hexdigest() == sha, f'stale RTL audit: {path}'
    rtl_plan = {r['path']: r for r in json.loads((ROOT / 'evidence/rtl-annotation-plan.json').read_text())['files']}
    diagnosed = {d['path'].rsplit(':', 1)[0] for d in rtl['diagnostics'] if blocks_declaration_review(d)}
    for symbol in rtl['symbols']:
        for ref in symbol['Implements'] or []:
            if ref not in requirements:
                invalid.append(dict(path=symbol['Path'], module=symbol['Signature'], requirement=ref))
        if symbol['role'] == 'test':
            verified.update(symbol['Implements'] or [])
        elif symbol['role'] == 'implementation':
            implemented.update(symbol['Implements'] or [])
    reviewed_rtl = set(rtl_plan) - diagnosed
    javascript = json.loads((ROOT / 'evidence/javascript-trace-audit.json').read_text())
    assert javascript['commit'] == REV
    expected_js = {r['path'] for r in inventory if r['category'] != 'historical-validation'
                   and Path(r['path']).suffix in ('.js', '.mjs', '.cjs')}
    assert set(javascript['files']) == expected_js == set(javascript['sha256']), 'incomplete JavaScript audit inventory'
    for path, sha in javascript['sha256'].items():
        assert hashlib.sha256((SOURCE / path).read_bytes()).hexdigest() == sha, f'stale JavaScript audit: {path}'
    js_plan = {r['path']: r for r in json.loads((ROOT / 'evidence/javascript-annotation-plan.json').read_text())['files']}
    js_diagnosed = {d['path'].rsplit(':', 1)[0] for d in javascript['diagnostics']}
    reviewed_js = set(js_plan) - js_diagnosed
    for path, row in js_plan.items():
        symbols = [s for s in javascript['symbols'] if s['Path'] == path]
        assert len(symbols) == len(row['declarations']), f'JavaScript annotation extraction mismatch: {path}'
        assert sorted(tuple(s['Implements'] or []) for s in symbols) == sorted(tuple(d['requirements']) for d in row['declarations']), path
        for symbol in symbols:
            for ref in symbol['Implements'] or []:
                if ref not in requirements:
                    invalid.append(dict(path=path, declaration=symbol['Signature'], requirement=ref))
            if path in reviewed_js:
                (verified if symbol['role'] == 'test' else implemented).update(symbol['Implements'] or [])
    non_go = [path for path in non_go if path not in reviewed_rtl | reviewed_js]
    proposed, unimplemented, unverified = requirement_gaps(requirements, implemented, verified)
    report = dict(commit=REV, activeSourceScope='active-source-scope.json', scope='Go function/method and top-level type declaration links, plus reviewed Verilog module and JavaScript named declaration/direct binding links, in pinned nonhistorical source. Grouped types count as one declaration. Constants/variables, anonymous callbacks and top-level statements are not included in declaration totals. Files with no extracted declaration still need explicit review. Proposed requirements do not require current implementation. Test-method requirements require verification links; links alone are not test outcomes.',
                  result='incomplete' if missing or missing_types or non_go or unimplemented or unverified or invalid else 'pass',
                  goFiles=len(paths), declarations=len(rows), linkedDeclarations=len(rows)-len(missing),
                  unlinkedDeclarations=len(missing), excludedGenerated=excluded,
                  typeDeclarations=len(type_rows), linkedTypeDeclarations=len(type_rows)-len(missing_types),
                  unlinkedTypeDeclarations=len(missing_types), typeDeclarationsByFile=type_rows,
                  otherLanguageFilesAwaitingAudit=non_go, invalidLinks=invalid,
                  rtlBuildEvidenceWarnings=[d for d in rtl['diagnostics'] if not blocks_declaration_review(d)],
                  requirementsWithoutGoImplementation=go_unimplemented, requirementsWithoutGoTestLink=go_unverified,
                  requirementsWithoutImplementationLink=unimplemented, testRequirementsWithoutVerificationLink=unverified,
                  proposedRequirements=sorted(proposed), reviewedVerilogFiles=sorted(reviewed_rtl),
                  reviewedJavaScriptFiles=sorted(reviewed_js),
                  linkedJavaScriptDeclarations=sum(len(js_plan[p]['declarations']) for p in reviewed_js),
                  declarationsByFile=rows)
    (ROOT / 'evidence/trace-audit.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps({k: report[k] for k in ('result', 'goFiles', 'declarations', 'linkedDeclarations', 'unlinkedDeclarations', 'typeDeclarations', 'linkedTypeDeclarations', 'unlinkedTypeDeclarations')}, indent=2))
    return report


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--require-complete', action='store_true')
    args = parser.parse_args()
    report = audit()
    raise SystemExit(1 if args.require_complete and report['result'] != 'pass' else 0)
