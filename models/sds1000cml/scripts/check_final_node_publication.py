#!/usr/bin/env python3
"""Require the three original Node results to attach to their exact test sources."""
import json,re,hashlib
from pathlib import Path
from inventory import ROOT,SOURCE
from javascript_source_provenance import validate
report=json.loads((ROOT/'evidence/final-node-suites-test-run.json').read_text())
for source,digest in report['sourceSHA256'].items():validate(source,digest)
text=(ROOT/'generated/ARCHITECTURE.adoc').read_text()
blocks=re.findall(r'\[\[REF_VERIFY_[^\n]+\]\]\n==== [\s\S]*?(?=\n\[\[REF_VERIFY_|\n=== |\Z)',text)
blocks=[re.sub(r'<<[^,>]+,([^>]+)>>',r'\1',b) for b in blocks]
for case in report['cases']:
    artifact='test-results/javascript/'+Path(case['source']).with_suffix('.json').name
    data=json.loads((ROOT/artifact).read_text())
    assert case['result']=='pass' and case['exitCode']==0
    assert hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256']
    assert data['logs']=={case['log']:case['logSHA256']}
    assert data['sourceSHA256']==hashlib.sha256((SOURCE/case['source']).read_bytes()).hexdigest()
    matches=[b for b in blocks if artifact in b];assert len(matches)==1,(artifact,len(matches))
    b=matches[0];assert '../../'+case['source'] in b and '|Status |partial\n' in b
    for item in data['results']:assert item['status']=='partial' and item['requirement'] in b
print('PASS: three original frame/peak/export summaries attach to their exact Node test sources')
