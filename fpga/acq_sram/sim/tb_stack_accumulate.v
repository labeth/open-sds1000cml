// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_accumulate;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,enabled=0,odd=0,ready=0;
 reg [36:0] value=0;reg [68:0] sum=0,sum_a=0;
 reg [105:0] sum2=0;reg [31:0] count=0,count_a=0;
 wire valid,busy,invalid;wire [68:0] result_sum,result_sum_a;
 wire [105:0] result_sum2;wire [31:0] result_count,result_count_a;
 stack_accumulate dut(.*);
 integer fd,r,cycles,hold_cycles,abort_kind,abort_delay;
 reg [4095:0] file_name;
 reg [308:0] held;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d %d %d\n",enabled,odd,value,sum,sum2,count,sum_a,count_a,hold_cycles,abort_kind,abort_delay);
   if(r!=11)$fatal(1,"fixture");
   start=1;@(negedge clk);start=0;
   if(abort_kind!=0)begin
    repeat(abort_delay)@(negedge clk);
    if(abort_kind==1)begin
     reset=1;@(negedge clk);reset=0;
     repeat(220)begin @(negedge clk);if(valid || busy)$fatal(1,"stale reset result");end
    end
    start=1;@(negedge clk);start=0;
   end
   // Poison inputs after launch: the transaction must use latched values.
   value=0;sum=0;sum2=0;count=0;sum_a=0;count_a=0;enabled=0;odd=0;
   cycles=0;
   while(!valid)begin @(negedge clk);cycles=cycles+1;if(cycles>210)$fatal(1,"latency");end
   held={invalid,result_sum,result_sum2,result_count,result_sum_a,result_count_a};
   repeat(hold_cycles)begin
    @(negedge clk);
    if(!valid || !busy || held!=={invalid,result_sum,result_sum2,result_count,result_sum_a,result_count_a})$fatal(1,"unstable result");
   end
   $display("A %0d %0d %0d %0d %0d %0d %0d",invalid,result_sum,result_sum2,result_count,result_sum_a,result_count_a,cycles);
   ready=1;@(negedge clk);ready=0;
   repeat(3)begin @(negedge clk);if(valid || busy)$fatal(1,"duplicate result");end
  end
  $fclose(fd);$finish(0);
 end
endmodule
