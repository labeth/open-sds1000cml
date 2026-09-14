#!/usr/bin/env python3
"""Default 80-bit/eight-slot FIFO probe; no FPGA image or board deployment."""
from pathlib import Path
import subprocess,fcntl,shutil,json,re,hashlib
root=Path(__file__).resolve().parents[2]
out=root/'fpga/acq_sram/out/host-fifo-probe'
lock=open('/tmp/open-sds-quartus.lock','w');fcntl.flock(lock,fcntl.LOCK_EX)
out.mkdir(parents=True,exist_ok=True)
for name in ('db','incremental_db','output_files'):shutil.rmtree(out/name,ignore_errors=True)
(out/'result.json').unlink(missing_ok=True)
(out/'probe.v').write_text('''module host_fifo_probe(input reset,source_clk,dest_clk,input [80:0] pins,input ready_pin,
 output reg [1:0] source_status,output reg [80:0] result);
reg [80:0] i;reg ready;wire available,overflow,valid;wire [79:0] data;
always @(posedge source_clk)begin i<=pins;source_status<={overflow,available};end
always @(posedge dest_clk)begin ready<=ready_pin;result<={valid,data};end
sram_host_fifo dut(.reset(reset),.source_clk(source_clk),.dest_clk(dest_clk),
 .push(i[80]),.source_data(i[79:0]),.source_ready(available),.overflow(overflow),
 .dest_valid(valid),.dest_data(data),.dest_ready(ready));
endmodule
''')
(out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
(out/'probe.qsf').write_text(f'''set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY host_fifo_probe
set_global_assignment -name NUM_PARALLEL_PROCESSORS 2
set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"
set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files
set_instance_assignment -name VIRTUAL_PIN ON -to *
set_global_assignment -name VERILOG_FILE probe.v
set_global_assignment -name VERILOG_FILE {root/'fpga/acq_sram/host_fifo.v'}
set_global_assignment -name SDC_FILE probe.sdc
''')
(out/'probe.sdc').write_text('create_clock -name source -period 4.0 [get_ports source_clk]\ncreate_clock -name dest -period 8.0 [get_ports dest_clk]\nderive_clock_uncertainty\n'+(root/'fpga/acq_sram/host_fifo_cdc.sdc').read_text())
quartus=Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
for tool in ('quartus_map','quartus_fit','quartus_sta'):
 print(tool,flush=True)
 with (out/(tool+'.log')).open('w') as log:
  result=subprocess.run([str(quartus/tool),'probe'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
 if result.returncode:
  print((out/(tool+'.log')).read_text()[-6000:]);raise SystemExit(result.returncode)
sta=(out/'output_files/probe.sta.rpt').read_text(encoding='latin-1')
m=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta)
if not m:raise RuntimeError('missing timing summary')
timing={k:None if v.strip()=='N/A' else float(v) for k,v in zip(('setup','hold','recovery','removal','minimum_pulse'),m.groups())}
fit=(out/'output_files/probe.fit.rpt').read_text(encoding='latin-1')
m=re.search(r'; M9K(?:s| blocks)\s*;\s*(\d+)',fit)
if not m:raise RuntimeError('missing M9K count')
result={'cdc_audit':'not_run','timing':timing,'m9k_blocks':int(m.group(1)),
 'sources':{name:hashlib.sha256((root/'fpga/acq_sram'/name).read_bytes()).hexdigest() for name in ('host_fifo.v','host_fifo_cdc.sdc')}}
(out/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(result,flush=True)
if result['m9k_blocks']!=0:raise SystemExit('FAILED: 250 MHz FIFO storage mapped to RAM')
if any(v is not None and v<0 for v in timing.values()):raise SystemExit('FAILED timing')

with (out/'audit.log').open('w') as log:
 audit=subprocess.run([str(quartus/'quartus_sta'),'-t',str(root/'validation/2026-09-14-host-fifo/audit.tcl')],cwd=out,stdout=log,stderr=subprocess.STDOUT)
if audit.returncode:
 print((out/'audit.log').read_text()[-6000:]);raise SystemExit(audit.returncode)
result['cdc_audit']='passed'
(out/'result.json').write_text(json.dumps(result,indent=2)+'\n')
print('PASS FIFO timing, resource and CDC audit',flush=True)
