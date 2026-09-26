// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_tile_transfer;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,command_valid=0,command_write=0,command_channel=0;
 reg [31:0] command_bin=0;
 wire command_ready,upload_ready,download_valid,completion_valid,completion_error,busy;
 reg upload_valid=0,download_ready=0,completion_ready=0;reg [15:0] upload_data=0;wire [15:0] download_data;
 wire request_valid,request_ready,request_write,request_channel,response_valid,response_ready,response_error;
 wire [31:0] request_bin,write_count,write_count_a,read_count,read_count_a;
 wire [68:0] write_sum,write_sum_a,read_sum,read_sum_a;wire [105:0] write_sum2,read_sum2;
 stack_tile_transfer dut(.*);
 wire tile_ready,tile_valid,tile_error,initialized;
 integer tick=0,requests=0,uploaded=0;wire allow_request=tick%5!=0,allow_response=tick%4!=0;
 assign request_ready=tile_ready && allow_request;
 assign response_valid=tile_valid && allow_response;assign response_error=tile_error;
 stack_state_tile tile(.clk(clk),.reset(reset),.request_valid(request_valid && allow_request),.request_ready(tile_ready),
 .request_write(request_write),.request_bin(request_bin),.request_channel(request_channel),
 .write_sum(write_sum),.write_sum2(write_sum2),.write_sum_a(write_sum_a),.write_count(write_count),.write_count_a(write_count_a),
 .response_valid(tile_valid),.response_ready(response_ready && allow_response),.response_error(tile_error),
 .read_sum(read_sum),.read_sum2(read_sum2),.read_sum_a(read_sum_a),.read_count(read_count),.read_count_a(read_count_a),.initialized(initialized));
 always @(posedge clk)begin
  tick<=tick+1;
  if(reset)begin requests<=0;uploaded<=0;end
  else begin
   if(command_valid && command_ready)uploaded<=0;
   if(upload_valid && upload_ready)uploaded<=uploaded+1;
   if(request_valid && request_ready)begin
    requests<=requests+1;
    if(request_write && uploaded!=20)$fatal(1,"partial upload reached memory");
   end
  end
 end
 integer fd,r,i,abort_after,stall,cycles,read_words,before_requests;
 reg wr;reg [31:0] bn;reg ch;reg [319:0] data,received;reg [4095:0] file_name;reg [15:0] held;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");fd=$fopen(file_name,"r");
  repeat(3)@(negedge clk);reset=0;
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d %d %d %h %d %d\n",wr,bn,ch,data,abort_after,stall);if(r!=6)$fatal(1,"fixture");
   before_requests=requests;received=0;read_words=0;
   command_valid=1;command_write=wr;command_bin=bn;command_channel=ch;#1;
   if(!command_ready)$fatal(1,"not ready for command");
   @(negedge clk);command_valid=0;command_bin=32'hffffffff;command_channel=~ch;command_write=~wr;
   if(wr)begin
    for(i=0;i<20;i=i+1)begin
     repeat((i+stall)%4)begin @(negedge clk);if(request_valid || command_ready)$fatal(1,"partial upload visible");end
     upload_valid=1;upload_data=data[16*i+:16];#1;if(!upload_ready)$fatal(1,"upload not ready");
     @(negedge clk);upload_valid=0;
     if(abort_after!=0 && i+1==abort_after)begin
      if(requests!=before_requests)$fatal(1,"aborted upload wrote state");
      reset=1;@(negedge clk);reset=0;i=20;
     end
    end
   end
   if(abort_after!=0)begin
    repeat(3)begin @(negedge clk);if(completion_valid || download_valid || busy)$fatal(1,"stale aborted command");end
    $display("X 0 0");
   end else begin
    cycles=0;
    while(!completion_valid)begin
     if(command_ready)$fatal(1,"command accepted while busy");
     if(download_valid)begin
      held=download_data;download_ready=0;
      repeat(stall)begin @(negedge clk);if(!download_valid || held!==download_data)$fatal(1,"unstable download");end
      received[read_words*16+:16]=download_data;read_words=read_words+1;download_ready=1;
     end
     @(negedge clk);download_ready=0;cycles=cycles+1;if(cycles>900)$fatal(1,"timeout");
    end
    if((wr || completion_error) ? read_words!=0:read_words!=20)$fatal(1,"download length");
    if(wr && |data[319:308] && requests!=before_requests)$fatal(1,"bad padding wrote memory");
    repeat(stall+1)begin @(negedge clk);if(!completion_valid || !busy || command_ready)$fatal(1,"completion not held");end
    $display("X %0d %h",completion_error,received);
    completion_ready=1;@(negedge clk);completion_ready=0;
   end
  end
  $finish;
 end
endmodule
