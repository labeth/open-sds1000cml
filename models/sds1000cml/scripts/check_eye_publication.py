#!/usr/bin/env python3
"""Check that the eye numerical result attaches to its original test source."""
import hashlib
import json
import re
from inventory import ROOT
from javascript_source_provenance import validate

report = json.loads((ROOT / 'evidence/eye-jitter-review-test-run.json').read_text())
for source, digest in report['sourceSHA256'].items():
    validate(source, digest)
for case in report['cases']:
    assert hashlib.sha256((ROOT / case['log']).read_bytes()).hexdigest() == case['logSHA256']
artifact = 'test-results/javascript/eyejitter.test.json'
data = json.loads((ROOT / artifact).read_text())
assert data['logs'] == {report['cases'][0]['log']: report['cases'][0]['logSHA256']}
assert report['cases'][0]['result'] == 'pass' and report['cases'][0]['exitCode'] == 0
assert [(r['requirement'], r['status']) for r in data['results']] == [('REQ-SDS-181', 'partial'), ('REQ-SDS-199', 'partial')]
text = (ROOT / 'generated/ARCHITECTURE.adoc').read_text()
blocks = re.findall(r'\[\[REF_VERIFY_[^\n]+\]\]\n==== [\s\S]*?(?=\n\[\[REF_VERIFY_|\n=== |\Z)', text)
blocks = [re.sub(r'<<[^,>]+,([^>]+)>>', r'\1', block) for block in blocks]
matching = [block for block in blocks if artifact in block]
assert len(matching) == 1, len(matching)
block = re.sub(r'<<[^,>]+,([^>]+)>>', r'\1', matching[0])
assert '../../app/internal/web/eyejitter.test.cjs' in block
assert '|Status |partial\n' in block
assert 'REQ-SDS-199' in block and 'REQ-SDS-181' in block
print('PASS: eye numerical summary attaches exactly once to its original test source, with partial status')
