#!/usr/bin/env python3
"""Check mode selection against accepted-command history; integration tests cover readiness."""
from pathlib import Path
import hashlib, subprocess, tempfile
root=Path(__file__).resolve().parents[2]
source=root/'fpga/acq_sram/capture_engine.v'
bench='''`timescale 1ns/1ps
module tb;
reg clk=0;always #2 clk=~clk;
reg reset=1,start=0,finite_mode=0,ready=0,expected=0;
wire selected_finite;
sram_capture_engine #(.AW(4)) dut(.core_clk(clk),.ram_clk(clk),.host_clk(clk),
.reset(reset),.start(start),.finite_mode(finite_mode),.selected_finite(selected_finite));
integer i;reg [31:0] rng=32'h947123ad;
initial begin
force dut.start_ready=ready;
for(i=0;i<10000;i=i+1)begin
 @(negedge clk);
 rng={rng[30:0],rng[31]^rng[21]^rng[1]^rng[0]};
 reset=(i%137==0);start=rng[0];ready=rng[1];finite_mode=rng[2];
 @(posedge clk);
 if(reset)expected=0;else if(start && ready)expected=finite_mode;
 #0.1;if(selected_finite!==expected)$fatal(1,"mode changed on wrong command cycle %0d",i);
end
$display("PASS backend mode: 10000 accepted/rejected/reset cycles preserve immediate selection");$finish;
end
endmodule
'''
print('capture_engine.v SHA256',hashlib.sha256(source.read_bytes()).hexdigest(),flush=True)
with tempfile.TemporaryDirectory(prefix='acq-mode-') as d:
 p=Path(d);(p/'tb.v').write_text(bench)
 # Only mode state is under test. Ready is driven explicitly; datapath children
 # are omitted. The full acquisition bench separately uses real child modules.
 subprocess.run(['iverilog','-g2012','-i','-s','tb','-o',str(p/'test'),str(p/'tb.v'),str(source)],check=True)
 subprocess.run(['vvp',str(p/'test')],check=True)
