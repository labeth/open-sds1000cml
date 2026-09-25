#!/usr/bin/env python3
"""Run original compensation/ETS Node suites from immutable pinned bytes."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data):return hashlib.sha256(data).hexdigest()
def main():
    names=['superres_comp.js','superres_ets.js','superres_comp.test.cjs','superres_ets.test.cjs']
    sources={name:git('show',f'{REV}:app/internal/web/{name}') for name in names}
    helper=ROOT/'scripts/helpers/superres-numerical-boundaries.cjs';cases=[]
    with tempfile.TemporaryDirectory(prefix='sds-sr-numerical-') as tmp:
        for name,data in sources.items():(Path(tmp)/name).write_bytes(data)
        for name in names[2:]:
            command=['node',name];run=subprocess.run(command,cwd=tmp,capture_output=True,timeout=60)
            log=ROOT/'evidence/test-runs'/('reviewed-'+name+'.log');log.write_bytes(run.stdout+b'\n--- stderr ---\n'+run.stderr)
            result='pass' if run.returncode==0 and b'ALL PASS' in run.stdout else 'fail'
            cases.append(dict(source='app/internal/web/'+name,command=command,exitCode=run.returncode,result=result,log=str(log.relative_to(ROOT)),logSHA256=sha(log.read_bytes())))
        boundary=json.loads(subprocess.check_output(['node',str(helper),tmp],text=True,timeout=30))
        assert boundary['result']=='pass' and len(boundary['cases'])==7
        assert all((Path(tmp)/name).read_bytes()==data for name,data in sources.items())
    report=dict(commit=REV,result='pass' if all(c['result']=='pass' for c in cases) else 'fail',cases=cases,boundaryCharacterization=boundary,sourceSHA256={'app/internal/web/'+name:sha(data) for name,data in sources.items()},helperSHA256=sha(helper.read_bytes()),runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip(),scope='Original synthetic Node suites and seven bounded characterizations. Compensation fixtures use the same response function as the inverse, so this is not independent measured calibration. ETS noise statistics are not SINAD ENOB. No device operations.')
    (ROOT/'evidence/superres-numerical-test-run.json').write_text(json.dumps(report,indent=2)+'\n');print(report['result']+': two original suites and seven boundary characterizations recorded')
if __name__=='__main__':main()
