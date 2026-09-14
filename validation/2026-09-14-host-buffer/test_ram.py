#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('fpga/acq_sram/host_ram.v','fpga/acq_sram/sim/tb_host_ram.v')
with tempfile.TemporaryDirectory(prefix='host-ram-test-') as directory:
 paths=[];hashes={}
 for name in names:
  data=(root/name).read_bytes();path=Path(directory)/Path(name).name;path.write_bytes(data)
  paths.append(str(path));hashes[name]=hashlib.sha256(data).hexdigest()
 binary=Path(directory)/'test'
 subprocess.run(['iverilog','-g2012','-s','tb','-o',str(binary)]+paths,check=True)
 result=subprocess.run(['vvp',str(binary)],capture_output=True,text=True,timeout=30)
 print('source-sha256: '+json.dumps(hashes,sort_keys=True));print(result.stdout,end='');print(result.stderr,end='')
 if result.returncode or 'PASS host RAM:' not in result.stdout:raise SystemExit('FAILED RAM simulation')
