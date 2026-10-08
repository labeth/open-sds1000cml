// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_resample_store;
 reg clk=0;always #4 clk=~clk;
 reg [31:0] first_bin=0;
 reg reset=1,start=0,odd=0;reg [31:0] hit_position=0,bin_count=8,factor=1,record_samples=32;
 reg signed [24:0] delta=0;reg [1:0] channel_mask=3;
 wire sample_request_valid,sample_response_ready;wire [31:0] sample_index;
 wire sample_request_ready=!sample_pending && !sample_response_valid && tick%4!=0;
 reg sample_response_valid=0,sample_response_error=0;
 reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 wire request_valid,request_write,request_channel;wire [31:0] request_bin;
 reg request_ready=0,response_valid=0,response_error=0;wire response_ready;
 wire [68:0] write_sum,write_sum_a;wire [105:0] write_sum2;wire [31:0] write_count,write_count_a;
 reg [68:0] read_sum=0,read_sum_a=0;reg [105:0] read_sum2=0;reg [31:0] read_count=0,read_count_a=0;
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
 reg [68:0] sums[0:15],sums_a[0:15];reg [105:0] squares[0:15];reg [31:0] counts[0:15],counts_a[0:15];
 integer tick=0,pending=0,delay=0,operations=0,fail_at=0,address=0,i,j,cycles,expected_ops=0;
 reg wr=0,err=0;reg [68:0] ws=0,wa=0;reg [105:0] wq=0;reg [31:0] wc=0,wca=0;
 reg held=0;reg [341:0] held_request;
 always @(posedge clk)begin
  tick<=tick+1;request_ready<=tick%5!=0 && tick%5!=1;
  if(reset)begin
   pending<=0;response_valid<=0;operations<=0;held<=0;
   for(i=0;i<16;i=i+1)begin sums[i]<=0;sums_a[i]<=0;squares[i]<=0;counts[i]<=0;counts_a[i]<=0;end
  end else begin
   if(held && (!request_valid || held_request!=={request_write,request_bin,request_channel,write_sum,write_sum2,write_count,write_sum_a,write_count_a}))$fatal(1,"request changed under stall");
   held<=request_valid && !request_ready;
   held_request<={request_write,request_bin,request_channel,write_sum,write_sum2,write_count,write_sum_a,write_count_a};
   if(request_valid && request_ready)begin
    if(pending || response_valid)$fatal(1,"overlapping operation");
    if(request_bin>=8)$fatal(1,"address");
    pending<=1;delay<=tick%7+1;operations<=operations+1;
    address<=request_bin*2+request_channel;wr<=request_write;err<=operations+1==fail_at;
    ws<=write_sum;wq<=write_sum2;wc<=write_count;wa<=write_sum_a;wca<=write_count_a;
   end
   if(pending)begin
    if(delay!=0)delay<=delay-1;
    else begin
     response_valid<=1;response_error<=err;pending<=0;
     read_sum<=sums[address];read_sum2<=squares[address];read_count<=counts[address];read_sum_a<=sums_a[address];read_count_a<=counts_a[address];
     if(wr && !err)begin sums[address]<=ws;squares[address]<=wq;counts[address]<=wc;sums_a[address]<=wa;counts_a[address]<=wca;end
    end
   end
   if(response_valid && response_ready)response_valid<=0;
  end
 end

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
   if(invalid || pending || response_valid)$fatal(1,"completion before writes acknowledged");
   if(sample_reads!=reads_expected || operations!=ops_expected)$fatal(1,"operation count");
   @(negedge clk);
  end
  for(j=0;j<16;j=j+1)$display("B %0d %0d %0d %0d %0d %0d",j,sums[j],squares[j],counts[j],sums_a[j],counts_a[j]);
  // Both adapter errors poison the whole logical stack and reject new starts.
  for(j=0;j<3;j=j+1)begin
   reset=1;@(negedge clk);reset=0;sample_fail_at=j==0 ? 1:0;fail_at=j==1 ? 1:j==2 ? 2:0;
   hit_position=1;delta=0;factor=4;channel_mask=3;odd=1;launch();
   if(!invalid)$fatal(1,"missing invalid");
   start=1;repeat(20)begin @(negedge clk);if(busy || request_valid || sample_request_valid)$fatal(1,"restart after fault");end start=0;
  end
  reset=1;@(negedge clk);reset=0;sample_fail_at=0;fail_at=0;launch();if(invalid)$fatal(1,"reset recovery");
  // Shared reset cancels in-flight sample/state operations and clears the
  // logical epoch. No old write or response may contaminate the next hit.
  for(j=0;j<5;j=j+1)begin
   start=1;@(negedge clk);start=0;
   repeat(j*100+2)@(negedge clk);
   reset=1;@(negedge clk);reset=0;
   repeat(20)begin
    @(negedge clk);
    if(busy || done || request_valid || sample_request_valid || response_valid || sample_response_valid)$fatal(1,"stale work after shared reset");
   end
   launch();if(invalid)$fatal(1,"aborted hit recovery");
   for(i=0;i<16;i=i+1)if(counts[i]!=1 || counts_a[i]!=1)$fatal(1,"old epoch contribution");
  end
  $display("PASS connected resampling, persistence, faults and recovery");$finish;
 end
endmodule
