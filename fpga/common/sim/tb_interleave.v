// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-039
module tb;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,consume=0,empty=0;reg [4:0] used=16;
 wire [79:0] q;wire [31:0] data;wire valid,pop,fault;
 integer frame=0,byteidx=0,i,n;
 interleave_gearbox dut(clk,enable,q,empty,used,consume,data,valid,pop,fault);
 genvar k;generate for(k=0;k<10;k=k+1)begin assign q[8*k+:8]=(frame*10+k)%256;end endgenerate
 always @(posedge clk) if(enable && consume && valid)begin
  for(integer b=0;b<4;b=b+1)if(data[8*b+:8]!==((byteidx+b)%256))$fatal(1,"packing error byte %d got %h",byteidx+b,data);
  byteidx=byteidx+4;if(pop)frame<=frame+1;
 end
 initial begin
  #9;enable=1;consume=1;
  for(n=0;n<524288;n=n+1)begin @(negedge clk);if(n%117==0)begin consume=0;repeat(3)@(negedge clk);consume=1;end end
  if(fault || byteidx<2097140)$fatal(1,"full depth failure %d",byteidx);
  empty=1;repeat(6)@(negedge clk);if(!fault)$fatal(1,"underflow not detected");
  enable=0;@(negedge clk);if(fault)$fatal(1,"reset did not clear fault");
  $display("PASS 80-to-32 full-depth byte order, arbitrary pauses, underflow and rearm");$finish;
 end
endmodule
