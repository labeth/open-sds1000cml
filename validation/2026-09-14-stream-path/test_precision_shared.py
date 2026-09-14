#!/usr/bin/env python3
"""Full arithmetic-path equivalence; FIFO interfaces are ideal simulation models."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('precision.v','precision_tail.v','sim/tb_precision_shared.v')
with tempfile.TemporaryDirectory(prefix='acq-precision-shared-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 reference=(root/'validation/2026-09-14-stream-path/precision-tail/reference_precision.v').read_bytes()
 assert hashlib.sha256(reference).hexdigest()=='dc03fe4fd3d2daf0d0f757f1f0f9c942793f650f8ab709aba881b95860598b33'
 # The faster CIC modules are unchanged; only the adc_precision composition differs.
 current=(root/'fpga/acq_sram/precision.v').read_text()
 assert reference.decode().split('module adc_precision')[0]==current.split('module adc_precision')[0]
 ref='module reference_adc_precision'+reference.decode().split('module adc_precision',1)[1]
 (p/'reference.v').write_text(ref);files.append(str(p/'reference.v'))
 hashes['reference_precision.v']=hashlib.sha256(reference).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_precision_shared','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
