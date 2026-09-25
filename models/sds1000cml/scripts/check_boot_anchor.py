#!/usr/bin/env python3
"""Exercise unchanged boot tests in private namespaces with stub agents only."""
import hashlib
import json
import os
import subprocess
import tempfile
from pathlib import Path
from inventory import ROOT, SOURCE, REV


def run():
    paths = ['ota/boot/startup.sh', 'ota/boot/boot_test.go', 'ota/boot/doc.go']
    hashes = lambda: {f: hashlib.sha256((SOURCE/f).read_bytes()).hexdigest() for f in paths}
    before = hashes()
    names = ['TestRespawnLoopBounded', 'TestConfirmStableSlot', 'TestAgentSelfUpdateActivatesNewSlot',
             'TestCrashLoopRevertsToConfirmed', 'TestIntentMarkerIsNeutralNoRevert', 'TestCommandsFileRuns']
    env = {k:v for k,v in os.environ.items() if not k.startswith('OTA_')}
    env.update(GOPROXY='off', TMPDIR='/tmp')
    with tempfile.TemporaryDirectory(prefix='sds-boot-review-') as directory:
        binary = Path(directory)/'boot.test'
        subprocess.run(['go','test','-c','-o',str(binary),'./boot'], cwd=SOURCE/'ota', env=env, check=True, timeout=120)
        command = ['go','tool','test2json','-p','open-sds/ota/boot',
                   'bwrap','--unshare-all','--die-with-parent','--new-session',
                   '--ro-bind','/','/','--tmpfs','/tmp','--dev','/dev','--proc','/proc',
                   '--ro-bind',str(binary),'/tmp/boot.test','--chdir',str(SOURCE/'ota/boot'),
                   '/tmp/boot.test','-test.v','-test.timeout=45s','-test.run=^('+'|'.join(names)+')$']
        result = subprocess.run(command,cwd=SOURCE/'ota',env=env,capture_output=True,text=True,timeout=60)
    assert before == hashes(), 'source changed during boot test'
    log = ROOT/'evidence/test-runs/boot-anchor.jsonl'
    log.write_text(result.stdout)
    events = [json.loads(line) for line in result.stdout.splitlines()]
    passed = [e['Test'] for e in events if e.get('Action')=='pass' and e.get('Test') in names]
    report = dict(commit=REV,sourceSHA256=before,command=command,exitCode=result.returncode,
                  stderr=result.stderr,log='test-runs/'+log.name,logSHA256=hashlib.sha256(log.read_bytes()).hexdigest(),
                  reviewedTests=names,passedTests=passed,
                  scope='Unchanged source and six original tests, compiled locally then executed in bubblewrap private user/PID/mount/network namespaces. Root read-only, /tmp private tmpfs, /dev synthetic, inherited OTA_* removed. Stub agents and commands only; fixed /tmp marker is isolated. No vendor mount, device, real agent, factory process or network access. Linux /bin/sh results do not establish BusyBox/device parity.',
                  result='pass' if result.returncode==0 and sorted(passed)==sorted(names) else 'fail')
    (ROOT/'evidence/boot-anchor-test-run.json').write_text(json.dumps(report,indent=2)+'\n')
    assert report['result']=='pass',result.stdout+result.stderr
    index = ROOT/'evidence/test-runs/results.json'
    rows = [r for r in json.loads(index.read_text()) if r['id']!='boot-anchor']
    rows.append(dict(id='boot-anchor',repository='open-sds1000cml-acq2',commit=REV,workingDirectory='ota',
                     command='python3 models/sds1000cml/scripts/check_boot_anchor.py',exitCode=0,result='pass',
                     log=log.name,sha256=report['logSHA256'],scope=report['scope']))
    index.write_text(json.dumps(rows,indent=2)+'\n')
    print('PASS: six original boot tests in isolated namespaces; source unchanged')


if __name__ == '__main__':
    run()
