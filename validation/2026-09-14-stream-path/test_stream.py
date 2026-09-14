#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('stream_path.v','stream_engine.v','ingress_path.v','ingress_fifo.v','ingress_stream.v','word_bridge.v',
       'timeslice_controller.v','ordinal_counter.v','transport.v','sim/ddr_model.v',
       'host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v','sim/tb_sram_stream_path.v')
with tempfile.TemporaryDirectory(prefix='acq-stream-wrapper-') as directory:
 tmp=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=tmp/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=tmp/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_stream_path','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
