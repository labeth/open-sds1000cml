#!/usr/bin/env python3
"""Compare native Python scanning with the independent pinned AST inventory."""
import hashlib,json,os,re,subprocess,tempfile
from pathlib import Path
from inventory import ROOT as root, SOURCE as repo, REV, git
tool=Path(os.environ.get('ENGINEERING_MODEL_GO_DIR', str(root.parents[2]/'engineering-model-go')))
inventory=json.loads((root/'evidence/python-declaration-inventory.json').read_text())
expected=[]
with tempfile.TemporaryDirectory(prefix='sds-python-scanner-') as tmp:
 staging=Path(tmp)/'source'
 for row in inventory['files']:
  data=git('show',f"{REV}:{row['path']}");assert hashlib.sha256(data).hexdigest()==row['sourceSHA256']
  if (repo/row['path']).read_bytes()!=data:
   from python_source_provenance import validate
   validate(row['path'],row['sourceSHA256'])
  lines=data.decode().splitlines(keepends=True)
  inserts=[min([d['line'],*d['decoratorLines']]) for d in row['declarations']]
  assert len(inserts)==len(set(inserts))
  for declaration in row['declarations']:
   line=declaration['line']+sum(i<=declaration['line'] for i in inserts)
   expected.append((row['path'],line,'CODE-'+re.sub('[^A-Za-z0-9]+','',declaration['name']).upper()))
  for line in sorted(inserts,reverse=True):
   indentation=re.match(r'\s*',lines[line-1]).group()
   lines.insert(line-1,indentation+'# TRLC-LINKS: REQ-AUDIT-001\n')
  target=staging/row['path'];target.parent.mkdir(parents=True,exist_ok=True);target.write_text(''.join(lines))
 helper=Path(tmp)/'scan.go';helper.write_bytes((root/'scripts/helpers/verilog-scan.go.txt').read_bytes())
 run=subprocess.run(['go','run',str(helper),str(staging)],cwd=tool,env={**os.environ,'GOPROXY':'off'},capture_output=True,text=True,check=True)
 actual=json.loads(run.stdout)
 assert not actual['diagnostics'],actual['diagnostics']
 observed=sorted((s['Path'],s['Line'],s['TraceID']) for s in actual['symbols'])
 assert observed==sorted(expected),(observed,expected)
 report=dict(result='pass',commit=inventory['commit'],files=len(inventory['files']),matchedDeclarations=len(expected),diagnostics=actual['diagnostics'],sourceInventorySHA256=hashlib.sha256((root/'evidence/python-declaration-inventory.json').read_bytes()).hexdigest(),scannerSHA256={name:hashlib.sha256((tool/name).read_bytes()).hexdigest() for name in ['go.mod','go.sum','codemap/scan.go','codemap/python.go']},runnerSHA256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),scope='Cross-parser comparison of all pinned nonhistorical Python named declarations against the independent standard-library AST inventory. Synthetic REQ-AUDIT-001 comments exist only in temporary copies. This is parser validation, not authored SDS requirement links or behavioral verification.')
 (root/'evidence/python-scanner-comparison.json').write_text(json.dumps(report,indent=2)+'\n')
 print('PASS:',report['files'],'Python files;',report['matchedDeclarations'],'AST declarations matched with zero scanner diagnostics')
