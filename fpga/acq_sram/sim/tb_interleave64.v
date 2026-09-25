// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-039
module tb;
 reg clk=0;always #4 clk=~clk;
 reg enable=0,consume=0,empty=0;reg [4:0] used=16;
 wire [79:0] q;wire [63:0] data;wire valid,pop,fault;
 integer frame=0,byteidx=0,n,epoch;
 interleave_gearbox64 dut(clk,enable,q,empty,used,consume,data,valid,pop,fault);
 genvar k;generate for(k=0;k<10;k=k+1)begin
  assign q[8*k+:8]=(epoch*37+frame*10+k)%256;
 end endgenerate
 always @(posedge clk)if(enable && consume && valid)begin
  for(integer b=0;b<8;b=b+1)
   if(data[8*b+:8]!==((epoch*37+byteidx+b)%256))
    $fatal(1,"epoch %d packing byte %d got %h",epoch,byteidx+b,data);
  byteidx=byteidx+8;if(pop)frame<=frame+1;
 end
 initial begin
  // Stop at every possible gearbox slot, then restart with distinct payload.
  for(epoch=0;epoch<10;epoch=epoch+1)begin
   @(negedge clk);enable=0;consume=0;
   repeat(2)@(negedge clk);
   frame=0;byteidx=0;empty=0;enable=1;consume=1;
   repeat(3)@(negedge clk);
   for(n=0;n<262144+epoch;n=n+1)begin
    @(negedge clk);
    if(n%117==0)begin consume=0;repeat(3)@(negedge clk);consume=1;end
   end
   if(fault || byteidx<2097152)$fatal(1,"full record fault=%b bytes=%d",fault,byteidx);
   enable=0;consume=0;
  end
  repeat(2)@(negedge clk);
  frame=0;byteidx=0;enable=1;consume=1;
  repeat(10)@(negedge clk);
  empty=1;repeat(6)@(negedge clk);
  if(!fault)$fatal(1,"underflow not detected");
  enable=0;@(negedge clk);if(fault)$fatal(1,"reset did not clear fault");
  $display("PASS 80-to-64 full records, byte order, pauses, ten restart epochs and underflow");$finish;
 end
endmodule
