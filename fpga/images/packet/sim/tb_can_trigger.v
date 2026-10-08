// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// One line sample per receiver clock; prints every published event.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_can_trigger;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,tick=1,line=1,inverted=0;
 reg [23:0] bit_ticks_q8=24'd32000,data_ticks_q8=24'd32000;
 reg [63:0] pattern=0,sample=0;reg [3:0] pattern_len=0;
 wire match,event_valid;wire [7:0] event_kind;wire [31:0] event_data;
 wire [15:0] event_count;wire [63:0] event_sample;
 can_trigger dut(.*);
 reg [0:0] samples[0:4194303];reg [4095:0] input_file;
 integer length,i,ignored,inv=0,stall=0;
 always @(posedge clk)begin
  #1;
  if(match && (!event_valid || event_kind!=3))$fatal(1,"unqualified hit");
  if(event_valid)$display("E %0d %08x %04x %0d %0d",event_kind,event_data,event_count,event_sample,match);
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length))$fatal(1,"missing fixture");
  ignored=$value$plusargs("nominal=%d",bit_ticks_q8);
  ignored=$value$plusargs("data=%d",data_ticks_q8);
  ignored=$value$plusargs("pattern=%h",pattern);
  ignored=$value$plusargs("len=%d",pattern_len);
  ignored=$value$plusargs("inverted=%d",inv);
  ignored=$value$plusargs("stall=%d",stall);
  inverted=inv!=0;
  $readmemh(input_file,samples,0,length-1);
  repeat(4)@(negedge clk);reset=0;
  for(i=0;i<length;i=i+1)begin
   line=samples[i];sample=i;
   @(negedge clk);
   if(stall!=0 && i%13==0)begin tick=0;repeat(2)@(negedge clk);tick=1;end
  end
  tick=0;repeat(4)@(negedge clk);$finish(0);
 end
 initial begin #400000000;$fatal(1,"timeout");end
endmodule
