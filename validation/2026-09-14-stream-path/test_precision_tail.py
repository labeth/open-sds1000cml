#!/usr/bin/env python3
"""Compare scheduled CIC tail against the unchanged parallel CIC stages."""
from pathlib import Path
import hashlib,json,subprocess,tempfile,sys
root=Path(__file__).resolve().parents[2]
names=('precision_tail.v','precision.v','sim/tb_precision_tail.v')
with tempfile.TemporaryDirectory(prefix='acq-precision-tail-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  source=root/'validation/2026-09-14-stream-path/precision-tail/reference_precision.v' if name=='precision.v' else root/'fpga/acq_sram'/name
  data=source.read_bytes()
  if name=='precision.v':assert hashlib.sha256(data).hexdigest()=='dc03fe4fd3d2daf0d0f757f1f0f9c942793f650f8ab709aba881b95860598b33'
  dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_precision_tail',f'-Ptb_precision_tail.MAX_LOG={12 if "--full" in sys.argv else 8}','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
