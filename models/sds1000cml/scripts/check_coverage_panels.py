#!/usr/bin/env python3
"""Check complete requirement diagrams and LCD source coordinates against the trace matrix."""
import hashlib,json,re
from inventory import ROOT
source=ROOT/'generated/ARCHITECTURE.adoc';text=source.read_text()
chapter=re.search(r'^=+ REQ-SDS-021\n([\s\S]*?)(?=^=+ REQ-SDS-\d+\n|^={1,3} |\Z)',text,re.M).group(1)
blocks=re.findall(r'\[source,mermaid\]\n----\n([\s\S]*?)\n----',chapter)
matrix=json.loads((ROOT/'generated/TRACE-MATRIX.json').read_text())
req=next(r for r in matrix['requirements'] if r['id']=='REQ-SDS-021')
expected={}
for row in req['code']: expected.setdefault(row['path'],set()).add(row['line'])
assert len(blocks)==1, 'LCD requirement must remain one complete diagram'
for requirement in matrix['requirements']:
 section=re.search(r'^=+ '+re.escape(requirement['id'])+r'\n([\s\S]*?)(?=^=+ REQ-SDS-\d+\n|^={1,3} |\Z)',text,re.M)
 assert section, requirement['id']
 diagrams=re.findall(r'\[source,mermaid\]\n----\n([\s\S]*?)\n----',section.group(1))
 assert len(diagrams)==1, (requirement['id'],len(diagrams))
actual={};counts=[];ids=[]
for block in blocks:
 labels=re.findall(r'^\s+(CODE_[A-Z0-9_]+)\["([^"\n]+)"\]:::code_element',block,re.M)
 counts.append(len(labels))
 for node,label in labels:
  path,sep,lines=label.rpartition(':');assert sep and path not in actual,label
  actual[path]={int(x.strip()) for x in lines.split(',')};ids.append(node)
 checks=re.findall(r'^\s+(VERC_[A-Z0-9_]+)\[',block,re.M)
 assert len(checks)==len(req['verifications']), 'verification context lost'
assert actual==expected,(actual,expected)
assert len(ids)==len(set(ids))
report=dict(result='pass',requirement=req['id'],panels=len(blocks),implementationFilesPerPanel=counts,codeCoordinates=sum(map(len,actual.values())),verificationChecks=len(req['verifications']),completeRequirementDiagrams=len(matrix['requirements']),inputSHA256=hashlib.sha256(source.read_bytes()).hexdigest(),matrixSHA256=hashlib.sha256((ROOT/'generated/TRACE-MATRIX.json').read_bytes()).hexdigest(),scope='LCD production source paths/coordinates appear once in one complete diagram and match the canonical matrix; verification context is preserved. Every requirement has exactly one diagram. PDF readability is independently reviewed.')
(ROOT/'evidence/coverage-panel-check.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: Complete requirement diagrams and LCD coordinates match canonical matrix; file counts',counts)
