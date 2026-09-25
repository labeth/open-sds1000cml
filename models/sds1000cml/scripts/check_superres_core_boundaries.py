#!/usr/bin/env python3
"""Characterize pinned original browser core without rerunning the full corpus."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data):return hashlib.sha256(data).hexdigest()
names=['superres.js','superres_math.js','superres_template.js','superres_gate.js','superres_measure.js']
sources={name:git('show',f'{REV}:app/internal/web/{name}') for name in names}
helper=ROOT/'scripts/helpers/superres-core-boundaries.cjs'
with tempfile.TemporaryDirectory(prefix='sds-sr-boundaries-') as tmp:
    for name,data in sources.items():(Path(tmp)/name).write_bytes(data)
    report=json.loads(subprocess.check_output(['node',str(helper),tmp],text=True,timeout=30))
    assert report['result']=='pass' and len(report['cases'])==8
    assert all((Path(tmp)/name).read_bytes()==data for name,data in sources.items())
report.update(commit=REV,sourceSHA256={'app/internal/web/'+name:sha(data) for name,data in sources.items()},helperSHA256=sha(helper.read_bytes()),runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip())
(ROOT/'evidence/superres-core-boundaries.json').write_text(json.dumps(report,indent=2)+'\n')
print('PASS: eight pinned-source boundary characterizations')
