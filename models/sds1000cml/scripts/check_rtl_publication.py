#!/usr/bin/env python3
"""Check native RTL outcomes attach to the intended source verification entries."""
import hashlib
import json
import re
from inventory import ROOT


def run():
    source=ROOT/'generated/ARCHITECTURE.adoc'
    text=source.read_text()
    blocks=re.findall(r'\[\[REF_VERIFY_[^\n]+\]\]\n==== [\s\S]*?(?=\n\[\[REF_VERIFY_|\n=== |\Z)',text)
    rows=[]
    for path in sorted((ROOT/'test-results/rtl').rglob('*.json')):
        name=str(path.relative_to(ROOT))
        matches=[block for block in blocks if name in block]
        assert len(matches)==1,(name,len(matches))
        block=matches[0]
        data=json.loads(path.read_text())
        statuses={row['status'] for row in data['results']}
        assert statuses <= {'fail','partial','not-run'},name
        expected='fail' if 'fail' in statuses else ('partial' if 'partial' in statuses else 'not-run')
        assert '|Status |'+expected+'\n' in block,name
        # Source references may be cross-linked while preserving their visible path.
        visible_block=re.sub(r'<<[^,>]+,([^>]+)>>', r'\1', block)
        assert '../../'+data['source'] in visible_block,name
        for result in data['results']:
            assert result['requirement'] in block,name
        rows.append(dict(source=data['source'],resultArtifact=name,status=expected,verificationID=block.split(']]')[0][2:]))
    report=dict(result='pass',inputSHA256=hashlib.sha256(source.read_bytes()).hexdigest(),checks=rows,
        scope='Every native RTL summary attaches exactly once to its intended testbench with the expected aggregate status. This checks publication consistency, not hardware qualification.')
    (ROOT/'evidence/rtl-publication-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print(f'PASS: {len(rows)} native RTL summaries attach to their intended testbenches')

if __name__ == '__main__':
    run()
