#!/usr/bin/env python3
"""Characterize pinned browser client controls and polling offline."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data):return hashlib.sha256(data).hexdigest()
def main():
    names=['app.js','app_core.js','app_controls.js']
    sources={name:git('show',f'{REV}:app/internal/web/{name}') for name in names}
    helper=ROOT/'scripts/helpers/browser-control.test.cjs'
    with tempfile.TemporaryDirectory(prefix='sds-control-') as tmp:
        for name,data in sources.items():(Path(tmp)/name).write_bytes(data)
        p=subprocess.run(['node',str(helper),tmp],capture_output=True,timeout=30)
        assert p.returncode==0,p.stderr.decode()
        result=json.loads(p.stdout)
        assert result['result']=='pass' and len(result['cases'])==13 and all(c['result']=='pass' for c in result['cases'])
        assert all((Path(tmp)/name).read_bytes()==data for name,data in sources.items())
    report=dict(commit=REV,sourceSHA256={'app/internal/web/'+n:sha(b) for n,b in sources.items()},helperSHA256=sha(helper.read_bytes()),runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip(),**result)
    (ROOT/'evidence/browser-control-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
    print('PASS: 13 bounded client control/polling characterizations, including known limitations')
if __name__=='__main__':main()
