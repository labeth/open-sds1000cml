#!/usr/bin/env python3
"""Check original suite and failing harness attach to distinct, correct checks."""
import hashlib,json,re
from pathlib import Path
from inventory import ROOT,SOURCE
from javascript_source_provenance import validate
report=json.loads((ROOT/'evidence/superres-core-test-run.json').read_text())
for source,digest in report['sourceSHA256'].items():validate(source,digest)
alias=ROOT/'tests/superres_breaker.cjs'
assert alias.is_symlink() and alias.resolve()==(SOURCE/'app/internal/web/superres_breaker.cjs').resolve()
text=(ROOT/'generated/ARCHITECTURE.adoc').read_text()
blocks=re.findall(r'\[\[REF_VERIFY_[^\n]+\]\]\n==== [\s\S]*?(?=\n\[\[REF_VERIFY_|\n=== |\Z)',text)
blocks=[re.sub(r'<<[^,>]+,([^>]+)>>',r'\1',b) for b in blocks]
for case in report['cases']:
    artifact='test-results/javascript/'+Path(case['source']).with_suffix('.json').name
    data=json.loads((ROOT/artifact).read_text())
    assert data['source']==case['source']
    assert data['sourceSHA256']==hashlib.sha256((SOURCE/case['source']).read_bytes()).hexdigest()
    assert data['logs']=={case['log']:case['logSHA256']}
    assert hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256']
    status='fail' if case['result']=='fail' else 'partial'
    source=data.get('discoveryAlias','../../'+case['source'])
    matches=[b for b in blocks if artifact in b];assert len(matches)==1,(artifact,len(matches))
    block=matches[0];assert source in block and '|Status |'+status+'\n' in block,(source,status)
    for item in data['results']:assert item['status']==status and item['requirement'] in block
    if status=='fail':
        assert data['caseResults']=={case['resultsFile']:case['resultsSHA256']}
        assert hashlib.sha256((ROOT/case['resultsFile']).read_bytes()).hexdigest()==case['resultsSHA256']
print('PASS: original core suite and six-failure adversarial corpus attach separately to their exact test sources')
