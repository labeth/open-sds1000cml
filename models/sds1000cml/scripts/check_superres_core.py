#!/usr/bin/env python3
"""Execute unchanged pinned browser stack suite and all 200 adversarial cases."""
import hashlib,json,subprocess,tempfile
from pathlib import Path
from inventory import ROOT,REV,git

def sha(data):return hashlib.sha256(data).hexdigest()
def main():
    names=['superres.js','superres_math.js','superres_template.js','superres_gate.js','superres_measure.js','peaks.js','superres.test.cjs','superres_breaker.cjs']
    sources={name:git('show',f'{REV}:app/internal/web/{name}') for name in names}
    cases=[]
    with tempfile.TemporaryDirectory(prefix='sds-sr-core-') as tmp:
        for name,data in sources.items():(Path(tmp)/name).write_bytes(data)
        for name,command in [('superres.test.cjs',['node','superres.test.cjs']),('superres_breaker.cjs',['node','superres_breaker.cjs','0','50','--json','breaker-results.json'])]:
            run=subprocess.run(command,cwd=tmp,capture_output=True,timeout=300)
            log=ROOT/'evidence/test-runs'/('reviewed-'+name+'.log');log.write_bytes(run.stdout+b'\n--- stderr ---\n'+run.stderr)
            case=dict(source='app/internal/web/'+name,command=command,exitCode=run.returncode,result='pass' if run.returncode==0 else 'fail',log=str(log.relative_to(ROOT)),logSHA256=sha(log.read_bytes()))
            if name=='superres.test.cjs':
                case['assertions']=[line for line in run.stdout.decode().splitlines() if line.startswith(('ok   ','FAIL '))]
                assert case['assertions'] and (b'ALL PASS' in run.stdout or b'FAILURES' in run.stdout)
            else:
                raw=(Path(tmp)/'breaker-results.json').read_bytes();rows=json.loads(raw)
                assert len(rows)==200 and {x['fam'] for x in rows}==set(range(50))
                assert all({x['gate'] for x in rows if x['fam']==n}=={'exact','half','triple','junk'} for n in range(50))
                case['runs']=len(rows);case['failedRuns']=sum(bool(x['fails']) for x in rows)
                assert (run.returncode==0)==(case['failedRuns']==0)
                resultpath=ROOT/'evidence/test-runs/reviewed-superres-breaker-results.json';resultpath.write_bytes(raw)
                case['resultsFile']=str(resultpath.relative_to(ROOT));case['resultsSHA256']=sha(raw)
            cases.append(case)
        assert all((Path(tmp)/name).read_bytes()==data for name,data in sources.items())
    report=dict(commit=REV,result='pass' if all(c['result']=='pass' for c in cases) else 'fail',cases=cases,sourceSHA256={'app/internal/web/'+name:sha(data) for name,data in sources.items()},runnerSHA256=sha(Path(__file__).read_bytes()),nodeVersion=subprocess.check_output(['node','--version'],text=True).strip(),scope='Unchanged original synthetic suite and all 50 families x four gates. Retains adversarial failures and host-dependent 3000 ms per-case timing assertions. No device execution or physical bandwidth, ENOB or timing qualification.')
    (ROOT/'evidence/superres-core-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps({c['source']:{k:v for k,v in c.items() if k in ('result','exitCode','runs','failedRuns')} for c in cases}))
if __name__=='__main__':main()
