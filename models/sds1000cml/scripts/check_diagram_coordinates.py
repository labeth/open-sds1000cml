#!/usr/bin/env python3
"""Validate display-only coordinate formatting in generated evidence diagrams."""
import hashlib,json,re
from inventory import ROOT
source=ROOT/'generated/ARCHITECTURE.adoc';text=source.read_text()
labels=re.findall(r'^\s+(?:CODE|VERCODE)_[A-Z0-9_]+\["([^"\n]+)"\]:::code_element',text,re.M)
rows=[]
for label in labels:
 path,sep,suffix=label.rpartition(':')
 if not sep or not re.fullmatch(r'[\d, ]+',suffix):continue
 numbers=[p.strip() for p in suffix.split(',')]
 assert numbers and all(p.isdecimal() for p in numbers),label
 assert label==path+': '+', '.join(numbers),label
 rows.append(dict(path=path,lines=[int(p) for p in numbers]))
assert rows
matrix=json.loads((ROOT/'generated/TRACE-MATRIX.json').read_text())
q=next(r for r in matrix['requirements'] if r['id']=='REQ-SDS-066')
expected=sorted(x['line'] for x in q['code'] if x['path']=='bode.go')
assert expected==[29,37,53,70,79,93,161,172,218]
assert any(r==dict(path='bode.go',lines=expected) for r in rows)
report=dict(result='pass',displayLabels=len(rows),canonicalExample=dict(requirement='REQ-SDS-066',path='bode.go',lines=expected),inputSHA256=hashlib.sha256(source.read_bytes()).hexdigest(),scope='Display labels provide spaces after the filename colon and between complete decimal coordinates; canonical trace matrix example remains unchanged. Actual PDF line placement is independently reviewed.')
(ROOT/'evidence/diagram-coordinate-check.json').write_text(json.dumps(report,indent=2)+'\n')
print(f'PASS: {len(rows)} diagram coordinate labels and unchanged canonical engine coordinates')
