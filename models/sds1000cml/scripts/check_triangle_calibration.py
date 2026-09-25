#!/usr/bin/env python3
"""Run or validate offline synthetic tests without invoking hardware workflows."""
import argparse,hashlib,json,re,subprocess,sys
import numpy,scipy
from inventory import ROOT,SOURCE,REV

SRC='tools/hw/adc_sram/triangle_calibrate.py'
TEST=ROOT/'tests/test_triangle_calibration.py'
NAMES=['test_cli_local_records_preserve_hashes_and_limits','test_constant_record_has_unhandled_crossing_failure','test_held_out_correction_does_not_hide_phase_rotation','test_ideal_triangle_reference','test_odd_record_rejected_without_output']
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()


def check(run=False):
    evidence=ROOT/'evidence';path=evidence/'python-triangle-test-run.json';log=evidence/'test-runs/python-triangle.log'
    if run:
        before={SRC:sha(SOURCE/SRC),str(TEST.relative_to(ROOT)):sha(TEST)}
        command=[sys.executable,'-m','unittest','discover','-s',str(TEST.parent),'-p',TEST.name,'-v']
        result=subprocess.run(command,cwd=SOURCE,capture_output=True,text=True,timeout=90)
        text=result.stdout+result.stderr;log.write_text(text)
        tests=re.findall(r'^(test_\w+) \(.*\) \.\.\. ok$',text,re.M)
        report=dict(commit=REV,exitCode=result.returncode,result='pass' if result.returncode==0 and sorted(tests)==NAMES else 'fail',command=command,sourceSHA256=before,runnerSHA256=sha(__file_path()),log=str(log.relative_to(evidence)),logSHA256=sha(log),tests=tests,numpy=numpy.__version__,scipy=scipy.__version__,scope='Five model-owned synthetic checks run the actual offline fitter and CLI with temporary local records. Passing checks provide partial evidence; they characterize the constant-record exception and do not qualify hardware or execute device control scripts.')
        assert before=={SRC:sha(SOURCE/SRC),str(TEST.relative_to(ROOT)):sha(TEST)}
        path.write_text(json.dumps(report,indent=2)+'\n')
        assert report['result']=='pass',text
        index=evidence/'test-runs/results.json';rows=[x for x in json.loads(index.read_text()) if x['id']!='python-triangle']
        rows.append(dict(id='python-triangle',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='.',command=command,exitCode=0,result='pass',log=log.name,sha256=sha(log),scope=report['scope']))
        index.write_text(json.dumps(rows,indent=2)+'\n')
    report=json.loads(path.read_text())
    assert report['commit']==REV and report['result']=='pass' and report['exitCode']==0
    assert sorted(report['tests'])==NAMES and len(report['tests'])==5
    assert report['runnerSHA256']==sha(__file_path()) and report['logSHA256']==sha(log)
    assert report['sourceSHA256']=={SRC:sha(SOURCE/SRC),str(TEST.relative_to(ROOT)):sha(TEST)}
    assert sorted(re.findall(r'^(test_\w+) \(.*\) \.\.\. ok$',log.read_text(),re.M))==NAMES
    native=dict(source='tests/test_triangle_calibration.py',results=[dict(requirement='REQ-SDS-209',status='partial',notes=report['scope'])])
    native_path=ROOT/'test-results/test_triangle_calibration.json'
    if run:native_path.write_text(json.dumps(native,indent=2)+'\n')
    assert json.loads(native_path.read_text())==native
    print('PASS: five exact offline triangle tests; native evidence remains partial')
    return report


def __file_path():
    from pathlib import Path
    return Path(__file__)


if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--run',action='store_true');check(p.parse_args().run)
