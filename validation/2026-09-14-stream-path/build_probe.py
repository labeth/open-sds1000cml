#!/usr/bin/env python3
"""Combined streaming resource/STA diagnostic; board IO and CDC still need qualification."""
from pathlib import Path
import fcntl, hashlib, json, re, shutil, subprocess, sys
root=Path(__file__).resolve().parents[2]
out=root/'fpga/acq_sram/out/stream-path-probe'
with open('/tmp/open-sds-quartus.lock','w') as lock:
 fcntl.flock(lock,fcntl.LOCK_EX)
 out.mkdir(parents=True,exist_ok=True)
 for name in ('db','incremental_db','output_files'):
  shutil.rmtree(out/name,ignore_errors=True)
 (out/'result.json').unlink(missing_ok=True)
 names=('stream_path.v','stream_engine.v','ingress_path.v','ingress_fifo.v','ingress_stream.v','word_bridge.v','timeslice_controller.v','ordinal_counter.v','transport.v','host_path.v','host_packer.v','host_fifo.v','host_sink.v','host_ram.v','host_ownership.v')
 bounded="--cdc" in sys.argv
 seed=int(sys.argv[sys.argv.index("--seed")+1]) if "--seed" in sys.argv else 1
 if seed<1:raise ValueError("seed must be positive")
 hashes={}
 for name in names:
  data=(root/'fpga/acq_sram'/name).read_bytes()
  (out/name).write_bytes(data)
  hashes[name]=hashlib.sha256(data).hexdigest()
 (out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
 (out/'probe.qsf').write_text('''set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY sram_stream_path
set_global_assignment -name NUM_PARALLEL_PROCESSORS 2
set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"
set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files
set_instance_assignment -name VIRTUAL_PIN ON -to *
set_global_assignment -name SDC_FILE probe.sdc
'''+''.join(f'set_global_assignment -name VERILOG_FILE {name}\n' for name in names))
 with (out/'probe.qsf').open('a') as f:f.write(f'set_global_assignment -name SEED {seed}\n')
 # No broad asynchronous cuts: cross-domain failures are expected until the
 # specific bundled-data/token constraints and endpoint audit are supplied.
 (out/'probe.sdc').write_text('''create_clock -name core -period 4.0 [get_ports core_clk]
create_clock -name sample -period 4.0 -waveform {0.4 2.4} [get_ports sample_clk]
create_clock -name ram -period 8.0 [get_ports ram_clk]
create_clock -name host -period 10.0 [get_ports host_clk]
derive_clock_uncertainty
set_false_path -from [get_ports reset]
''')
 if bounded:
  data=(root/'fpga/acq_sram/stream_path_cdc.sdc').read_bytes()
  (out/'stream_path_cdc.sdc').write_bytes(data)
  hashes['stream_path_cdc.sdc']=hashlib.sha256(data).hexdigest()
  with (out/'probe.sdc').open('a') as f:f.write('source stream_path_cdc.sdc\n')
 q=Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
 for tool in ('quartus_map','quartus_fit','quartus_sta'):
  print(tool,flush=True)
  with (out/(tool+'.log')).open('w') as log:
   result=subprocess.run([str(q/tool),'probe'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
  if result.returncode:
   print((out/(tool+'.log')).read_text()[-6000:]);raise SystemExit(result.returncode)
 sta=(out/'output_files/probe.sta.rpt').read_text(encoding='latin-1')
 match=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta)
 if not match:raise RuntimeError('missing timing summary')
 timing={k:None if v.strip()=='N/A' else float(v) for k,v in zip(('setup','hold','recovery','removal','minimum_pulse'),match.groups())}
 fit=(out/'output_files/probe.fit.rpt').read_text(encoding='latin-1')
 blocks=re.search(r'; M9K(?:s| blocks)\s*;\s*(\d+)',fit)
 result={'qualification':'diagnostic only; IO unconstrained; CDC audit pending',
         'seed':seed,'timing':timing,'m9k_blocks':int(blocks.group(1)) if blocks else None,'sources':hashes}
 (out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps(result,indent=2),flush=True)
 if bounded:
  audit=root/'validation/2026-09-14-stream-path/audit_cdc.tcl'
  (out/'audit_cdc.tcl').write_bytes(audit.read_bytes())
  with (out/'audit.log').open('w') as log:
   checked=subprocess.run([str(q/'quartus_sta'),'-t','audit_cdc.tcl'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
  result['cdc_audit']='passed' if checked.returncode==0 else 'failed'
  result['qualification']='IO unconstrained; combined CDC audit '+result['cdc_audit']+'; inspect timing separately'
  (out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
  print((out/'audit.log').read_text()[-1500:],flush=True)
  if checked.returncode:raise SystemExit(checked.returncode)
