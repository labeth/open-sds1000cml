#!/usr/bin/env python3
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
names=('profile_core.v','command_port.v','command_bridge.v','host_read_port.v','host_read_window.v','acquisition_path.v','acquisition_source.v','finite_writer.v','record.v','board_capture_path.v','capture_engine.v','finite_recall.v','stream_engine.v','ingress_path.v','ingress_fifo.v','ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v','transport.v','host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v','sim/ddr_model.v','sim/tb_profile_core.v','../common/gpmc_slave.v')
with tempfile.TemporaryDirectory(prefix='acq-profile-core-') as directory:
 p=Path(directory);sources=[];hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes();dest=p/Path(name).name;dest.write_bytes(data)
  sources.append(str(dest));hashes[name]=hashlib.sha256(data).hexdigest()
 mock=(root/'fpga/acq_sram/sim/tb_acquisition_path.v').read_text().split('module tb_acquisition_path')[0]
 (p/'adc_mocks.v').write_text(mock);sources.append(str(p/'adc_mocks.v'));hashes['adc_mocks']=hashlib.sha256(mock.encode()).hexdigest()
 print(json.dumps(hashes,sort_keys=True),flush=True)
 binary=p/'test'
 subprocess.run(['iverilog','-g2012','-s','tb_profile_core','-o',str(binary),*sources],check=True)
 subprocess.run(['vvp',str(binary)],check=True,timeout=900)
