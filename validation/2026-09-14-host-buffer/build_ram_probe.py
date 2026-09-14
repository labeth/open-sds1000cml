#!/usr/bin/env python3
"""Isolated host RAM resource/timing check. No FPGA image or board deployment."""
from pathlib import Path
import subprocess,fcntl,shutil,json,re,hashlib
root=Path(__file__).resolve().parents[2]
out=root/'fpga/acq_sram/out/host-ram-probe'
lock=open('/tmp/open-sds-quartus.lock','w');fcntl.flock(lock,fcntl.LOCK_EX)
out.mkdir(parents=True,exist_ok=True)
for name in ('db','incremental_db','output_files'):shutil.rmtree(out/name,ignore_errors=True)
(out/'result.json').unlink(missing_ok=True)
(out/'probe.v').write_text('''module host_ram_probe(input write_clk,read_clk,input [77:0] wpins,input [15:0] rpins,
 output reg wf,output reg [17:0] result);
reg [77:0] w;reg [15:0] r;wire fault,valid,error;wire [15:0] data;
always @(posedge write_clk)begin w<=wpins;wf<=fault;end
always @(posedge read_clk)begin r<=rpins;result<={error,valid,data};end
sram_host_ram dut(.write_clk(write_clk),.write_reset(w[0]),.write_enable(w[1]),
 .write_pair(w[13:2]),.write_data(w[77:14]),.write_fault(fault),.read_clk(read_clk),
 .read_reset(r[0]),.read_enable(r[1]),.read_halfword(r[15:2]),.read_valid(valid),
 .read_error(error),.read_data(data));
endmodule
''')
(out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
(out/'probe.qsf').write_text(f'''set_global_assignment -name FAMILY "Cyclone IV E"
set_global_assignment -name DEVICE EP4CE10F17C8
set_global_assignment -name TOP_LEVEL_ENTITY host_ram_probe
set_global_assignment -name NUM_PARALLEL_PROCESSORS 2
set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"
set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files
set_instance_assignment -name VIRTUAL_PIN ON -to *
set_global_assignment -name VERILOG_FILE probe.v
set_global_assignment -name VERILOG_FILE {root/'fpga/acq_sram/host_ram.v'}
set_global_assignment -name SDC_FILE probe.sdc
''')
(out/'probe.sdc').write_text('create_clock -name ram_write -period 8.0 [get_ports write_clk]\ncreate_clock -name host_read -period 10.0 [get_ports read_clk]\nderive_clock_uncertainty\n')
quartus=Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
for tool in ('quartus_map','quartus_fit','quartus_sta'):
 print(tool,flush=True)
 with (out/(tool+'.log')).open('w') as log:
  result=subprocess.run([str(quartus/tool),'probe'],cwd=out,stdout=log,stderr=subprocess.STDOUT)
 if result.returncode:
  print((out/(tool+'.log')).read_text()[-6000:]);raise SystemExit(result.returncode)
sta=(out/'output_files/probe.sta.rpt').read_text(encoding='latin-1')
match=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',sta)
if not match:raise RuntimeError('missing timing summary')
timing={k:None if v.strip()=='N/A' else float(v) for k,v in zip(('setup','hold','recovery','removal','minimum_pulse'),match.groups())}
fit=(out/'output_files/probe.fit.rpt').read_text(encoding='latin-1')
match=re.search(r'; M9Ks\s*;\s*(\d+)',fit)
if not match:raise RuntimeError('missing M9K count')
result={'timing':timing,'m9k_blocks':int(match.group(1)),
 'source_sha256':hashlib.sha256((root/'fpga/acq_sram/host_ram.v').read_bytes()).hexdigest()}
(out/'result.json').write_text(json.dumps(result,indent=2)+'\n');print(result,flush=True)
if result['m9k_blocks']!=20:raise SystemExit('FAILED resource budget')
if any(v is not None and v<0 for v in timing.values()):raise SystemExit('FAILED timing')
