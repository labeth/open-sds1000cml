#!/usr/bin/env python3
"""Compare transport control/data against the frozen pre-optimization RTL."""
from pathlib import Path
import hashlib,json,subprocess,tempfile
root=Path(__file__).resolve().parents[2]
rtl=root/'fpga/acq_sram'
baseline=root/'validation/2026-09-14-stream-path/bounded-cdc-probe/transport.v'
with tempfile.TemporaryDirectory(prefix='acq-ready-equiv-') as directory:
 p=Path(directory)
 src=(rtl/'transport.v').read_bytes();old=baseline.read_bytes()
 assert hashlib.sha256(old).hexdigest()=='f24d6fd8d9878b0e4eb722b293cab5a06c380ef42c58e6becccc0156eec1bfae'
 print(json.dumps({'candidate':hashlib.sha256(src).hexdigest(),'reference':hashlib.sha256(old).hexdigest()}),flush=True)
 (p/'candidate.v').write_bytes(src)
 (p/'reference.v').write_bytes(old.replace(b'module sram_transport #',b'module sram_transport_reference #',1))
 (p/'ddr.v').write_bytes((rtl/'sim/ddr_model.v').read_bytes())
 (p/'tb.v').write_text('''`timescale 1ns/1ps
module tb;
 parameter MODE=1, AW=8;
 reg clk=0;always #2 clk=~clk;
 wire sample_clk;assign #0.4 sample_clk=clk;
 reg reset=1,locked=1,command=0,command_read=0,command_discard=0,command_continue=0;
 reg [AW:0] command_count=0;reg [31:0] write_data=0;
 reg write_valid=0,write_stop=0;
 wire r0,r1,w0,w1,v0,v1,d0,d1,k10,k11,k20,k21,g0,g1;
 wire [AW-1:0] p0,p1;wire [31:0] rd0,rd1,q0,q1;
 assign q0=k10 ? write_data : 32'bz;
 assign q1=k11 ? write_data : 32'bz;
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(MODE)) dut
 (clk,sample_clk,reset,locked,command,command_read,command_discard,command_continue,command_count,r0,
 write_data,write_valid,write_stop,w0,rd0,v0,d0,p0,q0,k10,k20,g0);
 sram_transport_reference #(.AW(AW),.CONTINUOUS_ONLY(MODE)) refdut
 (clk,sample_clk,reset,locked,command,command_read,command_discard,command_continue,command_count,r1,
 write_data,write_valid,write_stop,w1,rd1,v1,d1,p1,q1,k11,k21,g1);
 integer i,seed=928731,commands=0,offers=0;reg [31:0] random_bits;
 initial begin
  for(i=0;i<100000;i=i+1)begin
   @(negedge clk);#0.1;
   if({r0,w0,v0,d0,p0,k10,k20,g0}!=={r1,w1,v1,d1,p1,k11,k21,g1})
    $fatal(1,"control mismatch cycle=%0d",i);
   if(v0 && rd0!==rd1)$fatal(1,"read data mismatch");
   if(q0!==q1)$fatal(1,"pin data mismatch");
   if(r0 && command)commands=commands+1;
   if(w0 && write_valid)offers=offers+1;
   random_bits=$random(seed);
   reset=i<3 || (i%997==0);locked=(i%991!=0);
   command=random_bits[0];command_read=random_bits[1];
   command_discard=random_bits[2];command_continue=random_bits[3];
   case(random_bits[18:16])
    0:command_count=0;
    1:command_count=1;
    2:command_count=1<<AW;
    3:command_count=(1<<AW)+1;
    4:command_count=(1<<AW)-1;
    default:command_count=random_bits[AW+4:4];
   endcase
   write_valid=random_bits[13];
   write_stop=random_bits[14] && random_bits[15];write_data=$random(seed);
  end
  if(commands<100 || offers<100)$fatal(1,"insufficient traffic");
  $display("PASS transport equivalence: mode=%0d AW=%0d, 100000 cycles, commands=%0d offers=%0d, stop/reset/lock transitions",MODE,AW,commands,offers);
  $finish;
 end
endmodule
''')
 for aw,mode in ((8,0),(8,1),(19,0),(19,1)):
  subprocess.run(['iverilog','-g2012','-s','tb',f'-Ptb.MODE={mode}',f'-Ptb.AW={aw}','-o',str(p/'test'),*[str(p/n) for n in ['candidate.v','reference.v','ddr.v','tb.v']]],check=True)
  subprocess.run(['vvp',str(p/'test')],check=True)
