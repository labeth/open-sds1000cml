#!/usr/bin/env python3
"""Run original Bode/spectrogram localhost browser fixtures with recorded inputs."""
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
from inventory import ROOT, SOURCE, REV


def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


inventory = json.loads((ROOT / 'evidence/source-inventory.json').read_text())
paths = [row['path'] for row in inventory if row['path'].startswith('app/')]
before = {path: sha(SOURCE / path) for path in paths}
discovery = """
import {pathToFileURL} from 'node:url';
import fs from 'node:fs';
import path from 'node:path';
const h=await import(pathToFileURL(process.argv[1]));
const p=h.findPlaywright();
if (!p) throw Error('No installed Playwright');
const pkg=JSON.parse(fs.readFileSync(path.join(path.dirname(p),'package.json'),'utf8'));
console.log(JSON.stringify({playwright:p,version:pkg.version}));
"""
runtime = json.loads(subprocess.check_output(['node', '--input-type=module', '-e', discovery,
                                             str(SOURCE / 'app/internal/web/scope_po.mjs')], text=True))
runtime['nodeVersion'] = subprocess.check_output(['node', '--version'], text=True).strip()
runtime['goVersion'] = subprocess.check_output(['go', 'version'], text=True).strip()
runtime['packageSHA256'] = sha(Path(runtime['playwright']).parent / 'package.json')
env = {**os.environ, 'GOPROXY': 'off', 'CI_REQUIRE_BROWSER': '1', 'DEBUG': 'pw:browser',
       'PLAYWRIGHT_DIR': str(Path(runtime['playwright']).parents[1])}
command = ['go', 'test', '-json', '-count=1', './internal/web', '-run', '^(TestBodeBrowser|TestSpectrogramBrowser)$']
run = subprocess.run(command, cwd=SOURCE / 'app', env=env, capture_output=True, timeout=120)
log = ROOT / 'evidence/test-runs/browser-views.jsonl'
log.write_bytes(run.stdout)
events = [json.loads(line) for line in run.stdout.splitlines()]
terminal = [e for e in events if e.get('Test') and e['Action'] in ('pass', 'fail', 'skip')]
assert run.returncode == 0, run.stderr.decode() + run.stdout.decode()[-5000:]
assert sorted((e['Test'], e['Action']) for e in terminal) == [('TestBodeBrowser', 'pass'), ('TestSpectrogramBrowser', 'pass')], terminal
output = ''.join(e.get('Output', '') for e in events)
launches = sorted(set(re.findall(r'<launching> ([^\s]+)', output)))
assert launches, 'Playwright launch provenance missing'
runtime['launchedExecutables'] = {path: sha(Path(path)) for path in launches}
helper = ROOT / 'scripts/helpers/view-glue-boundaries.cjs'
boundaries = json.loads(subprocess.check_output(['node', str(helper), str(SOURCE / 'app/internal/web')]))
assert boundaries['result'] == 'pass' and len(boundaries['cases']) == 8
assert before == {path: sha(SOURCE / path) for path in paths}, 'App input changed during browser tests'
report = dict(commit=REV, result='pass', sourceSHA256=before, runtime=runtime,
              sourceGuard='Every pinned app file hashed before and after execution; no application-source mutation observed. External Go dependencies are identified by the pinned module files.',
              originalTests={e['Test']: e['Action'] for e in terminal}, boundaryCharacterization=boundaries,
              helperSHA256=sha(helper),
              scope='Original browser launch flags and synthetic localhost fixtures. Two passing Chromium UI tests, no skips, plus eight isolated view-glue cases. Does not establish physical acquisition, Bode measurement/pixel accuracy, frequency-label correctness or arbitrary UI behavior.')
(ROOT / 'evidence/browser-view-test-run.json').write_text(json.dumps(report, indent=2) + '\n')
index = ROOT / 'evidence/test-runs/results.json'
rows = [r for r in json.loads(index.read_text()) if r['id'] != 'browser-views']
rows.append(dict(id='browser-views', repository='open-sds1000cml-acq2', commit=REV, workingDirectory='app',
                 command='GOPROXY=off CI_REQUIRE_BROWSER=1 DEBUG=pw:browser PLAYWRIGHT_DIR=' + shlex.quote(env['PLAYWRIGHT_DIR']) + ' ' + shlex.join(command),
                 exitCode=0, result='pass', log=log.name, sha256=sha(log), scope=report['scope']))
index.write_text(json.dumps(rows, indent=2) + '\n')
print(f'PASS: two original Chromium tests, eight view-glue cases, {len(paths)} unchanged app input hashes and launched browser provenance')
