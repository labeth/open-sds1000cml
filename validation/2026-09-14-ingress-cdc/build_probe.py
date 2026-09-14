#!/usr/bin/env python3
from pathlib import Path
import fcntl
import subprocess
import re
import json
root=Path(__file__).resolve().parents[2]
here=Path(__file__).resolve().parent
out=root/'fpga/acq_sram/out/ingress-cdc-probe'
out.mkdir(parents=True,exist_ok=True)
(out/'result.json').unlink(missing_ok=True)
(out/'cdc.tsv').unlink(missing_ok=True)
quartus=Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
(out/'probe.v').write_text('''module ingress_probe(input core,ram_clk,reset,source_valid,ready,input [35:0] data,
 output reg [35:0] q,output reg valid,source_ready,output reg [12:0] pending,output reg fault);
 reg sv=0,rd=0;reg [35:0] d;
 wire [35:0] qi;wire vi,sr,fi;wire [12:0] pi;
 always @(posedge core)begin
  sv<=source_valid;rd<=ready;d<=data;q<=qi;valid<=vi;source_ready<=sr;pending<=pi;fault<=fi;
 end
 sram_ingress_path path(reset,core,ram_clk,sv,d,sr,rd,vi,qi,pi,fi);
endmodule
''')
(out/'probe.qpf').write_text('PROJECT_REVISION = "probe"\n')
settings=['set_global_assignment -name FAMILY "Cyclone IV E"',
 'set_global_assignment -name DEVICE EP4CE10F17C8',
 'set_global_assignment -name TOP_LEVEL_ENTITY ingress_probe',
 'set_global_assignment -name NUM_PARALLEL_PROCESSORS 2',
 'set_global_assignment -name OPTIMIZATION_MODE "AGGRESSIVE PERFORMANCE"',
 'set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files',
 'set_instance_assignment -name VIRTUAL_PIN ON -to *',
 'set_global_assignment -name VERILOG_FILE probe.v',
 'set_global_assignment -name SDC_FILE probe.sdc']
settings += [f'set_global_assignment -name VERILOG_FILE {root / "fpga/acq_sram" / name}' for name in
             ('word_bridge.v','ingress_fifo.v','ingress_stream.v','ingress_path.v')]
(out/'probe.qsf').write_text('\n'.join(settings)+'\n')
(out/'probe.sdc').write_text('create_clock -name core -period 4.0 [get_ports core]\n'
 'create_clock -name ram -period 8.0 [get_ports ram_clk]\nderive_clock_uncertainty\n'+
 (root/'fpga/acq_sram/ingress_cdc.sdc').read_text())
(out/'audit.tcl').write_bytes((here/'audit.tcl').read_bytes())
with open('/tmp/open-sds-quartus.lock','w') as lock:
    fcntl.flock(lock,fcntl.LOCK_EX)
    for tool,args in [('quartus_map',['probe']),('quartus_fit',['probe']),('quartus_sta',['probe']),('quartus_sta',['-t','audit.tcl'])]:
        name='audit' if '-t' in args else tool
        print(name,flush=True)
        with (out/(name+'.log')).open('w') as log:
            result=subprocess.run([str(quartus/tool)]+args,cwd=out,stdout=log,stderr=subprocess.STDOUT)
        if result.returncode:
            print((out/(name+'.log')).read_text()[-6000:]);raise SystemExit(result.returncode)
report=(out/'output_files/probe.sta.rpt').read_text()
match=re.search(r'; Worst-case Slack\s*;\s*([-.0-9]+)\s*;\s*([-.0-9]+)\s*;([^;\n]*);([^;\n]*);\s*([-.0-9]+)\s*;',report)
if not match:raise RuntimeError('missing multicorner timing summary')
keys=('setup','hold','recovery','removal','minimum_pulse')
values={key:None if value.strip()=='N/A' else float(value) for key,value in zip(keys,match.groups())}
(out/'result.json').write_text(json.dumps(values,indent=2)+'\n')
print(values,flush=True)
if any(value is not None and value<0 for value in values.values()):raise SystemExit('FAILED timing')
print(out,flush=True)
