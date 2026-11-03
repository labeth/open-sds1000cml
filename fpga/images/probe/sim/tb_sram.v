// ENGMODEL-OWNER-UNIT: FU-RTL-PROBE
// tb_sram.v -- the probe's SRAM test on a model of the board's SRAM (an
// external address counter stepped by K2, cleared while G1 is low; a write
// lands three words on, a read arrives two pulses late): a fitted SRAM
// passes; one missing (the bus pulled high, or holding its last level) and
// one with an address line stuck fail.
`timescale 1ns/1ps

// The board's SRAM and its address counter, AW address bits.
// TRLC-LINKS: REQ-SDS-288
module sram_model #(parameter AW=12)(input g1,k1,k2,inout [31:0] dq,input [1:0] fault);
 reg [31:0] mem [0:(1<<AW)-1];
 reg [AW-1:0] addr=0;reg [31:0] s1=0,s2=0;reg [31:0] held=32'hffffffff;
 wire [AW-1:0] a=fault==2 ? addr&~(1<<5) : addr; // fault 2: address line 5 stuck low
 always @(posedge k2 or negedge g1)
  if(!g1)addr<=0;
  else begin
   if(!k1)mem[(a+3)&((1<<AW)-1)]<=dq;
   s1<=mem[a];s2<=s1;addr<=addr+1'b1;
  end
 // fault 1: no SRAM; the bus keeps the level last driven on it
 always @* if(fault==1 && dq!==32'bz)held=dq;
 assign dq=(fault==1) ? 32'bz : (k1 ? s2 : 32'bz);
 assign (weak1,weak0) dq=fault==1 ? held : 32'hffffffff;
endmodule

// TRLC-LINKS: REQ-SDS-288
module tb;
 reg clk=0;always #10 clk=~clk;
 reg start=0;reg [1:0] fault=0;
 wire on,dq_oe,g1,k1,k2,done;wire [31:0] dqo,first,bad;wire [23:0] rel,seq,checked;
 wire [31:0] bus;
 assign bus=dq_oe ? dqo : 32'bz;
 sram_model #(.AW(12)) mem(.g1(g1),.k1(k1),.k2(k2),.dq(bus),.fault(fault));
 sram_test #(.AW(12)) dut(.clk(clk),.start(start),.dq_in(bus),.on(on),.dq(dqo),.dq_oe(dq_oe),.g1(g1),.k1(k1),.k2(k2),
  .done(done),.rel_err(rel),.seq_err(seq),.checked(checked),.bad_bits(bad),.first(first));
 task run(input [1:0] f);
  begin
   fault=f;@(posedge clk);start=1;@(posedge clk);start=0;
   wait(done);@(posedge clk);
   $display("fault %0d: checked %0d pattern errors %0d count errors %0d bad bits %h",f,checked,rel,seq,bad);
  end
 endtask
 initial begin
  #100;
  run(0);
  if(checked<4000 || rel!=0 || seq!=0)$fatal(1,"a fitted SRAM fails");
  run(1);
  if(rel<4000)$fatal(1,"a missing SRAM passes");
  run(2);
  if(seq<100)$fatal(1,"a stuck address line passes");
  $display("PASS sram test: fitted, missing and stuck address line");$finish;
 end
endmodule
