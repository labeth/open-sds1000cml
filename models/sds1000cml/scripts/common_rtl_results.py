"""Attach bounded evidence from the original common suite to reviewed contracts."""
import hashlib
import json
from pathlib import Path
from inventory import ROOT, SOURCE
from rtl_source_provenance import validate


def expected():
    evidence = ROOT / 'evidence'
    report_path = evidence / 'common-rtl-review-test-run.json'
    report = json.loads(report_path.read_text())
    plan = {row['path']: row for row in json.loads((evidence / 'rtl-annotation-plan.json').read_text())['files']}
    names = {'tb_sync', 'tb_ddio', 'tb_pll', 'tb_lane_in', 'tb_gpmc_slave'}
    assert report['status'] == 'complete' and len(report['cases']) == 5
    assert {c['id'] for c in report['cases']} == names
    assert report['command'] == ['bash', 'fpga/common/sim/run.sh']
    required = {'fpga/common/' + name for name in ['sync.v', 'ddio_pair.v', 'pll_m2.v', 'lane_in.v', 'gpmc_slave.v', 'sim/gpmc_bfm.v', 'sim/run.sh']} | {'fpga/common/sim/' + name + '.v' for name in names}
    assert set(report['runtimeInputSHA256']) == required, 'incomplete common source snapshot'
    for path, sha in report['runtimeInputSHA256'].items():
        validate(path, sha)
    links, native = [], {}
    review = json.loads((evidence / 'common-rtl-source-review.json').read_text())
    for case in report['cases']:
        name = case['id']
        path = 'fpga/common/sim/' + name + '.v'
        assert case['source'] == path and case['sourceSnapshot']
        output = case['output']
        passed = ('[PASS] ' + name) in report['stdout'].splitlines() and any(line.startswith('PASS ' + name) for line in output.splitlines()) and 'FAIL' not in output and 'error' not in output
        result = 'pass' if passed else 'fail'
        assert result == case['result']
        refs = plan[path]['modules'][name]
        limits = ' '.join(row['finding'] for row in review['findings'] if row['path'] == path)
        links.append(dict(path=path,module=name,run=name,requirements=refs,result=result,log=report_path.name))
        native[name + '.json'] = dict(source=path,scope=report['scope'],sourceSHA256=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest(),reportSHA256=hashlib.sha256(report_path.read_bytes()).hexdigest(),runs=[name],results=[dict(requirement=req,status='partial' if passed else 'fail',notes=limits + ' See evidence/common-rtl-review-test-run.json. Exact original-source or approved comment-overlay equivalence is checked.') for req in refs])
    assert report['result'] == ('pass' if report['exitCode'] == 0 and all(x['result'] == 'pass' for x in links) else 'fail')
    return links, native
