// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_interpolate;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0;
 reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 reg [23:0] fraction=0;reg [1:0] channel_mask=0;
 wire busy,done;wire [36:0] ch0_value,ch1_value;wire [1:0] result_mask;
 stack_interpolate dut(.*);
 reg [36:0] a,b,c,d;reg [23:0] w;reg [1:0] m;
 integer fd,r,abort_kind,abort_delay,cycles;
 reg [4095:0] file_name;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d %d %d %d\n",a,b,c,d,w,m,abort_kind,abort_delay);
   if(r!=8)$fatal(1,"fixture");
   if(abort_kind!=0)begin
    ch0_left=0;ch0_right=37'h1fffffffff;ch1_left=0;ch1_right=37'h1fffffffff;fraction=24'h800000;channel_mask=3;
    start=1;@(negedge clk);start=0;
    repeat(abort_delay)begin @(negedge clk);if(done)$fatal(1,"abort too late");end
    if(abort_kind==1)begin
     reset=1;@(negedge clk);reset=0;
     repeat(60)begin @(negedge clk);if(done || busy)$fatal(1,"old interpolation after reset");end
    end
   end
   ch0_left=a;ch0_right=b;ch1_left=c;ch1_right=d;fraction=w;channel_mask=m;
   start=1;@(negedge clk);start=0;cycles=0;
   while(!done)begin @(negedge clk);cycles=cycles+1;if(cycles>60)$fatal(1,"latency");end
   if(busy)$fatal(1,"busy result");
   $display("I %0d %0d %0d %0d",ch0_value,ch1_value,result_mask,cycles);
   repeat(4)begin @(negedge clk);if(done)$fatal(1,"duplicate result");end
  end
  $fclose(fd);$finish(0);
 end
endmodule
