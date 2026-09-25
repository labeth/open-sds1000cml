// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_arinc429_trigger;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,tick=1;
 reg [7:0] code=128,threshold_hi=161,threshold_lo=92,exit_hi=141,exit_lo=114;
 reg [23:0] bit_ticks=20;
 reg [63:0] sample=0,base=64'hfffffff0;
 reg [31:0] pattern=0,pattern_mask=0;
 wire match,event_valid;wire [7:0] event_kind;
 wire [31:0] event_data;wire [63:0] event_sample;
 arinc429_trigger dut(.*);
 reg [7:0] samples[0:2097151];reg [4095:0] input_file;
 integer length,period,i,ignored,stall=0,reset_at=-1,disable_at=-1,invalid=0;
 always @(posedge clk)begin
  #1;
  if(match && (!event_valid || event_kind!=3 || event_data!=0))$fatal(1,"unqualified hit");
  if(event_valid)$display("E %0d %08x %016x %0d",event_kind,event_data,event_sample,match);
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length) ||
     !$value$plusargs("period=%d",period))$fatal(1,"missing fixture");
  ignored=$value$plusargs("pattern=%h",pattern);
  ignored=$value$plusargs("mask=%h",pattern_mask);
  ignored=$value$plusargs("base=%h",base);
  ignored=$value$plusargs("stall=%d",stall);
  ignored=$value$plusargs("reset_at=%d",reset_at);
  ignored=$value$plusargs("disable_at=%d",disable_at);
  ignored=$value$plusargs("invalid=%d",invalid);
  if(invalid!=0)exit_hi=threshold_hi;
  $readmemh(input_file,samples,0,length-1);
  bit_ticks=period;repeat(4)@(negedge clk);reset=0;
  for(i=0;i<length;i=i+1)begin
   code=samples[i];sample=base+64'(i)*4;
   reset=i==reset_at;enable=i!=disable_at;
   @(negedge clk);
   if(stall!=0 && i%17==0)begin tick=0;repeat(3)@(negedge clk);tick=1;end
  end
  tick=0;repeat(4)@(negedge clk);$finish(0);
 end
 initial begin #40000000;$fatal(1,"timeout");end
endmodule
