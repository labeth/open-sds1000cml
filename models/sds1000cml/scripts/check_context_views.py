#!/usr/bin/env python3
"""Check exact boundary memberships and reviewed browser bootstrap connections."""
import hashlib,json,re
import yaml
from inventory import ROOT,SOURCE
from check_publication_views import contents


def graph(name):
    path=ROOT/'generated/views'/f'{name}.mmd'
    text=path.read_text()
    nodes,_=contents(text)
    edges=[]
    for line in text.splitlines():
        match=re.match(r'^(N_\w+)\s+(?:-->|-\.->|==>)(?:\|.*?\|)?\s+(N_\w+)',line)
        if match:edges.append(match.groups())
    return nodes,edges,hashlib.sha256(path.read_bytes()).hexdigest()


def node(value):return 'N_'+value.replace('-','_')


def check():
    assurance=yaml.safe_load((ROOT/'model/assurance.yml').read_text())['assurance']
    expected={(node(m),node(b['id'])) for b in assurance['trustBoundaries'] for m in b['members']}
    nodes,edges,security_hash=graph('VIEW-SECURITY')
    assert set(edges)==expected and len(edges)==len(expected), 'security membership projection differs from authored assurance'
    assert nodes=={n for edge in expected for n in edge}, 'security view lost or added members'
    review=json.loads((ROOT/'evidence/browser-bootstrap-review.json').read_text())
    overlays={row['path']:row for row in json.loads((ROOT/'evidence/annotation-overlay.json').read_text())['files']}
    for path,sha in review['sources'].items():
        current=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()
        if current==sha:continue
        if path.endswith(('.js', '.mjs', '.cjs')):
            from javascript_source_provenance import validate
            validate(path, sha)
            continue
        overlay=overlays.get(path,{})
        assert overlay.get('baselineSHA256')==sha and overlay.get('annotatedSHA256')==current and overlay.get('exactBaselineRecovery'), 'stale browser review: '+path
    browser_nodes,browser_edges,browser_hash=graph('VIEW-SYSTEM')
    authored_views=yaml.safe_load((ROOT/'model/views.yml').read_text())['views']
    assert [v['id'] for v in authored_views if v['kind']=='architecture-intent']==['VIEW-SYSTEM'], 'architecture intent must be one complete system view'
    architecture=yaml.safe_load((ROOT/'model/architecture.yml').read_text())['architecture']
    complete_system={node(e['id']) for k in ('functionalGroups','functionalUnits','actors','interfaces','dataObjects','deploymentTargets','hardwareItems','hardwareInterfaces') for e in architecture.get(k,[])}
    assert complete_system <= browser_nodes, 'complete system view lost authored entities: '+str(sorted(complete_system-browser_nodes))
    expected_calls={(node(x['from']),node(x['to'])) for x in review['authoredRelationships']}
    assert expected_calls <= set(browser_edges), 'reviewed browser calls missing from projection'
    reachable={node('IF-HTTP')}
    while True:
        before=set(reachable)
        for a,b in browser_edges:
            if a in reachable or b in reachable:reachable.update([a,b])
        if before==reachable:break
    assert {node(x) for x in ['FU-WEB-APP','FU-WEB-APP-GL','FU-WEB-APP-INIT','FU-WEB-APP-CORE']} <= reachable, 'browser bootstrap graph disconnected from HTTP API'
    document=(ROOT/'generated/ARCHITECTURE.adoc').read_text()
    views=yaml.safe_load((ROOT/'model/views.yml').read_text())['views']
    for view in views:
        chapter=document.split('[[SEC_VIEW_'+view['id']+']]',1)[1].split('[[SEC_VIEW_',1)[0]
        scope='*Authored scope and limitations:* '+view.get('authoredStatusExplanation','No authored status explanation provided.').strip()
        assert scope in chapter, 'authored view scope missing: '+view['id']
        if '=== Authored View Diagram' in chapter:
            assert chapter.index(scope)<chapter.index('=== Authored View Diagram'), 'view scope follows diagram'
    report=dict(result='pass',security=dict(nodes=len(nodes),memberships=len(edges),sha256=security_hash),browser=dict(nodes=len(browser_nodes),relationships=len(browser_edges),reviewedCalls=len(expected_calls),sha256=browser_hash),scope='Source-checked graph projection and membership consistency only; no browser execution, authentication, enforcement, complete threat assessment or device qualification.')
    (ROOT/'evidence/context-view-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print('PASS: exact security memberships and reviewed browser bootstrap/API connections')


if __name__=='__main__':check()
