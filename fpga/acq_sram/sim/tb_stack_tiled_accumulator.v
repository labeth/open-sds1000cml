// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_tiled_accumulator;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0;wire start_ready,busy,done,invalid,initialized,host_busy;
 reg [31:0] hit_position=0,bin_count=8,factor=3,record_samples=32,first_bin=0;
 reg signed [24:0] delta=0;reg [1:0] channel_mask=3;reg odd=0;
 wire sample_request_valid,sample_response_ready;wire [31:0] sample_index;
 reg sample_response_valid=0,sample_response_error=0;reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 integer tick=0,pending=0,delay=0,address=0,sample_reads=0,fail_at=0;
 wire sample_request_ready=!pending && !sample_response_valid && tick%5!=0;
 reg command_valid=0,command_write=0,command_channel=0;reg [31:0] command_bin=0;
 wire command_ready,upload_ready,download_valid,completion_valid,completion_error;
 reg upload_valid=0,download_ready=0,completion_ready=0;reg [15:0] upload_data=0;wire [15:0] download_data;
 stack_tiled_accumulator dut(.*);
 always @(posedge clk)begin
  tick<=tick+1;
  if(reset)begin pending<=0;sample_response_valid<=0;sample_reads<=0;end
  else begin
   if(sample_request_valid && sample_request_ready)begin
    if(sample_index>=31)$fatal(1,"sample range");
    pending<=1;delay<=tick%7+1;address<=sample_index;sample_reads<=sample_reads+1;
   end
   if(pending)begin
    if(delay!=0)delay<=delay-1;
    else begin
     pending<=0;sample_response_valid<=1;sample_response_error<=sample_reads==fail_at;
     ch0_left<=(address*37+5)*37'd1048576;ch0_right<=((address+1)*37+5)*37'd1048576;
     ch1_left<=(address*19+3)*37'd1048576;ch1_right<=((address+1)*19+3)*37'd1048576;
    end
   end
   if(sample_response_valid && sample_response_ready)sample_response_valid<=0;
  end
 end
 integer cycles,i,fd,r,op;reg [319:0] payload,received;reg [4095:0] file_name;
 task wait_init;
 begin #1;while(!initialized)@(negedge clk);end
 endtask
 task host;
 begin
  command_valid=1;#1;cycles=0;
  while(!command_ready)begin @(negedge clk);cycles=cycles+1;if(cycles>700)$fatal(1,"host ready timeout");end
  @(negedge clk);command_valid=0;
  if(command_write)for(i=0;i<20;i=i+1)begin
   repeat(i%3)@(negedge clk);
   upload_valid=1;upload_data=payload[16*i+:16];#1;if(!upload_ready)$fatal(1,"upload blocked");
   @(negedge clk);upload_valid=0;
  end
  received=0;i=0;cycles=0;
  while(!completion_valid)begin
   if(start_ready)$fatal(1,"hit could interrupt host transfer");
   if(download_valid)begin
    repeat(i%3)@(negedge clk);
    received[16*i+:16]=download_data;i=i+1;download_ready=1;
   end
   @(negedge clk);download_ready=0;cycles=cycles+1;if(cycles>700)$fatal(1,"host completion timeout");
  end
  if(completion_error || (!command_write && i!=20))$fatal(1,"host result");
  repeat(3)begin @(negedge clk);if(start_ready)$fatal(1,"hit interrupted held completion");end
  completion_ready=1;@(negedge clk);completion_ready=0;
 end
 endtask
 task hit;
 begin
  start=1;#1;if(!start_ready)$fatal(1,"hit not ready");
  @(negedge clk);start=0;cycles=0;
  // Geometry must have been latched at acceptance.
  hit_position=32'hffffffff;first_bin=32'hffffffff;factor=0;delta=0;channel_mask=0;odd=0;
  while(!done)begin
   @(negedge clk);cycles=cycles+1;if(cycles>10000)$fatal(1,"hit timeout");
   start=cycles==15;command_valid=cycles>=5 && cycles<=12;
   if(command_valid && command_ready)$fatal(1,"host interrupted hit");
  end
  start=0;command_valid=0;
  if(busy || dut.access.engine_owned)$fatal(1,"completion before ownership release");
  @(negedge clk);
 end
 endtask
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");fd=$fopen(file_name,"r");
  repeat(3)@(negedge clk);reset=0;wait_init();
  while(!$feof(fd))begin
   r=$fscanf(fd,"%d",op);if(r!=1)$fatal(1,"opcode");
   case(op)
    0:begin r=$fscanf(fd," %d %d %h\n",command_bin,command_channel,payload);if(r!=3)$fatal(1,"upload fixture");command_write=1;host();end
    1:begin r=$fscanf(fd," %d %d\n",command_bin,command_channel);if(r!=2)$fatal(1,"download fixture");command_write=0;host();$display("S %h",received);end
    2:begin r=$fscanf(fd," %d %d %d %d %d %d %d\n",hit_position,first_bin,delta,factor,channel_mask,odd,bin_count);if(r!=7)$fatal(1,"hit fixture");hit();if(invalid)$fatal(1,"unexpected hit error");end
    default:$fatal(1,"bad opcode");
   endcase
  end
  // A source fault after processing starts invalidates the whole tile.
  fail_at=sample_reads+3;hit_position=1;first_bin=0;factor=4;delta=0;channel_mask=3;odd=1;bin_count=8;hit();
  if(!invalid)$fatal(1,"missing source fault");
  start=1;command_valid=1;repeat(20)begin @(negedge clk);if(start_ready || command_ready || busy || sample_request_valid)$fatal(1,"poisoned tile exposed");end start=0;command_valid=0;
  reset=1;@(negedge clk);reset=0;fail_at=0;wait_init();
  // Oversized tiles reject before issuing a sample read or acquiring ownership.
  hit_position=1;first_bin=0;factor=4;bin_count=33;delta=0;channel_mask=3;hit();
  if(!invalid || sample_reads!=0)$fatal(1,"oversize tile accessed memory");
  reset=1;@(negedge clk);reset=0;wait_init();
  // Arithmetic failure also blocks readback of a partially updated logical tile.
  command_write=1;command_bin=0;command_channel=0;payload=320'hffffffff<<175;host();
  hit_position=1;first_bin=0;factor=4;bin_count=8;delta=0;channel_mask=1;odd=1;hit();
  if(!invalid || command_ready)$fatal(1,"overflow tile exposed");
  reset=1;@(negedge clk);reset=0;wait_init();
  hit_position=1;first_bin=0;factor=4;bin_count=8;delta=0;channel_mask=3;odd=1;hit();if(invalid)$fatal(1,"reset recovery");
  $display("PASS host-restored accumulation, ownership, invalidation and reset recovery");$finish;
 end
 initial begin repeat(500000)@(negedge clk);$fatal(1,"global timeout");end
endmodule
