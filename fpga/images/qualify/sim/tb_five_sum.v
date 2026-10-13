// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// five_sum: each clock's two sums are those of the five valid samples ending
// at its first and its second sample, three clocks later; invalid words
// neither enter the sums nor make an output valid.
// TRLC-LINKS: REQ-SDS-011
module tb;
 reg clk=0;always #2 clk=~clk;
 reg valid=0;reg [7:0] first=0,second=0;
 wire [10:0] sf,ss;wire ov;
 five_sum dut(.clk(clk),.valid(valid),.first(first),.second(second),.sum_first(sf),.sum_second(ss),.out_valid(ov));
 // the valid samples so far, after four zeros (the history the module starts with)
 reg [7:0] hist[0:1000003];integer n=4,checked=0,k,seed=3;
 // the expected sums of the valid word sent three clocks ago
 reg [10:0] ef[0:3],es[0:3];reg ev[0:3];
 integer i;
 initial for(i=0;i<4;i=i+1)begin ef[i]=0;es[i]=0;ev[i]=0;hist[i]=0;end
 always @(posedge clk)begin
  if(ov!==ev[2] || (ov && (sf!==ef[2] || ss!==es[2])))$fatal(1,"at %0t: valid %b sums %0d %0d, expected %b %0d %0d",$time,ov,sf,ss,ev[2],ef[2],es[2]);
  if(ov)checked=checked+1;
  ev[2]<=ev[1];ef[2]<=ef[1];es[2]<=es[1];ev[1]<=ev[0];ef[1]<=ef[0];es[1]<=es[0];
  ev[0]<=valid;
  if(valid)begin
   hist[n]=first;hist[n+1]=second;
   ef[0]<=hist[n-4]+hist[n-3]+hist[n-2]+hist[n-1]+first;
   es[0]<=hist[n-3]+hist[n-2]+hist[n-1]+first+second;
   n=n+2;
  end
 end
 initial begin
  for(k=0;k<20000;k=k+1)begin
   @(negedge clk);valid=$random(seed)%4!=0;first=$random(seed);second=$random(seed);
  end
  if(checked<10000)$fatal(1,"only %0d sums checked",checked);
  $display("PASS five_sum: %0d clocks of sums of the five samples ending at each sample, invalid words skipped",checked);
  $finish;
 end
endmodule
