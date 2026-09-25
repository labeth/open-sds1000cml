"""Run only through check_boot_boundaries.py's private namespaces."""
import json, os, subprocess, sys, tempfile
from pathlib import Path

anchor=Path(sys.argv[1])
rows=[]
for case in ['nonzero-confirmation','arbitrary-intent','failed-confirm-write','failed-commands']:
 with tempfile.TemporaryDirectory(prefix='boot-case-') as directory:
  root=Path(directory);ota=root/'ota';ota.mkdir();logs=ota/'logs';logs.mkdir()
  active=ota/'agent.active';active.write_text('B\n')
  confirmed=ota/'agent.confirmed'
  if case=='failed-confirm-write':confirmed.mkdir()
  else:confirmed.write_text('A\n')
  body='sleep 2; exit 7' if case in ['nonzero-confirmation','failed-confirm-write'] else 'exit 1'
  if case=='arbitrary-intent':body='echo arbitrary-unrecognized-value > "$OTA_DIR/agent.intent"; exit 1'
  binary=ota/'agent.B';binary.write_text('#!/bin/sh\n'+body+'\n');binary.chmod(0o755)
  if case=='failed-commands':(root/'commands').write_text('exit 7\n')
  env={k:v for k,v in os.environ.items() if not k.startswith('OTA_')}
  env.update(OTA_USB=str(root),OTA_DIR=str(ota),OTA_BOOT_LOG=str(logs/'boot.log'),OTA_AGENT_A=str(ota/'missing.A'),OTA_AGENT_B=str(binary),OTA_AGENT_RUNS='1',OTA_AGENT_STABLE='1',OTA_AGENT_MAXFAILS='1',OTA_RESPAWN='0')
  result=subprocess.run(['/bin/sh',str(anchor)],env=env,capture_output=True,text=True,timeout=12)
  log=(logs/'boot.log').read_text();assert 'agent-loop-stop' in log,log
  after='directory' if confirmed.is_dir() else confirmed.read_text().strip()
  if case=='nonzero-confirmation':assert after=='B' and 'agent-confirmed slot=B' in log,log
  elif case=='arbitrary-intent':assert active.read_text().strip()=='B' and 'agent-intent arbitrary-unrecognized-value' in log and 'agent-revert' not in log,log
  elif case=='failed-confirm-write':assert after=='directory' and 'agent-confirmed slot=B' in log,log
  else:assert result.returncode==0 and 'commands-done' in log and 'agent-start slot=B' in log,log
  rows.append(dict(case=case,exitCode=result.returncode,bootLog=log,stderr=result.stderr,confirmed=after,active=active.read_text().strip()))
print(json.dumps(rows))
