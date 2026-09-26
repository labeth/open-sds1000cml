// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_positions;
 reg [31:0] first_bin=0;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,ready=0;
 reg [31:0] hit_position=0,bin_count=0,factor=0,record_samples=0;
 reg signed [24:0] delta=0;
 wire valid,eligible,busy,done,invalid;
 wire [31:0] bin;wire signed [33:0] sample_index;wire [23:0] fraction;
 stack_positions dut(.*);
 reg [31:0] p,b,f,n,o;reg signed [24:0] d;
 integer fd,r,abort_kind,abort_delay,cycles=0;
 reg [4095:0] file_name;reg holding=0;reg [90:0] held;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");
  r=$fscanf(fd,"%d %d %d %d %d %d %d %d\n",p,b,f,n,d,abort_kind,abort_delay,o);
  if(r!=8)$fatal(1,"fixture");$fclose(fd);
  repeat(3)@(negedge clk);reset=0;
  if(abort_kind!=0)begin
   first_bin=11;hit_position=9999;bin_count=99;factor=13;record_samples=999;delta=25'sh800000;
   start=1;@(negedge clk);start=0;repeat(abort_delay)@(negedge clk);
   if(abort_kind==1)begin
    reset=1;@(negedge clk);reset=0;
    repeat(100)begin @(negedge clk);if(done || valid || busy)$fatal(1,"old position after reset");end
   end
  end
  first_bin=o;hit_position=p;bin_count=b;factor=f;record_samples=n;delta=d;
  start=1;@(negedge clk);start=0;first_bin=~o;
  while(!done)begin
   ready=cycles%4==0;
   if(valid)begin
    if(holding && held!=={bin,sample_index,fraction,eligible})$fatal(1,"unstable position");
    held={bin,sample_index,fraction,eligible};holding=!ready;
    if(ready)$display("P %0d %0d %0d %0d",bin,sample_index,fraction,eligible);
   end
   @(negedge clk);cycles=cycles+1;if(cycles>100000)$fatal(1,"timeout");
  end
  if(busy)$fatal(1,"busy result");
  $display("D %0d",invalid);
  repeat(4)begin @(negedge clk);if(done || valid)$fatal(1,"late result");end
  $finish(0);
 end
endmodule
