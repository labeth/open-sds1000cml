"""Attach bounded evidence from the original default suite to reviewed contracts."""
import hashlib
import json
from pathlib import Path
from inventory import ROOT, SOURCE
from rtl_source_provenance import validate


def expected():
    evidence = ROOT / 'evidence'
    report_path = evidence / 'default-rtl-review-test-run.json'
    report = json.loads(report_path.read_text())
    plan = {row['path']: row for row in json.loads((evidence / 'rtl-annotation-plan.json').read_text())['files']}
    names = {'tb_adc_front', 'tb_capture', 'tb_drain', 'tb_diag', 'tb_top'}
    assert report['status'] == 'complete' and len(report['cases']) == 5
    assert {c['id'] for c in report['cases']} == names
    assert report['command'] == ['bash', 'fpga/default/sim/run.sh']
    required = {str(p.relative_to(SOURCE)) for folder in ['fpga/default', 'fpga/common'] for p in (SOURCE/folder).rglob('*') if p.is_file() and p.suffix in {'.v', '.vh', '.sh'}}
    assert set(report['runtimeInputSHA256']) == required, 'incomplete default source snapshot'
    for path, sha in report['runtimeInputSHA256'].items():
        validate(path, sha)
    links, native = [], {}
    review = json.loads((evidence / 'default-rtl-source-review.json').read_text())
    for case in report['cases']:
        name = case['id']
        path = 'fpga/default/sim/' + name + '.v'
        assert case['source'] == path and case['sourceSnapshot']
        output = case['output']
        passed = ('[PASS] ' + name) in report['stdout'].splitlines() and any(line.startswith('PASS ' + name) for line in output.splitlines()) and 'FAIL' not in output and 'error' not in output
        result = 'pass' if passed else 'fail'
        assert result == case['result']
        refs = plan[path]['modules'][name]
        limits = ' '.join(review['findings'])
        links.append(dict(path=path,module=name,run=name,requirements=refs,result=result,log=report_path.name))
        native['default/' + name + '.json'] = dict(source=path,scope=report['scope'],sourceSHA256=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest(),reportSHA256=hashlib.sha256(report_path.read_bytes()).hexdigest(),runs=[name],results=[dict(requirement=req,status='partial' if passed else 'fail',notes=limits + ' See evidence/default-rtl-review-test-run.json. Exact original-source or approved comment-overlay equivalence is checked.') for req in refs])
    assert report['result'] == ('pass' if report['exitCode'] == 0 and all(x['result'] == 'pass' for x in links) else 'fail')
    return links, native
