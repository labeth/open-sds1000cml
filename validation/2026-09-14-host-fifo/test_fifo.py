#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('fpga/acq_sram/host_fifo.v','fpga/acq_sram/sim/tb_host_fifo.v')
with tempfile.TemporaryDirectory(prefix='host-fifo-test-') as directory:
 paths=[];hashes={}
 for name in names:
  data=(root/name).read_bytes();path=Path(directory)/Path(name).name;path.write_bytes(data)
  paths.append(str(path));hashes[name]=hashlib.sha256(data).hexdigest()
 print('source-sha256: '+json.dumps(hashes,sort_keys=True),flush=True)
 for phase,stall in [(p,s) for s in (0,1) for p in (0,1,2,3)]:
  binary=Path(directory)/f'test-{phase}-{stall}'
  subprocess.run(['iverilog','-g2012','-s','tb',f'-Ptb.PHASE={phase}',f'-Ptb.SYNC_STALL={stall}','-o',str(binary)]+paths,check=True)
  result=subprocess.run(['vvp',str(binary)],capture_output=True,text=True,timeout=30)
  print(result.stdout,end='',flush=True);print(result.stderr,end='',flush=True)
  if result.returncode or f'PASS host FIFO phase={phase} sync_stall={stall}:' not in result.stdout:raise SystemExit('FAILED FIFO simulation')
