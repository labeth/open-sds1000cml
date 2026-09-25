#!/usr/bin/env python3
"""Run unchanged pinned frame, peak and export Node regression suites."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data):return hashlib.sha256(data).hexdigest()
names=[stem+ext for stem in ['binframe','peaks','sigrok_export'] for ext in ['.js','.test.cjs']]
sources={n:git('show',f'{REV}:app/internal/web/{n}') for n in names};cases=[]
with tempfile.TemporaryDirectory(prefix='sds-final-node-') as tmp:
    for n,b in sources.items():(Path(tmp)/n).write_bytes(b)
    for n in names:
        if not n.endswith('.test.cjs'):continue
        run=subprocess.run(['node',n],cwd=tmp,capture_output=True,timeout=60)
        log=ROOT/'evidence/test-runs'/('reviewed-'+n+'.log');log.write_bytes(run.stdout+b'\n--- stderr ---\n'+run.stderr)
        cases.append(dict(source='app/internal/web/'+n,exitCode=run.returncode,result='pass' if run.returncode==0 and b'ALL PASS' in run.stdout else 'fail',log=str(log.relative_to(ROOT)),logSHA256=sha(log.read_bytes())))
    assert all((Path(tmp)/n).read_bytes()==b for n,b in sources.items())
report=dict(commit=REV,result='pass' if all(c['result']=='pass' for c in cases) else 'fail',cases=cases,sourceSHA256={'app/internal/web/'+n:sha(b) for n,b in sources.items()},runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip(),scope='Three original synthetic Node suites. No real browser interaction, external libsigrok reader, physical calibration or complete malformed-input proof.')
(ROOT/'evidence/final-node-suites-test-run.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps({c['source']:c['result'] for c in cases}))
