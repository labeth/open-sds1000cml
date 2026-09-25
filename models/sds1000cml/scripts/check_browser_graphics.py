#!/usr/bin/env python3
"""Run isolated graphics/geometry checks on immutable pinned sources."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data): return hashlib.sha256(data).hexdigest()
def main():
    names=['app_gl.js','app_geom.js','app_canvas.js','app_nav.js']
    sources={name:git('show',f'{REV}:app/internal/web/{name}') for name in names}
    helper=ROOT/'scripts/helpers/browser-graphics.test.cjs'
    with tempfile.TemporaryDirectory(prefix='sds-graphics-') as tmp:
        for name,data in sources.items(): (Path(tmp)/name).write_bytes(data)
        run=subprocess.run(['node',str(helper),tmp],capture_output=True,timeout=30)
        assert run.returncode==0,run.stderr.decode()
        result=json.loads(run.stdout)
        assert result['result']=='pass' and len(result['cases'])==15
        assert all(row['result']=='pass' for row in result['cases'])
        assert all((Path(tmp)/name).read_bytes()==data for name,data in sources.items())
    report=dict(commit=REV,sourceSHA256={'app/internal/web/'+name:sha(data) for name,data in sources.items()},helperSHA256=sha(helper.read_bytes()),runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip(),**result)
    (ROOT/'evidence/browser-graphics-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
    print('PASS: 15 renderer command and geometry cases; no GPU rasterization or device commands')
if __name__=='__main__': main()
