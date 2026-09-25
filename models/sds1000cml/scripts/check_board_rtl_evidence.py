#!/usr/bin/env python3
"""Check exact board variants, vendor input hashes and original profile-core recipe."""
import ast,hashlib,json
from pathlib import Path
from inventory import ROOT,SOURCE,REV
from board_rtl_cases import CASES,VENDOR_DIR,VENDOR_FILES,outcome
from rtl_source_provenance import validate

def check():
    evidence=ROOT/'evidence';read=lambda n:json.loads((evidence/n).read_text())
    report=read('board-rtl-review-test-run.json')
    assert report['status']=='complete' and report['commit']==REV
    actual={c['id']:c for c in report['cases']};assert len(actual)==len(CASES)==len(report['cases'])==13
    assert set(actual)=={c['id'] for c in CASES}
    paths={p for c in CASES for p in [c['test'],*c['sources'],'fpga/default/lanemap_seed.vh']}
    assert set(report['runtimeInputSHA256'])==paths
    equivalent=[p for p,h in report['runtimeInputSHA256'].items() if validate(p,h)]
    vendors=['vendor/'+n for n in VENDOR_FILES];assert set(report['vendorInputs'])==set(vendors)
    for name,item in report['vendorInputs'].items():
        expected=str(Path(VENDOR_DIR)/Path(name).name);assert item['source']==expected
        assert hashlib.sha256(Path(expected).read_bytes()).hexdigest()==item['sha256']
    results=[]
    for c in CASES:
        run=actual[c['id']];assert all(run[k]==v for k,v in c.items()),c['id']
        cmd=run['compileCommand'];assert cmd[:7]==['iverilog','-g2012','-s',c['top'],'-I','fpga/default','-o']
        assert cmd[8:]==['-D'+d for d in c['defines']]+[c['test'],*c['sources']]+(vendors if c['vendor'] else [])
        assert run['simulationCommand']==['vvp',cmd[7],*c['plusargs']] and run['sourceSnapshot']
        status=outcome(run);assert run['status']==status and run['result']==('pass' if status=='partial' else 'fail')
        results.append(dict(id=c['id'],path=c['test'],module=c['top'],status=status,log='board-rtl-review-test-run.json'))
    assert report['result']==('pass' if all(x['result']=='pass' for x in report['cases']) else 'fail')
    core=read('board-core-review-test-run.json');script='validation/2026-09-14-profile-core/test_profile.py';mock='fpga/acq_sram/sim/tb_acquisition_path.v'
    assert core['status']=='complete' and core['commit']==REV and core['command']==['python3',script]
    tree=ast.parse((SOURCE/script).read_text());names=ast.literal_eval(next(n.value for n in tree.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='names' for t in n.targets)))
    canonical=lambda n:str((SOURCE/'fpga/acq_sram'/n).resolve().relative_to(SOURCE.resolve()))
    assert set(core['runtimeInputSHA256'])=={canonical(n) for n in names}|{script,mock}
    for p,h in core['runtimeInputSHA256'].items():
        if validate(p,h):equivalent.append(p)
    reported=core['runnerReportedSHA256'];assert set(reported)==set(names)|{'adc_mocks'}
    for n in names:assert reported[n]==core['runtimeInputSHA256'][canonical(n)]
    assert json.loads(core['output'].splitlines()[0])==reported
    derived=(SOURCE/mock).read_text().split('module tb_acquisition_path')[0]
    assert core['derivedMock']['source']==mock and core['derivedMock']['sha256']==reported['adc_mocks']==hashlib.sha256(derived.encode()).hexdigest()
    passed=not core['timedOut'] and core['exitCode']==0 and any(x.startswith('PASS profile core:') for x in core['output'].splitlines())
    assert core['result']==('pass' if passed else 'fail')
    results.append(dict(id='profile-core-original',path='fpga/acq_sram/sim/tb_profile_core.v',module='tb_profile_core',status='partial' if passed else 'fail',log='board-core-review-test-run.json'))
    from check_board_metadata_observation import OBSERVER
    import re
    observed=read('board-metadata-observation.json')
    assert observed['commit']==REV and observed['observerSource']==OBSERVER
    assert observed['observerSHA256']==hashlib.sha256(OBSERVER.encode()).hexdigest()
    subset=[c for c in CASES if c['id'] in ['legacy-base','legacy-interleave']]
    assert set(observed['runtimeInputSHA256'])=={p for c in subset for p in [c['test'],*c['sources'],'fpga/default/lanemap_seed.vh']}
    for p,h in observed['runtimeInputSHA256'].items():
        assert h==report['runtimeInputSHA256'][p]
        if validate(p,h):equivalent.append(p)
    probes={c['id']:c for c in observed['cases']};assert len(probes)==len(observed['cases'])==2 and set(probes)=={c['id'] for c in subset}
    metadata={}
    for c in subset:
        probe=probes[c['id']];cmd=probe['compileCommand']
        assert cmd[:9]==['iverilog','-g2012','-s',c['top'],'-s','board_metadata_observer','-I','fpga/default','-o']
        assert cmd[10:]==['-D'+v for v in c['defines']]+[c['test'],*c['sources'],'observer.v']
        assert probe['simulationCommand']==['vvp',cmd[9]]
        assert probe['compileExitCode']==actual[c['id']]['compileExitCode']==0
        assert probe['simulationExitCode']==actual[c['id']]['simulationExitCode']==1
        assert 'capture metadata' in probe['simulationOutput']
        values=re.findall(r'^OBSERVED metadata length=(\d+) origin=(\d+) position=(\d+)$',probe['simulationOutput'],re.M)
        assert len(values)==1
        metadata[c['id']]=dict(zip(['length','origin','position'],map(int,values[0])))
    output=dict(result='pass',cases=results,annotationEquivalentSources=sorted(set(equivalent)),reportSHA256={n:hashlib.sha256((evidence/n).read_bytes()).hexdigest() for n in ['board-rtl-review-test-run.json','board-core-review-test-run.json','board-metadata-observation.json']},metadataObservations=metadata,scope='Exact flags,sources,vendor library hashes and original profile-core recipe. Failures are retained; continuation requires all37 exact external-oracle values. No physical qualification. Source changes allowed only by exact approved pinned-baseline comment reconstruction.')
    (evidence/'board-rtl-evidence-check.json').write_text(json.dumps(output,indent=2)+'\n')
    print('PASS:14 exact board/profile cases;', {s:sum(x['status']==s for x in results) for s in ['partial','fail','not-run']})
    return output
if __name__=='__main__':check()
