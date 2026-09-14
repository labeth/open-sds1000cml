#!/usr/bin/env python3
"""Exercise producer selection over one actual host buffer and SRAM transport."""
from pathlib import Path
import hashlib,json,subprocess,tempfile,sys
root=Path(__file__).resolve().parents[2]
names=('acquisition_path.v','acquisition_source.v','finite_writer.v','record.v','board_capture_path.v','capture_path.v','capture_engine.v','stream_engine.v','finite_recall.v','ingress_path.v','ingress_fifo.v',
       'ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v','transport.v',
       'host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v',
       'sim/ddr_model.v','sim/tb_acquisition_path.v')
with tempfile.TemporaryDirectory(prefix='acq-shared-engine-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_acquisition_path',f'-Ptb_acquisition_path.READY_ONLY={int("--ready-only" in sys.argv)}','-o',str(binary),*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
