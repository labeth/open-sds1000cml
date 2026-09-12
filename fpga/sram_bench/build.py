#!/usr/bin/env python3
import pathlib,sys,subprocess,fcntl,re,json
root=pathlib.Path(__file__).resolve().parent
mhz=int(sys.argv[1]);phase=int(sys.argv[2]) if len(sys.argv)>2 else 0
assert 1<=mhz<=300
out=root/'out'/f'{mhz}mhz-p{phase}';out.mkdir(parents=True,exist_ok=True)
lock=open('/tmp/open-sds-quartus.lock','w');fcntl.flock(lock,fcntl.LOCK_EX)
(out/'bench.v').write_text(f'`define BENCH_MHZ {mhz}\n'+(root/'bench.v').read_text())
(out/'gpmc_slave.v').write_text((root.parent/'common/gpmc_slave.v').read_text())
(out/'pll.v').write_text(f'''module bench_pll(input refclk,output c0,c1,locked);
wire [4:0] clocks;
altpll #(.inclk0_input_frequency(10000),.intended_device_family("Cyclone IV E"),
.clk0_multiply_by({mhz}),.clk0_divide_by(100),.clk0_duty_cycle(50),.clk0_phase_shift("0"),
.clk1_multiply_by({mhz}),.clk1_divide_by(100),.clk1_duty_cycle(50),.clk1_phase_shift("{phase}"),
.compensate_clock("CLK0"),.operation_mode("NORMAL"),.width_clock(5),
.port_inclk0("PORT_USED"),.port_clk0("PORT_USED"),.port_clk1("PORT_USED"),.port_locked("PORT_USED")) pll(
.inclk({{1'b0,refclk}}),.clk(clocks),.locked(locked),.areset(1'b0),.pfdena(1'b1),.pllena(1'b1),
.clkena(6'b111111),.extclkena(4'b1111),.fbin(1'b1),.clkswitch(1'b0),.scanclk(1'b0),
.scanclkena(1'b0),.scandata(1'b0),.configupdate(1'b0),.phasecounterselect(4'b0),
.phasestep(1'b0),.phaseupdown(1'b0),.scanaclr(1'b0),.scanread(1'b0),.scanwrite(1'b0));
assign c0=clocks[0];assign c1=clocks[1];endmodule
''')
ports={}
base=(root.parent/'default/default.qsf').read_text()
for ball,port in re.findall(r'set_location_assignment PIN_(\w+) -to (\S+)',base):
 if port.startswith(('gpmc_d[','sel[','enc_p[','enc_n[')) or port in ('clk','mclk_in','nCS1','nOE','nWE','gpmc_a2','gpmc_b1','k1','k2','g1','g2','d1','d2','f1','f2','j2','a11'):ports[port]=ball
for i,ball in enumerate('J6 F5 L2 L1 L3 N2 N1 K5 L4 R1 P2 P1 F3 G5 N3 P3 N5 N6 D3 M6 R5 T5 R6 T6 R3 R7 T7 T3 T2 R4 T4 F7'.split()):ports[f'dq[{i}]']=ball
assert len(set(ports.values()))==len(ports)
q=['set_global_assignment -name FAMILY "Cyclone IV E"','set_global_assignment -name DEVICE EP4CE10F17C8','set_global_assignment -name TOP_LEVEL_ENTITY sram_bench_top','set_global_assignment -name PROJECT_OUTPUT_DIRECTORY output_files','set_global_assignment -name NUM_PARALLEL_PROCESSORS 2','set_global_assignment -name RESERVE_ALL_UNUSED_PINS "AS INPUT TRI-STATED"','set_global_assignment -name RESERVE_FLASH_NCE_AFTER_CONFIGURATION "USE AS REGULAR IO"','set_global_assignment -name ON_CHIP_BITSTREAM_DECOMPRESSION OFF','set_global_assignment -name SDC_FILE bench.sdc']
q += [f'set_global_assignment -name VERILOG_FILE {v}' for v in ('bench.v','gpmc_slave.v','pll.v')]
for port,ball in ports.items():
 q += [f'set_location_assignment PIN_{ball} -to {port}',f'set_instance_assignment -name IO_STANDARD "3.3-V LVTTL" -to {port}']
 if port.startswith(('dq[','enc_')) or port in ('k1','g1','g2','d1','f1','f2','j2','a11'):q += [f'set_instance_assignment -name CURRENT_STRENGTH_NEW "MINIMUM CURRENT" -to {port}']
q += [f'set_instance_assignment -name CURRENT_STRENGTH_NEW 8MA -to dq[{i}]' for i in range(32)]
q += ['set_instance_assignment -name FAST_INPUT_REGISTER ON -to dq[*]', 'set_instance_assignment -name FAST_OUTPUT_REGISTER ON -to dq[*]']
(out/'bench.qsf').write_text('\n'.join(q)+'\n');(out/'bench.qpf').write_text('PROJECT_REVISION = "bench"\n')
(out/'bench.sdc').write_text('''create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name cpu -period 12.5 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
set_clock_groups -asynchronous -group [get_clocks cpu] -group [get_clocks {*pll* mref}]
# External SRAM timing is measured separately; do not interpret core slack as board closure.
''')
quartus=pathlib.Path('/home/labeth/intelFPGA_lite/21.1/quartus/bin')
for tool,args in [('quartus_map',['bench']),('quartus_fit',['bench']),('quartus_sta',['bench']),('quartus_asm',['bench']),('quartus_cpf',['-c','-o','bitstream_compression=off','output_files/bench.sof','bench.rbf'])]:
 print(tool,flush=True)
 with (out/(tool+'.log')).open('w') as f:r=subprocess.run([str(quartus/tool)]+args,cwd=out,stdout=f,stderr=subprocess.STDOUT)
 if r.returncode:print((out/(tool+'.log')).read_text()[-7000:]);sys.exit(r.returncode)
assert (out/'bench.rbf').stat().st_size==368011
subprocess.run([sys.executable,str(root/'audit.py'),str(out)],check=True)
print(out/'bench.rbf',flush=True)
