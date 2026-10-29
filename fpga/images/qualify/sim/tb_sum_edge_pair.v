// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// sum_edge_pair at 8 bits makes exactly sram_edge_pair's edges (one clock
// sooner: it has no split comparison stage), on random walks, ramps and
// noise through a level with its hysteresis band, with enables and invalid
// words.
// TRLC-LINKS: REQ-SDS-011
module tb;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0;reg [7:0] first=128,second=128,level=128,low=120,high=136;
 wire r_ref,f_ref,r_sum,f_sum;
 sram_edge_pair #(.PIPELINED(1)) reference(.clk(clk),.enable(enable),.valid(valid),.first(first),.second(second),
  .level(level),.low_level(low),.high_level(high),.sustain(1'b0),.rise(r_ref),.fall(f_ref));
 sum_edge_pair #(.W(8)) dut(.clk(clk),.enable(enable),.valid(valid),.first(first),.second(second),
  .level(level),.low_level(low),.high_level(high),.rise(r_sum),.fall(f_sum));
 reg r_d=0,f_d=0;integer edges=0,n,seed=7;integer x=128;
 always @(posedge clk)begin
  r_d<=r_sum;f_d<=f_sum;
  if(r_ref!==r_d || f_ref!==f_d)$fatal(1,"at %0t: reference rise %b fall %b, sums rise %b fall %b",$time,r_ref,f_ref,r_d,f_d);
  if(r_ref || f_ref)edges=edges+1;
 end
 initial begin
  // the reference's local copies of the levels settle first
  repeat(4)@(negedge clk);enable=1;
  for(n=0;n<200000;n=n+1)begin
   @(negedge clk);
   valid=$random(seed)%8!=0;
   if(n%50000==25000)begin enable=0;@(negedge clk);enable=1;end
   // a random walk across the band, with jumps
   x=x+($random(seed)%7);if($random(seed)%97==0)x=x+($random(seed)%40);
   if(x<90)x=90;if(x>170)x=170;
   first=x+($random(seed)%5);second=x+($random(seed)%5);
  end
  if(edges<1000)$fatal(1,"only %0d edges compared",edges);
  $display("PASS sum_edge_pair: %0d edges identical to sram_edge_pair over 200000 clocks",edges);
  $finish;
 end
endmodule
