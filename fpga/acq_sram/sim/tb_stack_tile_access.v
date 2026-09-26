// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_tile_access;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,command_valid=0,command_write=0,command_channel=0;reg [31:0] command_bin=0;
 wire command_ready,upload_ready,download_valid,completion_valid,completion_error,busy;
 reg upload_valid=0,download_ready=0,completion_ready=0;reg [15:0] upload_data=0;wire [15:0] download_data;
 reg engine_acquire=0,engine_release=0;wire engine_acquire_ready,engine_owned,initialized;
 reg engine_request_valid=0,engine_request_write=0,engine_request_channel=0;reg [31:0] engine_request_bin=0;
 wire engine_request_ready,engine_response_valid,engine_response_error;reg engine_response_ready=0;
 reg [68:0] engine_write_sum=0,engine_write_sum_a=0;reg [105:0] engine_write_sum2=0;reg [31:0] engine_write_count=0,engine_write_count_a=0;
 wire [68:0] engine_read_sum,engine_read_sum_a;wire [105:0] engine_read_sum2;wire [31:0] engine_read_count,engine_read_count_a;
 stack_tile_access dut(.*);
 integer i,cycles;reg [319:0] data,received;reg [308:0] held;
 task wait_init;
 begin #1;while(!initialized)begin @(negedge clk);if(!initialized && (command_ready || engine_acquire_ready))$fatal(1,"uninitialized ownership");end end
 endtask
 task completion;
 begin
  cycles=0;while(!completion_valid)begin @(negedge clk);cycles=cycles+1;if(cycles>700)$fatal(1,"completion timeout");end
  if(completion_error)$fatal(1,"host error");
  repeat(5)begin @(negedge clk);if(!busy || !completion_valid || engine_acquire_ready || engine_owned)$fatal(1,"host lost ownership before completion");end
  engine_acquire=0;completion_ready=1;@(negedge clk);completion_ready=0;
 end
 endtask
 initial begin
  repeat(3)@(negedge clk);reset=0;wait_init();
  // Simultaneous requests prefer the host; a half-upload cannot be interrupted.
  data={12'd0,32'h12345678,69'h13579,32'h87654321,106'h123456789abcdef,69'h123456789abcdef};
  command_valid=1;command_write=1;command_bin=3;command_channel=1;engine_acquire=1;#1;
  if(!command_ready || engine_acquire_ready)$fatal(1,"idle arbitration");
  @(negedge clk);command_valid=0;
  for(i=0;i<20;i=i+1)begin
   repeat(2)begin @(negedge clk);if(engine_owned || engine_acquire_ready)$fatal(1,"engine interrupted upload");end
   upload_valid=1;upload_data=data[16*i+:16];#1;if(!upload_ready)$fatal(1,"upload");@(negedge clk);upload_valid=0;
  end
  completion();
  // Engine ownership spans a read/write gap. Host requests must stay blocked.
  engine_acquire=1;#1;if(!engine_acquire_ready)$fatal(1,"acquire");@(negedge clk);engine_acquire=0;
  if(!engine_owned)$fatal(1,"not owned");
  command_valid=1;command_write=0;command_bin=3;command_channel=1;
  repeat(8)begin @(negedge clk);if(command_ready)$fatal(1,"host entered between engine operations");end
  engine_request_valid=1;engine_request_write=0;engine_request_bin=3;engine_request_channel=1;
  #1;if(!engine_request_ready)$fatal(1,"read not ready");@(negedge clk);engine_request_valid=0;
  cycles=0;while(!engine_response_valid)begin @(negedge clk);cycles=cycles+1;if(cycles>30)$fatal(1,"read timeout");end
  if(engine_response_error || {12'd0,engine_read_count_a,engine_read_sum_a,engine_read_count,engine_read_sum2,engine_read_sum}!==data)$fatal(1,"host/engine layout mismatch");
  held={engine_response_error,engine_read_count_a,engine_read_sum_a,engine_read_count,engine_read_sum2,engine_read_sum};
  repeat(7)begin @(negedge clk);if(!engine_owned || command_ready || held!=={engine_response_error,engine_read_count_a,engine_read_sum_a,engine_read_count,engine_read_sum2,engine_read_sum})$fatal(1,"read not held");end
  engine_response_ready=1;@(negedge clk);engine_response_ready=0;
  repeat(4)begin @(negedge clk);if(!engine_owned || command_ready)$fatal(1,"engine lock dropped in gap");end
  engine_write_sum={69{1'b1}};engine_write_sum2={106{1'b1}};engine_write_sum_a=69'h123456789abcd;
  engine_write_count=32'h13579bdf;engine_write_count_a=32'h2468ace0;
  data={12'd0,engine_write_count_a,engine_write_sum_a,engine_write_count,engine_write_sum2,engine_write_sum};
  engine_request_valid=1;engine_request_write=1;#1;if(!engine_request_ready)$fatal(1,"write not ready");
  @(negedge clk);engine_request_valid=0;
  engine_release=1;@(negedge clk);engine_release=0;
  // Releasing must prevent a second request and retain ownership until the ack.
  engine_request_valid=1;
  while(!engine_response_valid)begin @(negedge clk);if(!engine_owned || command_ready || engine_request_ready)$fatal(1,"premature release");end
  repeat(5)begin @(negedge clk);if(!engine_owned || command_ready || engine_request_ready)$fatal(1,"unconsumed write ack released");end
  engine_request_valid=0;engine_response_ready=1;@(negedge clk);engine_response_ready=0;
  if(engine_owned)$fatal(1,"release not completed");
  // The already-waiting host request can now observe the fully committed state.
  #1;if(!command_ready)$fatal(1,"host not resumed");@(negedge clk);command_valid=0;received=0;i=0;
  cycles=0;while(!completion_valid)begin
   if(download_valid)begin received[16*i+:16]=download_data;i=i+1;download_ready=1;end
   @(negedge clk);download_ready=0;cycles=cycles+1;if(cycles>60)$fatal(1,"download timeout");
  end
  if(i!=20 || received!==data || completion_error)$fatal(1,"engine/host layout mismatch");completion();
  // Reset revokes an engine lock and forces RAM initialization before reuse.
  engine_acquire=1;@(negedge clk);engine_acquire=0;reset=1;@(negedge clk);reset=0;
  if(engine_owned)$fatal(1,"reset retained ownership");wait_init();
  $display("PASS exclusive ownership, full-width host/engine exchange, in-flight release and shared reset");$finish;
 end
 initial begin repeat(5000)@(negedge clk);$fatal(1,"global timeout");end
endmodule
