#!/usr/bin/env python3
"""Check source epoch/control alignment with explicit ADC/CIC interface mocks."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('acquisition_source.v','sim/tb_acquisition_source.v')
with tempfile.TemporaryDirectory(prefix='acq-source-control-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_acquisition_source','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=30)
