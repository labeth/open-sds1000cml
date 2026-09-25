#!/usr/bin/env python3
"""Fail closed on source baseline drift, coverage holes and dangling evidence."""
import hashlib,json,re,sys
from pathlib import Path
import yaml
from inventory import ROOT,SOURCE,REV,BRANCH,git

def matching_test_event(events, package, name, expected):
    terminal=[e for e in events if e.get('Package')==package and e.get('Test')==name and e.get('Action') in ('pass','fail','skip')]
    return len(terminal)==1 and terminal[0]['Action']==expected

def javascript_input_matches(path, expected):
    try:
        from javascript_source_provenance import validate
        validate(path, expected)
        return True
    except (AssertionError, KeyError, StopIteration):
        return False


def check():
    failures=[]
    def need(condition,message):
        if not condition:failures.append(message)
    read=lambda p:json.loads((ROOT/p).read_text())
    manifest=read('evidence/source-manifest.json');inventory=read('evidence/source-inventory.json');units=read('evidence/source-units.json')
    actual={}
    for raw in git('ls-tree','-rlz',REV).split(b'\0'):
        if not raw:continue
        header,path=raw.split(b'\t',1);mode,kind,blob,size=header.decode().split();actual[path.decode()]=(blob,int(size))
    need(manifest['commit']==REV,'manifest revision differs from extractor revision')
    need(manifest['branch']==BRANCH,'manifest branch differs from the requested branch')
    need(git('rev-parse',BRANCH).decode().strip()==REV,'source branch has advanced; explicit model rebaseline required')
    need(git('branch','--show-current').decode().strip()==BRANCH,'source checkout is not on the user-selected branch')
    expected={x['path']:(x['gitBlob'],x['bytes']) for x in inventory}
    need(actual==expected,'source inventory does not exactly cover the pinned Git tree')
    need(len(expected)==len(inventory),'duplicate source inventory paths')
    need(hashlib.sha256(json.dumps(inventory,sort_keys=True).encode()).hexdigest()==manifest['inventorySHA256'],'inventory digest mismatch')
    owned=[p for ps in units.values() for p in ps]
    need(len(owned)==len(set(owned)) and set(owned)==set(actual),'ownership must cover every committed path exactly once')
    arch=yaml.safe_load((ROOT/'model/architecture.yml').read_text())['architecture']
    reqdoc=yaml.safe_load((ROOT/'model/requirements.yml').read_text());reqs=reqdoc['requirements']
    need(reqdoc['lintRun']['mode']=='strict','strict requirements lint must remain enabled')
    public=yaml.safe_load((ROOT/'engmod.yml').read_text())['publications'][0]
    need(set(public['requirements'])=={r['id'] for r in reqs},'public export must include every authored requirement')
    ids={x['id'] for xs in arch.values() for x in xs}
    need(set(public['architecture'])==ids,'public export must include every authored architecture entity')
    modelunits={x['id'] for x in arch['functionalUnits']}
    need(modelunits==set(units),'authored units must match the source ownership inventory')
    refs={x['id']:x for x in arch['referencedElements']}
    for r in reqs:
        need(bool(r.get('sourceRefs')),r['id']+': missing source references')
        need(bool(r.get('appliesTo')),r['id']+': missing responsibility')
        for ref in r.get('sourceRefs',[]):need(ref in refs,r['id']+': unknown source '+ref)
        for unit in r.get('appliesTo',[]):need(unit in modelunits,r['id']+': unknown unit '+unit)
    for r in refs.values():
        if r['kind']=='source-artifact':
            need(r['name'] in actual,r['id']+': source not in pinned commit')
            need(r['version']==REV,r['id']+': source revision differs')
    evidence=read('evidence/requirement-evidence.json')
    reviewed=read('evidence/reviewed-test-links.json')
    need({e['requirement'] for e in evidence}=={r['id'] for r in reqs},'requirement evidence must cover every requirement')
    need(len(evidence)==len(reqs),'duplicate requirement evidence rows')
    for e in evidence:
        for src in e['sources']:need(actual.get(src['path'],(None,))[0]==src['gitBlob'],e['requirement']+': evidence blob mismatch')
        for p in e['testCandidates']:need(p in actual,e['requirement']+': missing test candidate '+p)
        linked=[{k:row[k] for k in ('path','test','result','log')} for row in reviewed if e['requirement'] in row['requirements']]
        need(e.get('reviewedTests',[])==linked,e['requirement']+': requirement test summary differs from reviewed links')
        expected_status = ('fail',) if any(t['result']=='fail' for t in linked + e.get('reviewedRTLTests', [])) else ('partial','pass')
        need(not linked or e['verification'] in expected_status,e['requirement']+': test status does not preserve observed outcome')
    for row in read('evidence/source-symbols.json'):
        need(row['path'] in actual,'symbol source missing: '+row['path']);need(row['owner'] in modelunits,'symbol owner missing')
    dependencies=read('evidence/dependencies.json')['rtlInstantiationCandidates']
    expected_rtl={(r['source'],r['target']) for r in dependencies if not r['internal']}
    relationships=yaml.safe_load((ROOT/'model/behavior.yml').read_text())['behavior']['relationships']
    actual_rtl={(r['from'],r['to']) for r in relationships if r.get('description')=='Lexically extracted RTL instantiation; elaboration and variant selection remain separate evidence.'}
    need(expected_rtl==actual_rtl,'unit RTL dependencies differ from scoped module evidence')
    runs=read('evidence/test-runs/results.json')
    for run in runs:
        log=ROOT/'evidence/test-runs'/run['log']
        need(log.is_file(),run['id']+': missing test log')
        if log.is_file():need(hashlib.sha256(log.read_bytes()).hexdigest()==run['sha256'],run['id']+': test log digest mismatch')
    plan={row['path']:row['functions'] for row in read('evidence/annotation-plan.json')['files']}
    modified=set(git('diff','--name-only',REV,'--').decode().splitlines())
    rtl_plan=read('evidence/rtl-annotation-plan.json')['files']
    js_plan=read('evidence/javascript-annotation-plan.json')['files']
    python_plan=read('evidence/python-annotation-plan.json')['files']
    native_sources=yaml.safe_load((ROOT/'engmod.yml').read_text())['inferenceHints']['codeSources']
    for row in python_plan:
        need('../../'+row['path'] in native_sources, 'reviewed Python absent from native scan: '+row['path'])
    for row in js_plan:
        need('../../'+row['path'] in native_sources, 'reviewed JavaScript absent from native scan: '+row['path'])
    need(modified==set(plan)|{r['path'] for r in rtl_plan}|{r['path'] for r in js_plan}|{r['path'] for r in python_plan},'tracked source changes differ from the reviewed annotation overlays')
    linked_logs={}
    for row in reviewed:
        path=row['path'];name=row['test'];logpath=row['log']
        need(row['requirements']==plan.get(path,{}).get(name),path+': reviewed test differs from source annotation plan')
        need(path.endswith('_test.go') and name.startswith(('Test','Fuzz')),path+': verification link is not a test declaration')
        if logpath not in linked_logs:
            linked_logs[logpath]=[json.loads(line) for line in (ROOT/'evidence'/logpath).read_text().splitlines()]
        parts=Path(path).parts
        modules=['/'.join(parts[:i])+'/go.mod' for i in range(len(parts)-1,0,-1)]
        module=next((m for m in modules if m in actual),None)
        need(module is not None,path+': no source module for test')
        if module:
            module_name=re.search(r'^module\s+(\S+)',git('show',f'{REV}:{module}').decode(),re.M).group(1)
            module_dir=str(Path(module).parent)
            relative=str(Path(path).parent.relative_to(module_dir))
            package=module_name+('' if relative=='.' else '/'+relative)
            need(matching_test_event(linked_logs[logpath],package,name,row['result']),path+': claimed test result lacks an exact matching event')
        need(any('test-runs/'+r['log']==logpath for r in runs),path+': linked log has no digest-indexed run')
    from annotations import run as verify_annotations
    verify_annotations()
    from rtl_annotations import run as verify_rtl_annotations
    verify_rtl_annotations()
    from javascript_annotations import run as verify_javascript_annotations
    verify_javascript_annotations()
    from python_annotations import run as verify_python_annotations
    verify_python_annotations()
    from check_triangle_calibration import check as verify_triangle_calibration
    verify_triangle_calibration()
    browser=read('evidence/browser-pure-test-run.json')
    for path,sha in browser['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser pure test source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/browser-pure-boundaries.cjs').read_bytes()).hexdigest()==browser['helperSHA256'], 'stale browser boundary helper')
    export=read('evidence/export-spectrogram-test-run.json')
    for path,sha in export['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale export/spectrogram source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/export-spectrogram-boundaries.cjs').read_bytes()).hexdigest()==export['helperSHA256'], 'stale export/spectrogram helper')
    views=read('evidence/browser-view-review.json')
    for path,sha in views['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser view source: '+path)
    view_run=read('evidence/browser-view-test-run.json')
    need(hashlib.sha256((ROOT/'scripts/helpers/view-glue-boundaries.cjs').read_bytes()).hexdigest()==view_run['helperSHA256'], 'stale view glue helper')
    bode=read('evidence/bode-renderer-test-run.json')
    for path,sha in bode['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale Bode renderer source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/bode-renderer-boundaries.cjs').read_bytes()).hexdigest()==bode['helperSHA256'], 'stale Bode renderer helper')
    engine_bode=read('evidence/engine-bode-test-run.json')
    for path,sha in engine_bode['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale engine Bode source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/engine-bode-boundaries.go.txt').read_bytes()).hexdigest()==engine_bode['helperSHA256'], 'stale engine Bode helper')
    lcd=read('evidence/lcd-foundation-review.json')
    for path,sha in lcd['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale LCD foundation source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/lcd-foundation-boundaries.go.txt').read_bytes()).hexdigest()==lcd['helperSHA256'], 'stale LCD foundation helper')
    lcd_render=read('evidence/lcd-render-test-run.json')
    for path,sha in lcd_render['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale LCD renderer source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/lcd-render-boundaries.go.txt').read_bytes()).hexdigest()==lcd_render['helperSHA256'], 'stale LCD renderer helper')
    for report_name,helper_name in [('lcd-spectrogram-boundary-review.json','lcd-spectrogram-boundaries.go.txt'),('lcd-netaddr-boundary-review.json','lcd-netaddr-boundaries.go.txt'),('lcd-superres-boundary-review.json','lcd-superres-boundaries.go.txt'),('lcd-bode-boundary-review.json','lcd-bode-boundaries.go.txt')]:
        report=read('evidence/'+report_name)
        for path,sha in report['sourceSHA256'].items():
            need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale LCD evidence: '+path)
        need(hashlib.sha256((ROOT/'scripts/helpers'/helper_name).read_bytes()).hexdigest()==report['helperSHA256'], 'stale LCD helper: '+helper_name)
    palette=read('evidence/lcd-palette-test-run.json')
    for path,sha in palette['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale palette source: '+path)
    decoder=read('evidence/decode-acceptance-boundary-review.json')
    for path,sha in decoder['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale decoder source: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/decode-acceptance-boundaries.go.txt').read_bytes()).hexdigest()==decoder['helperSHA256'], 'stale decoder acceptance helper')
    js_decoder=read('evidence/javascript-decode-source-review.json')
    for path,sha in js_decoder['sourceSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale JavaScript decoder: '+path)
    need(hashlib.sha256((ROOT/'scripts/helpers/javascript-decode-boundaries.cjs').read_bytes()).hexdigest()==js_decoder['helperSHA256'], 'stale JavaScript decoder helper')
    extended=read('evidence/decode-extended-source-review.json')
    for path,sha in extended['runtimeInputSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale extended decoder input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/extended['log']).read_bytes()).hexdigest()==extended['logSHA256'], 'stale extended decoder log')
    for family in ('word','clock','framed'):
        report=read('evidence/decode-'+family+'-breakers-source-review.json')
        for path,sha in report['runtimeInputSHA256'].items():
            need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale breaker input: '+path)
        need(hashlib.sha256((ROOT/'evidence'/report['log']).read_bytes()).hexdigest()==report['logSHA256'], 'stale breaker log: '+family)
    usb=read('evidence/decode-framed-breakers-source-review.json')['disabledUSBAssertions']
    need(hashlib.sha256((ROOT/'evidence'/usb['log']).read_bytes()).hexdigest()==usb['logSHA256'], 'stale disabled USB assertion log')
    edit=usb['edit']
    replacement=(SOURCE/edit['path']).read_text().replace(edit['before'],edit['after'])
    need(hashlib.sha256(replacement.encode()).hexdigest()==usb['replacementSHA256'], 'stale USB overlay source')
    need(usb['exitCode']==1 and all(v=='fail' for v in usb['tests'].values()), 'USB characterization unexpectedly credited as passing')
    final_node=read('evidence/final-node-suites-test-run.json')
    for path,sha in final_node['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale final Node suite input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_final_node_suites.py').read_bytes()).hexdigest()==final_node['runnerSHA256'], 'stale final Node suite runner')
    need(final_node['result']=='pass' and len(final_node['cases'])==3 and all(c['result']=='pass' and c['exitCode']==0 for c in final_node['cases']), 'incomplete final Node suites')
    for case in final_node['cases']:
        need(hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256'], 'stale final Node suite log')
    srcore_boundary=read('evidence/superres-core-boundaries.json')
    for path,sha in srcore_boundary['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale superres core boundary input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_superres_core_boundaries.py').read_bytes()).hexdigest()==srcore_boundary['runnerSHA256'], 'stale superres core boundary runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/superres-core-boundaries.cjs').read_bytes()).hexdigest()==srcore_boundary['helperSHA256'], 'stale superres core boundary helper')
    need(srcore_boundary['result']=='pass' and len(srcore_boundary['cases'])==8 and all(c['result']=='pass' for c in srcore_boundary['cases']), 'incomplete superres core boundary characterizations')
    srcore=read('evidence/superres-core-test-run.json')
    for path,sha in srcore['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale superres core input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_superres_core.py').read_bytes()).hexdigest()==srcore['runnerSHA256'], 'stale superres core runner')
    for case in srcore['cases']:
        need(hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256'], 'stale superres core log')
    need(len(srcore['cases'])==2 and srcore['cases'][0]['result']=='pass' and len(srcore['cases'][0]['assertions'])==72, 'incomplete original superres suite')
    breaker=srcore['cases'][1]
    need(hashlib.sha256((ROOT/breaker['resultsFile']).read_bytes()).hexdigest()==breaker['resultsSHA256'], 'stale superres breaker results')
    runs=read(breaker['resultsFile'])
    need(len(runs)==200 and {x['fam'] for x in runs}==set(range(50)), 'incomplete superres breaker coverage')
    need(sum(bool(x['fails']) for x in runs)==6 and breaker['failedRuns']==6 and breaker['result']=='fail' and breaker['exitCode']==1 and srcore['result']=='fail', 'superres failures must remain failed evidence')
    srnum=read('evidence/superres-numerical-test-run.json')
    for path,sha in srnum['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale superres numerical input: '+path)
    for case in srnum['cases']:
        need(hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256'], 'stale superres numerical log')
    need(hashlib.sha256((ROOT/'scripts/check_superres_numerical.py').read_bytes()).hexdigest()==srnum['runnerSHA256'], 'stale superres numerical runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/superres-numerical-boundaries.cjs').read_bytes()).hexdigest()==srnum['helperSHA256'], 'stale superres numerical helper')
    need(len(srnum['cases'])==2 and len(srnum['boundaryCharacterization']['cases'])==7, 'incomplete superres numerical cases')
    srmask=read('evidence/reconstruction-mask-ui-test-run.json')
    for path,sha in srmask['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale reconstruction/mask controller input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_reconstruction_mask_ui.py').read_bytes()).hexdigest()==srmask['runnerSHA256'], 'stale reconstruction/mask runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/reconstruction-mask-ui.test.cjs').read_bytes()).hexdigest()==srmask['helperSHA256'], 'stale reconstruction/mask helper')
    need(srmask['result']=='pass' and len(srmask['cases'])==10 and all(c['result']=='pass' for c in srmask['cases']), 'incomplete reconstruction/mask characterizations')
    features=read('evidence/browser-features-test-run.json')
    for path,sha in features['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser features input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_browser_features.py').read_bytes()).hexdigest()==features['runnerSHA256'], 'stale browser features runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/browser-features.test.cjs').read_bytes()).hexdigest()==features['helperSHA256'], 'stale browser features helper')
    need(features['result']=='pass' and len(features['cases'])==13 and all(c['result']=='pass' for c in features['cases']), 'browser feature checks incomplete')
    controls=read('evidence/browser-control-test-run.json')
    for path,sha in controls['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser control input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_browser_control.py').read_bytes()).hexdigest()==controls['runnerSHA256'], 'stale browser control runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/browser-control.test.cjs').read_bytes()).hexdigest()==controls['helperSHA256'], 'stale browser control helper')
    need(controls['result']=='pass' and len(controls['cases'])==13 and all(c['result']=='pass' for c in controls['cases']), 'browser control checks incomplete')
    traces=read('evidence/browser-traces-test-run.json')
    for path,sha in traces['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser traces input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_browser_traces.py').read_bytes()).hexdigest()==traces['runnerSHA256'], 'stale browser traces runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/browser-traces.test.cjs').read_bytes()).hexdigest()==traces['helperSHA256'], 'stale browser traces helper')
    need(traces['result']=='pass' and len(traces['cases'])==13 and all(c['result']=='pass' for c in traces['cases']), 'browser trace checks incomplete')
    graphics=read('evidence/browser-graphics-test-run.json')
    for path,sha in graphics['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser graphics input: '+path)
    need(hashlib.sha256((ROOT/'scripts/check_browser_graphics.py').read_bytes()).hexdigest()==graphics['runnerSHA256'], 'stale browser graphics runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/browser-graphics.test.cjs').read_bytes()).hexdigest()==graphics['helperSHA256'], 'stale browser graphics helper')
    need(graphics['result']=='pass' and len(graphics['cases'])==15 and all(c['result']=='pass' for c in graphics['cases']), 'browser graphics checks incomplete')
    eye=read('evidence/eye-jitter-review-test-run.json')
    for path,sha in eye['sourceSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale eye jitter input: '+path)
    for case in eye['cases']:
        need(hashlib.sha256((ROOT/case['log']).read_bytes()).hexdigest()==case['logSHA256'], 'stale eye jitter log: '+case['id'])
    need(hashlib.sha256((ROOT/'scripts/check_eye_jitter_review.py').read_bytes()).hexdigest()==eye['runnerSHA256'], 'stale eye jitter runner')
    need(hashlib.sha256((ROOT/'scripts/helpers/eye-jitter-guards.cjs').read_bytes()).hexdigest()==eye['helperSHA256'], 'stale eye jitter guard helper')
    need(len(eye['families'])==50 and [f['fam'] for f in eye['families']]==list(range(50)), 'incomplete eye jitter family set')
    need(eye['failedFamilies']==sum(bool(f['fails']) for f in eye['families']), 'eye jitter failure count mismatch')
    need(len(eye['guardCharacterization']['cases'])==11, 'incomplete eye jitter guard set')
    browser_js=read('evidence/browser-harness-javascript-integration.json')
    need(hashlib.sha256((ROOT/browser_js['sourceReview']).read_bytes()).hexdigest()==browser_js['sourceReviewSHA256'], 'stale reused browser source review')
    need(len(browser_js['files'])==13 and sum(f['linkedDeclarations'] for f in browser_js['files'])==141, 'incomplete browser JavaScript integration')
    for f in browser_js['files']:
        need(javascript_input_matches(f['path'],f['baselineSHA256']), 'stale reviewed browser JavaScript baseline: '+f['path'])
        need(hashlib.sha256((SOURCE/f['path']).read_bytes()).hexdigest()==f['annotatedSHA256'], 'stale reviewed browser JavaScript comments: '+f['path'])
    browser=read('evidence/browser-harness-integrated-test-run.json')
    browser_review=read('evidence/browser-harness-source-review.json')
    for path,sha in browser['runtimeInputSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale browser harness input: '+path)
    browser_log=(ROOT/'evidence'/browser['log']).read_bytes()
    need(hashlib.sha256(browser_log).hexdigest()==browser['logSHA256'], 'stale browser harness log')
    browser_events=[json.loads(line) for line in browser_log.splitlines()]
    need(set(browser['tests'])==set(browser_review['uniqueReviewedTests']) and len(browser['tests'])==22, 'browser harness test set differs')
    for name,status in browser['tests'].items():
        need(matching_test_event(browser_events,'open-sds/app/internal/web',name,status), 'browser terminal mismatch: '+name)
        need(status in ('pass','fail','skip'), 'unknown browser outcome: '+name)
        if status=='skip':need(name in ('TestPerfServe','TestSigrokExportLogicE2E'), 'required browser test skipped: '+name)
        if status=='pass':need(not any('SKIP:' in e.get('Output','') for e in browser_events if e.get('Test')==name), 'hidden browser skip: '+name)
    need(browser['environment'].get('CI_REQUIRE_BROWSER')=='1' and bool(browser['runtime']['launchedExecutables']), 'browser launch provenance missing')
    need(browser['result']==('fail' if 'fail' in browser['tests'].values() else 'pass'), 'browser aggregate outcome differs')
    baseline_browser=read('evidence/browser-harness-review-test-run.json')
    need(sum(v=='fail' for v in baseline_browser['tests'].values())==6, 'original browser failures lost')
    need(hashlib.sha256((ROOT/'evidence'/baseline_browser['log']).read_bytes()).hexdigest()==baseline_browser['logSHA256'], 'original browser log changed')
    otaentries=read('evidence/ota-entrypoints-integrated-test-run.json')
    otareview=read('evidence/ota-entrypoints-source-review.json')
    for path,sha in otaentries['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale OTA entrypoint input: '+path)
    need(otaentries['exitCode']==0 and otaentries['result']=='pass', 'OTA entrypoint host checks failed')
    need(hashlib.sha256((ROOT/'evidence'/otaentries['log']).read_bytes()).hexdigest()==otaentries['logSHA256'], 'stale OTA entrypoint log')
    need(otaentries['tests']==dict.fromkeys(otareview['uniqueReviewedTests'],'pass') and len(otaentries['tests'])==6, 'OTA boot test outcomes differ')
    entries=read('evidence/entrypoints-integrated-test-run.json')
    entryreview=read('evidence/entrypoints-source-review.json')
    for path,sha in entries['runtimeInputSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale entrypoint input: '+path)
    need(entries['result']=='pass' and len(entries['runs'])==3, 'entrypoint host checks incomplete')
    for run in entries['runs']:
        need(run['exitCode']==0 and hashlib.sha256((ROOT/'evidence'/run['log']).read_bytes()).hexdigest()==run['logSHA256'], 'entrypoint execution or log mismatch')
    outcomes=entries['runs'][0]['tests']
    need({k:v for k,v in outcomes.items() if '/' not in k}==dict.fromkeys(entryreview['uniqueReviewedTests'],'pass'), 'entrypoint helper test set differs')
    need(len(outcomes)==7 and all(v=='pass' for v in outcomes.values()), 'entrypoint helper subtest outcomes differ')
    webapi=read('evidence/webapi-test-run.json')
    wabatch=read('evidence/webapi-annotation-batch.json')
    for path,sha in webapi['runtimeInputSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale web API input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/webapi['log']).read_bytes()).hexdigest()==webapi['logSHA256'], 'stale web API log')
    need(webapi['topLevelTests']==dict.fromkeys(wabatch['uniqueReviewedTests'],'pass') and len(webapi['topLevelTests'])==34, 'web API reviewed test set differs')
    need(len(webapi['tests'])==40 and all(s=='pass' for s in webapi['tests'].values()), 'web API subtest outcomes differ')
    need(webapi['result']=='pass' and webapi['exitCode']==0 and webapi['nativeEligibleTests']==wabatch['nativeEligibleTests'], 'web API trace execution incomplete')
    codegen=read('evidence/codegen-test-run.json')
    cgbatch=read('evidence/codegen-annotation-batch.json')
    codegen_rtl_equivalence=[]
    for path,sha in codegen['runtimeInputSHA256'].items():
        current=hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()
        matches=current==sha
        if not matches and path.endswith('.v'):
            from rtl_source_provenance import validate
            try:
                validate(path,sha)
                matches=True
                codegen_rtl_equivalence.append(dict(path=path,baselineSHA256=sha,annotatedSHA256=current,
                    method='Exact approved comment overlay reconstructed from pinned Git bytes; original execution hash retained.'))
            except (AssertionError,KeyError,StopIteration):
                matches=False
        need(matches, 'stale codegen input: '+path)
    (ROOT/'evidence/codegen-rtl-input-equivalence.json').write_text(json.dumps(dict(files=codegen_rtl_equivalence,
        scope='Only approved Verilog comment changes may differ from the frozen codegen input snapshot; other inputs remain byte-exact.'),indent=2)+'\n')
    cglog=(ROOT/'evidence'/codegen['log']).read_bytes()
    need(hashlib.sha256(cglog).hexdigest()==codegen['logSHA256'], 'stale codegen log')
    cgexpected=dict.fromkeys(cgbatch['uniqueReviewedTests'],'pass')
    cgexpected['TestNoDrift']='fail'
    need(codegen['tests']==cgexpected and len(cgexpected)==37, 'codegen outcomes differ')
    need(codegen['result']=='fail' and codegen['exitCode']==1 and b'generated artifacts are stale' in cglog, 'codegen drift failure not preserved')
    need(b'REGMUX PASS' in cglog, 'generated decode simulation did not run')
    need(codegen['nativeEligibleTests']==cgbatch['nativeEligibleTests'] and cgbatch['status']=='integrated', 'codegen trace integration incomplete')
    quartus=read('evidence/quartus-test-run.json')
    qbatch=read('evidence/quartus-annotation-batch.json')
    for path,sha in quartus['runtimeInputSHA256']['source'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale Quartus input: '+path)
    for path,sha in quartus['runtimeInputSHA256']['optionalReports'].items():
        target=Path(path)
        need((hashlib.sha256(target.read_bytes()).hexdigest() if target.exists() else None)==sha, 'stale optional Quartus report: '+path)
    need(hashlib.sha256((ROOT/'evidence'/quartus['log']).read_bytes()).hexdigest()==quartus['logSHA256'], 'stale Quartus log')
    need(set(quartus['tests'])==set(qbatch['uniqueReviewedTests']) and len(quartus['tests'])==22, 'Quartus test set differs')
    need(all(status=='pass' or (status=='skip' and name in ('TestRealReports','TestRealDefaultSTAReport')) for name,status in quartus['tests'].items()), 'unexpected Quartus outcome')
    need(quartus['result']=='pass' and quartus['exitCode']==0, 'Quartus tests failed')
    need(quartus['nativeEligibleTests']==qbatch['nativeEligibleTests'] and qbatch['status']=='integrated', 'Quartus trace integration incomplete')
    diag=read('evidence/diag-test-run.json')
    diagbatch=read('evidence/diag-annotation-batch.json')
    for path,sha in diag['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale diag input: '+path)
    diaglog=(ROOT/'evidence'/diag['log']).read_bytes()
    need(hashlib.sha256(diaglog).hexdigest()==diag['logSHA256'], 'stale diag log')
    expected_diag=dict.fromkeys(diagbatch['uniqueReviewedTests'],'pass')
    expected_diag['TestGpmcSweepPersistApply']='fail'
    need(diag['topLevelTests']==expected_diag and len(expected_diag)==14, 'diag outcomes differ')
    need(diag['result']=='fail' and diag['exitCode']==1 and b'WARNING: DATA RACE' in diaglog, 'diag race failure not preserved')
    need(diag['nativeEligibleTests']==diagbatch['nativeEligibleTests'] and diagbatch['status']=='integrated', 'diag trace integration incomplete')
    sr=read('evidence/superres-test-run.json')
    srbatch=read('evidence/superres-annotation-batch.json')
    for path,sha in sr['runtimeInputSHA256'].items():
        need(javascript_input_matches(path,sha), 'stale superres input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/sr['log']).read_bytes()).hexdigest()==sr['logSHA256'], 'stale superres log')
    need(sr['tests']==dict.fromkeys(srbatch['uniqueReviewedTests'],'pass') and len(sr['tests'])==7, 'superres outcomes differ')
    need(sr['environment'].get('CI_REQUIRE_BROWSER')=='1', 'superres Node parity not required')
    need(sr['nativeEligibleTests']==srbatch['nativeEligibleTests'] and srbatch['status']=='integrated', 'superres trace integration incomplete')
    panel=read('evidence/panel-test-run.json')
    panelbatch=read('evidence/panel-annotation-batch.json')
    for path,sha in panel['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale panel input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/panel['log']).read_bytes()).hexdigest()==panel['logSHA256'], 'stale panel log')
    need(panel['topLevelTests']==dict.fromkeys(panelbatch['uniqueReviewedTests'],'pass') and len(panel['topLevelTests'])==31, 'panel outcomes differ')
    need(panel['nativeEligibleTests']==panelbatch['nativeEligibleTests'] and panelbatch['status']=='integrated', 'panel trace integration incomplete')
    busrun=read('evidence/bus-test-run.json')
    busbatch=read('evidence/bus-annotation-batch.json')
    for path,sha in busrun['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale bus input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/busrun['log']).read_bytes()).hexdigest()==busrun['logSHA256'], 'stale bus log')
    need(busrun['tests']==dict.fromkeys(busbatch['uniqueReviewedTests'],'pass') and len(busrun['tests'])==33, 'bus outcomes differ')
    need(busrun['nativeEligibleTests']==busbatch['nativeEligibleTests'] and busbatch['status']=='integrated', 'bus trace integration incomplete')
    core=read('evidence/engine-core-test-run.json')
    corebatch=read('evidence/engine-core-annotation-batch.json')
    for path,sha in core['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale engine core input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/core['log']).read_bytes()).hexdigest()==core['logSHA256'], 'stale engine core log')
    need(core['tests']==dict.fromkeys(corebatch['uniqueReviewedTests'],'pass') and len(core['tests'])==45, 'engine core outcomes differ')
    need(core['nativeEligibleTests']==corebatch['nativeEligibleTests'] and corebatch['status']=='integrated', 'engine core trace integration incomplete')
    serialzone=read('evidence/engine-serial-zonemask-test-run.json')
    serialbatch=read('evidence/engine-serial-zonemask-annotation-batch.json')
    for path,sha in serialzone['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale serial/zone input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/serialzone['log']).read_bytes()).hexdigest()==serialzone['logSHA256'], 'stale serial/zone log')
    need(serialzone['tests']==dict.fromkeys(serialbatch['uniqueReviewedTests'],'pass') and len(serialzone['tests'])==23, 'serial/zone outcomes differ')
    need(serialzone['nativeEligibleTests']==serialbatch['nativeEligibleTests'] and serialbatch['status']=='integrated', 'serial/zone trace integration incomplete')
    timebase=read('evidence/engine-timebase-test-run.json')
    for path,sha in timebase['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale timebase input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/timebase['log']).read_bytes()).hexdigest()==timebase['logSHA256'], 'stale timebase log')
    need(len(timebase['tests'])==14 and all(v=='pass' for v in timebase['tests'].values()), 'timebase outcomes differ')
    processing=read('evidence/engine-processing-test-run.json')
    for path,sha in processing['runtimeInputSHA256'].items():
        need(hashlib.sha256((SOURCE/path).read_bytes()).hexdigest()==sha, 'stale engine processing input: '+path)
    need(hashlib.sha256((ROOT/'evidence'/processing['log']).read_bytes()).hexdigest()==processing['logSHA256'], 'stale engine processing log')
    need(len(processing['tests'])==27 and all(v=='pass' for v in processing['tests'].values()), 'engine processing outcomes differ')
    from oracle_evidence import check as verify_oracle_evidence
    verify_oracle_evidence()
    from go_evidence import run as verify_go_evidence
    verify_go_evidence()
    from rtl_evidence import run as verify_rtl_evidence
    verify_rtl_evidence()
    report=dict(commit=REV,strict=True,files=len(inventory),units=len(modelunits),requirements=len(reqs),sourceReferences=len(refs),failures=failures,result='fail' if failures else 'pass',scope='Model/source consistency only; does not prove runtime behavior or hardware qualification.')
    (ROOT/'generated/coverage.json').write_text(json.dumps(report,indent=2,sort_keys=True)+'\n')
    print(json.dumps(report,indent=2));return not failures
if __name__=='__main__':sys.exit(0 if check() else 1)
