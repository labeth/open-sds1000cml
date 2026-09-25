#!/usr/bin/env python3
"""Bind reviewed JS decoders, comment-only provenance and bounded Node evidence."""
import hashlib
import json
import subprocess
from inventory import ROOT, SOURCE
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
path = ROOT / 'evidence/javascript-decode-source-review.json'
review = json.loads(path.read_text())
overlay = {r['path']: r for r in json.loads((ROOT / 'evidence/javascript-annotation-overlay.json').read_text())['files']}
audit = json.loads((ROOT / 'evidence/javascript-trace-audit.json').read_text())
paths = review['completeReads']
for rel in paths:
    assert review['baselineSHA256'][rel] == overlay[rel]['baselineSHA256']
    assert sha(SOURCE / rel) == overlay[rel]['annotatedSHA256'] == audit['sha256'][rel]
    assert overlay[rel]['exactBaselineRecovery']
assert not [d for d in audit['diagnostics'] if d['path'].split(':')[0] in paths]
symbols = [s for s in audit['symbols'] if s['Path'] in paths]
assert len(symbols) == 60 and all(s['Implements'] == ['REQ-SDS-018'] for s in symbols)
before = {p: sha(SOURCE / p) for p in paths}
helper = ROOT / 'scripts/helpers/javascript-decode-boundaries.cjs'
result = subprocess.run(['node', str(helper), str(SOURCE)], capture_output=True, text=True, timeout=15, check=True)
observed = json.loads(result.stdout)
assert observed['result'] == 'pass' and len(observed['cases']) == 6
assert before == {p: sha(SOURCE / p) for p in paths}
review.update(result='source-reviewed-and-bounded-host-checks-pass', sourceSHA256=before,
              linkedDeclarations=len(symbols), helperSHA256=sha(helper), boundaryChecks=observed,
              qualification='Comment-only provenance and selected Node VM behavior. No browser, electrical or standards qualification; seven reviewed Go tests and 286 selected parity vectors are recorded separately in decode-extended-source-review.json and are integrated into native partial-verification summaries.')
path.write_text(json.dumps(review, indent=2) + '\n')
print('PASS: eight JavaScript decoder files, 60 declaration links and six Node characterizations')
