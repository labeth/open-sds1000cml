#!/usr/bin/env python3
"""Isolated controller timing; abstract transport and host endpoints, no image."""
from pathlib import Path
import subprocess
import fcntl
import re
import json
import shutil
import hashlib
import argparse
parser=argparse.ArgumentParser()
parser.add_argument("--seed",type=int,default=1)
parser.add_argument("--retime",choices=("ON","OFF"),default="OFF")
parser.add_argument("--duplicate",choices=("ON","OFF"),default="OFF")
args=parser.parse_args()
if args.seed<1:parser.error("seed must be positive")
root=Path(__file__).resolve().parents[2]
out=root/'fpga/acq_sram/out/timeslice-controller-probe'
out.mkdir(parents=True,exist_ok=True)
# Lock before touching project files or databases, not just tool invocation.
lock=open('/tmp/open-sds-quartus.lock','w')
fcntl.flock(lock,fcntl.LOCK_EX)
for generated in ('db','incremental_db','output_files'):
    shutil.rmtree(out/generated,ignore_errors=True)
(out/'result.json').unlink(missing_ok=True)
inputs=[('reset',1),('start',1),('stop',1),('source_finished',1),('read_bias',19),('ingress_data',36),
 ('ingress_valid',1),('ingress_pending',16),('ingress_fault',1),('host_fault',1),('bank_release',2),
 ('transport_ready',1),('transport_write_ready',1),('transport_done',1),('position',19),
 ('transport_read_valid',1),('transport_read_data',32)]
outputs=[('start_ready',1),('source_enable',1),('ingress_ready',1),('active',1),('done',1),('capture_done',1),
 ('fault',1),('error_code',4),('committed',64),('read_ordinal',64),('unread',20),('bank_busy',2),('bank_begin',2),
 ('bank_done',2),('bank_first0',64),('bank_first1',64),('bank_words0',20),('bank_words1',20),
 ('host_valid',1),('host_bank',1),('host_data',32),('host_index',12),('command',1),('command_read',1),
 ('command_discard',1),('command_continue',1),('command_count',20),('write_valid',1),('write_stop',1),('write_data',32)]
ni=sum(w for _,w in inputs);no=sum(w for _,w in outputs)
connections=['.clk(clk)']
for ports,bus in ((inputs,'i'),(outputs,'o')):
    offset=0
    for name,width in ports:
        connections.append(f'.{name}({bus}[{offset}+:{width}])');offset+=width
(out/'probe.v').write_text(f'''module controller_probe(input clk,input [{ni-1}:0] pins,output reg [{no-1}:0] result);
reg [{ni-1}:0] i;wire [{no-1}:0] o;
always @(posedge clk)begin i<=pins;result<=o;end
sram_timeslice_controller dut({','.join(connections)});
endmodule
''')
(out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
(out/'probe.qsf').write_text(f'''set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY controller_probe
set_global_assignment -name NUM_PARALLEL_PROCESSORS 2
set_global_assignment -name SEED {args.seed}
set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"
set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files
set_instance_assignment -name VIRTUAL_PIN ON -to *
set_global_assignment -name ALLOW_REGISTER_RETIMING {args.retime}
set_global_assignment -name PHYSICAL_SYNTHESIS_REGISTER_DUPLICATION {args.duplicate}
set_global_assignment -name VERILOG_FILE probe.v
set_global_assignment -name VERILOG_FILE {root/'fpga/acq_sram/timeslice_controller.v'}
set_global_assignment -name VERILOG_FILE {root/'fpga/acq_sram/ordinal_counter.v'}
set_global_assignment -name SDC_FILE probe.sdc
''')
(out/'probe.sdc').write_text('create_clock -name core -period 4.0 [get_ports clk]\nderive_clock_uncertainty\n')
quartus=Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
for tool in ('quartus_map','quartus_fit','quartus_sta'):
    print(tool,flush=True)
    with (out/(tool+'.log')).open('w') as log:
        r=subprocess.run([str(quartus/tool),'probe'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
    if r.returncode:
        print((out/(tool+'.log')).read_text()[-6000:]);raise SystemExit(r.returncode)
report=(out/'output_files/probe.sta.rpt').read_text()
m=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',report)
if not m:raise RuntimeError('missing multicorner summary')
values={k:None if v.strip()=='N/A' else float(v) for k,v in zip(('setup','hold','recovery','removal','minimum_pulse'),m.groups())}
(out/'source-hashes.json').write_text(json.dumps({name:hashlib.sha256((root/'fpga/acq_sram'/name).read_bytes()).hexdigest() for name in ('timeslice_controller.v','ordinal_counter.v')},indent=2)+'\n')
(out/'result.json').write_text(json.dumps(values,indent=2)+'\n');print(values,flush=True)
if any(v is not None and v<0 for v in values.values()):raise SystemExit('FAILED timing')
