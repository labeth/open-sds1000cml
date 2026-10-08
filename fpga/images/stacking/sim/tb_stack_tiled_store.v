// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_tiled_store;
 reg clk=0;always #4 clk=~clk;
 reg [31:0] first_bin=0;
 reg reset=1,start=0,odd=0;reg [31:0] hit_position=0,bin_count=8,factor=1,record_samples=32;
 reg signed [24:0] delta=0;reg [1:0] channel_mask=3;
 wire sample_request_valid,sample_response_ready;wire [31:0] sample_index;
 wire sample_request_ready=!sample_pending && !sample_response_valid && tick%4!=0;
 reg sample_response_valid=0,sample_response_error=0;
 reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 wire request_valid,request_write,request_channel;wire [31:0] request_bin;
 wire request_ready,response_valid,response_error;wire response_ready;
 wire [68:0] write_sum,write_sum_a;wire [105:0] write_sum2;wire [31:0] write_count,write_count_a;
 wire [68:0] read_sum,read_sum_a;wire [105:0] read_sum2;wire [31:0] read_count,read_count_a;
 wire busy,done,invalid;
 stack_resample_store dut(.clk(clk),.reset(reset),.start(start),.hit_position(hit_position),.bin_count(bin_count),.first_bin(first_bin),
 .factor(factor),.record_samples(record_samples),.delta(delta),.channel_mask(channel_mask),.odd(odd),
 .sample_request_valid(sample_request_valid),.sample_request_ready(sample_request_ready),.sample_index(sample_index),
 .sample_response_valid(sample_response_valid),.sample_response_error(sample_response_error),.sample_response_ready(sample_response_ready),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
 .state_request_valid(request_valid),.state_request_ready(request_ready),.state_write(request_write),.state_bin(request_bin),.state_channel(request_channel),
 .write_sum(write_sum),.write_sum2(write_sum2),.write_sum_a(write_sum_a),.write_count(write_count),.write_count_a(write_count_a),
 .state_response_valid(response_valid),.state_response_error(response_error),.state_response_ready(response_ready),
 .read_sum(read_sum),.read_sum2(read_sum2),.read_sum_a(read_sum_a),.read_count(read_count),.read_count_a(read_count_a),
 .busy(busy),.done(done),.invalid(invalid));

 integer tick=0,operations=0,cycles,j;
 reg host=0,host_valid=0,host_channel=0,host_ready=0;reg [31:0] host_bin=0;
 wire tile_ready,tile_valid,tile_error,initialized;
 wire allow_request=tick%5!=0,allow_response=tick%4!=0;
 assign request_ready=!host && tile_ready && allow_request;
 assign response_valid=!host && tile_valid && allow_response;
 assign response_error=tile_error;
 always @(posedge clk)begin tick<=tick+1;if(reset)operations<=0;else if(request_valid && request_ready)operations<=operations+1;end
 stack_state_tile tile(.clk(clk),.reset(reset),
 .request_valid(host ? host_valid:request_valid && allow_request),.request_ready(tile_ready),
 .request_write(host ? 1'b0:request_write),.request_bin(host ? host_bin:request_bin),.request_channel(host ? host_channel:request_channel),
 .write_sum(write_sum),.write_sum2(write_sum2),.write_sum_a(write_sum_a),.write_count(write_count),.write_count_a(write_count_a),
 .response_valid(tile_valid),.response_ready(host ? host_ready:response_ready && allow_response),.response_error(tile_error),
 .read_sum(read_sum),.read_sum2(read_sum2),.read_sum_a(read_sum_a),.read_count(read_count),.read_count_a(read_count_a),.initialized(initialized));
 integer sample_pending=0,sample_delay=0,sample_address=0,sample_reads=0,sample_fail_at=0;
 always @(posedge clk)begin
  if(reset)begin sample_pending<=0;sample_response_valid<=0;sample_reads<=0;end
  else begin
   if(sample_request_valid && sample_request_ready)begin
    if(sample_index>=31)$fatal(1,"sample bound");
    sample_pending<=1;sample_delay<=tick%5+1;sample_address<=sample_index;sample_reads<=sample_reads+1;
   end
   if(sample_pending)begin
    if(sample_delay!=0)sample_delay<=sample_delay-1;
    else begin
     sample_pending<=0;sample_response_valid<=1;sample_response_error<=sample_reads==sample_fail_at;
     ch0_left<=(sample_address*37+5)*37'd1048576;ch0_right<=((sample_address+1)*37+5)*37'd1048576;
     ch1_left<=(sample_address*19+3)*37'd1048576;ch1_right<=((sample_address+1)*19+3)*37'd1048576;
    end
   end
   if(sample_response_valid && sample_response_ready)sample_response_valid<=0;
  end
 end
 task launch;
 begin start=1;@(negedge clk);start=0;cycles=0;
  while(!done)begin
   @(negedge clk);cycles=cycles+1;if(cycles>5000)$fatal(1,"timeout");
   // Busy-time starts cannot replace a partially committed hit.
   start=cycles==100;
  end
  start=0;if(busy)$fatal(1,"busy completion");
 end
 endtask

 integer fd,r,reads_expected=0,ops_expected=0,dr,dp;
 reg [4095:0] file_name;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");fd=$fopen(file_name,"r");
  repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %d %d %d %d %d\n",hit_position,delta,factor,channel_mask,odd,dr,dp,first_bin);if(r!=8)$fatal(1,"fixture");
   reads_expected=reads_expected+dr;ops_expected=ops_expected+dp;launch();
   if(invalid || tile_valid)$fatal(1,"completion before state acknowledgement");
   if(sample_reads!=reads_expected || operations!=ops_expected)$fatal(1,"operation count");
   @(negedge clk);
  end
  host=1;
  for(j=0;j<64;j=j+1)begin
   host_bin=j/2;host_channel=j%2;host_valid=1;#1;
   while(!tile_ready)@(negedge clk);
   @(negedge clk);host_valid=0;
   while(!tile_valid)@(negedge clk);
   if(tile_error)$fatal(1,"host state read");
   if(j<16)$display("B %0d %0d %0d %0d %0d %0d",j,read_sum,read_sum2,read_count,read_sum_a,read_count_a);
   else if({read_sum,read_sum2,read_count,read_sum_a,read_count_a}!=0)$fatal(1,"untouched tile bin changed");
   repeat(3)@(negedge clk);
   host_ready=1;@(negedge clk);host_ready=0;
  end
  $display("PASS connected pipeline with physical tile RAM and offline readout");$finish;
 end
endmodule
