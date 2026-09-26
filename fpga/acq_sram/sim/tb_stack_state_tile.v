// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_state_tile;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,request_valid=0,request_write=0,request_channel=0,response_ready=0;
 reg [31:0] request_bin=0;wire request_ready,response_valid,response_error,initialized;
 reg [68:0] write_sum=0,write_sum_a=0;reg [105:0] write_sum2=0;reg [31:0] write_count=0,write_count_a=0;
 wire [68:0] read_sum,read_sum_a;wire [105:0] read_sum2;wire [31:0] read_count,read_count_a;
 stack_state_tile dut(.*);
 integer fd,r,abort_delay,hold_cycles,cycles;reg [4095:0] file_name;reg [308:0] held;
 task wait_init;
 begin cycles=0;#1;while(!initialized)begin @(negedge clk);cycles=cycles+1;if(!initialized && (request_ready || response_valid))$fatal(1,"initialization handshake");if(cycles>641)$fatal(1,"initialization timeout");end end
 endtask
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");fd=$fopen(file_name,"r");
  repeat(3)@(negedge clk);reset=0;wait_init();
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d %d\n",request_write,request_bin,request_channel,write_sum,write_sum2,write_count,write_sum_a,write_count_a,hold_cycles,abort_delay);
   if(r!=10)$fatal(1,"fixture");
   request_valid=1;@(negedge clk);request_valid=0;
   request_bin=32'hffffffff;request_channel=0;request_write=0;write_sum=0;write_sum2=0;write_count=0;write_sum_a=0;write_count_a=0;
   if(abort_delay!=0)begin
    repeat(abort_delay)@(negedge clk);reset=1;@(negedge clk);reset=0;wait_init();
    $display("T 0 0 0 0 0 0");
   end else begin
    cycles=0;while(!response_valid)begin @(negedge clk);cycles=cycles+1;if(cycles>24)$fatal(1,"response timeout");end
    held={response_error,read_sum,read_sum2,read_count,read_sum_a,read_count_a};
    repeat(hold_cycles)begin @(negedge clk);if(request_ready || !response_valid || held!=={response_error,read_sum,read_sum2,read_count,read_sum_a,read_count_a})$fatal(1,"response changed");end
    $display("T %0d %0d %0d %0d %0d %0d",response_error,read_sum,read_sum2,read_count,read_sum_a,read_count_a);
    response_ready=1;@(negedge clk);response_ready=0;
   end
  end
  $finish;
 end
endmodule
