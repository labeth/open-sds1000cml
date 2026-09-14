#!/usr/bin/env python3
"""Frozen SRAM -> shared host RAM readback, with optional entire physical depth."""
from pathlib import Path
import hashlib,json,subprocess,tempfile,sys
root=Path(__file__).resolve().parents[2]
names=('finite_recall.v','ordinal_counter.v','transport.v','host_path.v','host_packer.v',
       'host_fifo.v','host_sink.v','host_ram.v','host_ownership.v','sim/ddr_model.v','sim/tb_finite_recall.v','sim/tb_finite_recall_faults.v')
with tempfile.TemporaryDirectory(prefix='acq-finite-recall-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 modes=[] if '--faults-only' in sys.argv else ([(19,1,1)] if '--full' in sys.argv else [(13,1,0),(13,0,0)])
 for aw,cont,full in modes:
  binary=p/'test'
  subprocess.run(['iverilog','-g2012','-s','tb_finite_recall',f'-Ptb_finite_recall.AW={aw}',
                  f'-Ptb_finite_recall.CONTINUE_READS={cont}',f'-Ptb_finite_recall.FULL_ONLY={full}',
                  '-o',str(binary),*files],check=True)
  subprocess.run(['vvp',str(binary)],check=True,timeout=900)

 binary=p/'faults'
 subprocess.run(['iverilog','-g2012','-s','tb_finite_recall_faults','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=30)
