#!/usr/bin/env python3
"""Check native Go outcomes attach to the intended source verification entries."""
import hashlib
import json
import re
from inventory import ROOT


def run():
    source=ROOT/'generated/ARCHITECTURE.adoc'
    text=source.read_text()
    blocks=re.findall(r'\[\[REF_VERIFY_[^\n]+\]\]\n==== [\s\S]*?(?=\n\[\[REF_VERIFY_|\n=== |\Z)',text)
    rows=[]
    for path in sorted((ROOT/'test-results/go').rglob('*.json')):
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
    def chapter(req):
        return re.search(r'^=+ '+re.escape(req)+r'\n([\s\S]*?)(?=^=+ REQ-SDS-\d+\n|\Z)',text,re.M).group(1)
    broker=chapter('REQ-SDS-111')
    broker_labels=re.findall(r'\["([^"\n]*server\.go:[^"\n]*)"\]:::code_element',broker)
    assert broker_labels == ['otactl/server.go: 16'], broker_labels
    rpc=chapter('REQ-SDS-113')
    assert '\n  CODE_REQ_SDS_113_RPC_TEST_GO[' not in rpc, 'RPC test code is presented as production evidence'
    assert 'VERCODE_REQ_SDS_113_' in rpc, 'verification artifact needs separate source node'
    code_labels=re.findall(r'^\s+CODE_[A-Z0-9_]+\[\"([^\"]+)\"\]', text, re.M)
    test_labels=[label for label in code_labels if re.search(r'(?:_test\.go|\.(?:test|spec)\.(?:js|mjs|cjs|ts|tsx)|(?:^|/)tb_[^/:]+\.v|_tb\.v)(?::|$)', label)]
    assert not test_labels, ('test code in production diagram nodes',test_labels)
    browser=chapter('REQ-SDS-023')
    assert 'RTC_FU_APP_WEB_' not in browser, 'test-only web owner gained runtime evidence'
    assert 'binframe.js: 20, 43, 56, 66' in browser and 'binframe_node_test.go: 17' in browser
    report=dict(result='pass',sourceIdentityCheck=dict(requirement='REQ-SDS-111',labels=broker_labels),inputSHA256=hashlib.sha256(source.read_bytes()).hexdigest(),checks=rows,
        scope='Every native Go summary attaches once to its intended source verification entry with the expected aggregate status. This is publication consistency, not behavioral qualification.')
    (ROOT/'evidence/go-publication-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print(f'PASS: {len(rows)} native Go summaries attach to their intended source checks')


if __name__ == '__main__':
    run()
