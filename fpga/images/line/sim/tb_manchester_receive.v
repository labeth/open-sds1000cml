// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// The Go test supplies physical samples and compares both hypotheses against
// recoverManchester, including candidate words, errors and final scores.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_manchester_receive;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,finish=0,tick=1,line=1,previous_level=1,ieee=1,msb=1;
 reg [23:0] bit_ticks=20;reg [4:0] word_bits=8;
 wire [1:0] event_valid,event_error,done,overflow;
 wire [31:0] event_data,event_cell;wire signed [19:0] score_a,score_b;
 wire [16:0] good_a,good_b;
 manchester_receive dut(.*);
 reg samples[0:131071];reg [4095:0] input_file;
 integer length,first_edge,period,width,convention,order,i,p,stall=0,ignored;
 always @(posedge clk)begin
  #1;for(p=0;p<2;p=p+1)if(event_valid[p])
   $display("E %0d %0d %0d %0d",p,event_error[p],event_data[p*16+:16],event_cell[p*16+:16]);
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length) ||
     !$value$plusargs("first=%d",first_edge) || !$value$plusargs("period=%d",period) ||
     !$value$plusargs("width=%d",width) || !$value$plusargs("ieee=%d",convention) ||
     !$value$plusargs("msb=%d",order))$fatal(1,"missing fixture");
  $readmemh(input_file,samples,0,length-1);
  ignored=$value$plusargs("stall=%d",stall);
  bit_ticks=period;word_bits=width;ieee=convention;msb=order;
  repeat(4)@(negedge clk);reset=0;
  for(i=0;i<length;i=i+1)begin
   previous_level=line;line=samples[i];start=i==first_edge;@(negedge clk);
   if(stall!=0)begin start=0;tick=0;repeat(3)@(negedge clk);tick=1;end
  end
  start=0;finish=1;@(negedge clk);finish=0;
  repeat(4)@(negedge clk);
  if(done!=3 || overflow!=0)$fatal(1,"receiver did not finish cleanly");
  $display("S %0d %0d %0d %0d",score_a,score_b,good_a,good_b);$finish(0);
 end
 initial begin #2000000;$fatal(1,"timeout");end
endmodule
