#!/usr/bin/env python3
"""Exercise producer selection over one actual host buffer and SRAM transport."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('capture_path.v','capture_engine.v','stream_engine.v','finite_recall.v','ingress_path.v','ingress_fifo.v',
       'ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v','transport.v',
       'host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v',
       'sim/ddr_model.v','sim/tb_capture_engine.v')
with tempfile.TemporaryDirectory(prefix='acq-shared-engine-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 for enabled,wrapped in ((1,0),(1,1),(0,0),(0,1)):
  binary=p/f'test{enabled}-{wrapped}'
  subprocess.run(['iverilog','-g2012','-s','tb_capture_engine',f'-Ptb_capture_engine.WRAPPED={wrapped}',f'-Ptb_capture_engine.ENABLE_STREAM={enabled}','-o',str(binary),*files],check=True)
  subprocess.run(['vvp',str(binary)],check=True,timeout=900)
