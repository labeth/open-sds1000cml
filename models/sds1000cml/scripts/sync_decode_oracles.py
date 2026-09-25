#!/usr/bin/env python3
"""REQ-SDS-018: index optional skips only; never credit missing-dependency failures."""
import json
from inventory import ROOT, REV
from oracle_evidence import check
check()
p = ROOT/'evidence/test-runs/results.json'
rows = json.loads(p.read_text())
report = json.loads((ROOT/'evidence/decode-oracle-all-availability.json').read_text())
run = next(r for r in report['availabilityChecks'] if r['environment']['CI_REQUIRE_SIGROK']=='0')
rows = [r for r in rows if r['id'] != 'decode-oracles']
rows.append(dict(id='decode-oracles',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='app',command=report['command'],environment=run['environment'],exitCode=0,result='not-run',log=run['log'].split('/')[-1],sha256=run['logSHA256'],scope='Six reviewed external oracle suites skipped before protocol subtests because sigrok-cli is absent. Native status is not-run; no external comparison, passing protocol credit or physical qualification.'))
p.write_text(json.dumps(rows,indent=2)+'\n')
print('PASS: six optional skips indexed for native not-run summaries')
