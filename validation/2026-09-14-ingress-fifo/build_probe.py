#!/usr/bin/env python3
"""Isolated timing/resource experiment. Never assembles or deploys a bitstream."""
from pathlib import Path
import fcntl
import subprocess
import sys
import re
import json

root = Path(__file__).resolve().parents[2]
mhz=int(next((a.split('=',1)[1] for a in sys.argv if a.startswith('--mhz=')), '125'))
assert mhz in (125,250)
out = root / f'fpga/acq_sram/out/ingress-probe-{mhz}'
out.mkdir(parents=True, exist_ok=True)
quartus = Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
(out/'probe.v').write_text('''module ingress_probe(input clk,reset,push,pop,input [31:0] data,
 output reg [31:0] q,output reg valid,output [12:0] count,output empty,full,overflow,underflow);
 reg reset_r=1,push_r=0,pop_r=0;reg [31:0] data_r;
 wire [31:0] fifo_q;wire fifo_valid;
 always @(posedge clk)begin
  reset_r<=reset;push_r<=push;pop_r<=pop;data_r<=data;
  q<=fifo_q;valid<=fifo_valid;
 end
 sram_ingress_stream fifo(clk,reset_r,push_r,data_r,pop_r,fifo_valid,fifo_q,count,overflow,underflow);
 assign empty=count==0;assign full=count>=4608;
endmodule
''')
(out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
(out/'probe.qsf').write_text(f'''set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY ingress_probe
set_global_assignment -name NUM_PARALLEL_PROCESSORS 2
set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"
set_global_assignment -name VERILOG_FILE probe.v
set_global_assignment -name VERILOG_FILE {root / 'fpga/acq_sram/ingress_fifo.v'}
set_global_assignment -name VERILOG_FILE {root / 'fpga/acq_sram/ingress_stream.v'}
set_global_assignment -name SDC_FILE probe.sdc
set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files
set_instance_assignment -name VIRTUAL_PIN ON -to *
''')
(out/'probe.sdc').write_text(f'create_clock -name core -period {1000/mhz} [get_ports clk]\nderive_clock_uncertainty\n')
with open('/tmp/open-sds-quartus.lock', 'w') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    for tool in ('quartus_map','quartus_fit','quartus_sta'):
        print(tool, flush=True)
        with (out/(tool+'.log')).open('w') as log:
            result = subprocess.run([str(quartus/tool), 'probe'], cwd=out, stdout=log, stderr=subprocess.STDOUT)
        if result.returncode:
            print((out/(tool+'.log')).read_text()[-6000:])
            raise SystemExit(result.returncode)
report=(out/'output_files/probe.sta.rpt').read_text()
match=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;[^;\n]*;[^;\n]*;\s*([-.0-9]+)\s*;',report)
if not match:raise RuntimeError('missing multicorner timing summary')
setup,hold,pulse=map(float,match.groups())
result=dict(clock_mhz=mhz,setup_slack_ns=setup,hold_slack_ns=hold,min_pulse_slack_ns=pulse)
(out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps(result),flush=True)
if min(setup,hold,pulse)<0:raise SystemExit('FAILED timing; do not use this clock configuration')
print(out, flush=True)
