// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_alignment;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,left_present=1,right_present=1;
 reg signed [49:0] left_score=0,center_score=0,right_score=0;
 wire busy,done,invalid;wire signed [24:0] delta;
 stack_alignment dut(.*);
 integer fd,r,lp,rp,abort_kind,abort_delay,cycles,k;
 reg signed [49:0] l,c,rr;
 reg [4095:0] file_name;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d %d %d\n",l,c,rr,lp,rp,abort_kind,abort_delay);
   if(r!=7)$fatal(1,"fixture");
   if(abort_kind!=0)begin
    left_score=0;center_score=50'sh1000000000000;right_score=50'sh0800000000000;
    left_present=1;right_present=1;start=1;@(negedge clk);start=0;
    repeat(abort_delay)begin @(negedge clk);if(done)$fatal(1,"abort too late");end
    if(abort_kind==1)begin
     reset=1;@(negedge clk);reset=0;
     repeat(250)begin @(negedge clk);if(done || busy)$fatal(1,"old result after reset");end
    end
   end
   left_score=l;center_score=c;right_score=rr;left_present=lp;right_present=rp;
   start=1;@(negedge clk);start=0;cycles=0;
   while(!done)begin @(negedge clk);cycles=cycles+1;if(cycles>240)$fatal(1,"latency");end
   if(busy)$fatal(1,"busy result");
   $display("A %0d %0d %0d",invalid,delta,cycles);
   repeat(4)begin @(negedge clk);if(done)$fatal(1,"duplicate result");end
  end
  $fclose(fd);$finish(0);
 end
endmodule
