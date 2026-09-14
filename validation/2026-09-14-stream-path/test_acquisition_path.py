#!/usr/bin/env python3
"""Exercise producer selection over one actual host buffer and SRAM transport."""
from pathlib import Path
import argparse,hashlib,json,subprocess,tempfile
parser=argparse.ArgumentParser()
parser.add_argument("--deep-only",action="store_true")
parser.add_argument("--ready-only",action="store_true")
parser.add_argument("--host-fault-only",action="store_true")
parser.add_argument("--vendor-library",type=Path,help="Intel altera_mf.v for the host RAM primitive")
args=parser.parse_args()
root=Path(__file__).resolve().parents[2]
names=('acquisition_path.v','acquisition_source.v','finite_writer.v','record.v','board_capture_path.v','capture_path.v','capture_engine.v','stream_engine.v','finite_recall.v','ingress_path.v','ingress_fifo.v',
       'ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v','transport.v',
       'host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v',
       'sim/ddr_model.v','sim/tb_acquisition_path.v')
with tempfile.TemporaryDirectory(prefix='acq-shared-engine-') as directory:
 p=Path(directory);files=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name
  # Keep the existing ideal transport clock model: only host RAM uses Intel's model.
  compiled=data.replace(b'altddio_out',b'test_transport_ddio') if args.vendor_library and name in ('transport.v','sim/ddr_model.v') else data
  dest.write_bytes(compiled)
  files.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 if args.vendor_library:
  hashes["vendor:altera_mf.v"]=hashlib.sha256(args.vendor_library.read_bytes()).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 extra=["-DALTERA_RESERVED_QIS",str(args.vendor_library.resolve())] if args.vendor_library else []
 subprocess.run(['iverilog','-g2012','-s','tb_acquisition_path',f'-Ptb_acquisition_path.ENABLE_STREAM={int(not args.deep_only)}',f'-Ptb_acquisition_path.READY_ONLY={int(args.ready_only)}',f'-Ptb_acquisition_path.HOST_FAULT_ONLY={int(args.host_fault_only)}','-o',str(binary),*extra,*files],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
