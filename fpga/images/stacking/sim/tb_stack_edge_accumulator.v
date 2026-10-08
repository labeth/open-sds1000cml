// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Script-driven edge-locked stacking bench. +wave holds one "ch1ch0" hex pair
// per sample; +input holds commands:
//  s first samples pre separation channel falling level hysteresis bins first_bin factor initial mask
//  r bin channel   (download; prints "S <hex>")   p index (peek; prints "P <hex>")
//  q enable count stride pre threshold   t n byte0 .. byte(n-1)   (template)
// TRLC-LINKS: REQ-SDS-141
module tb_stack_edge_accumulator #(parameter BINS=16);
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,peek=0;wire start_ready,busy,done,invalid,initialized,host_busy;
 reg [31:0] first_index=0,record_samples=0,pre_samples=0,min_separation=0,bin_count=0,first_bin=0,factor=1,initial_hits=0;
 reg channel=0,falling=0;reg [7:0] level=0,hysteresis=0;reg [1:0] channel_mask=3;
 wire [31:0] accepted_hits,crossings,peek_data,rejected;
 reg qualify_enable=0;reg [10:0] qualify_count=0;reg [7:0] qualify_stride=1;reg [31:0] qualify_pre=0,qualify_threshold=0;
 wire [9:0] template_address;reg [7:0] template_data=0;reg [7:0] template[0:1023];
 always @(posedge clk)template_data<=template[template_address];
 wire request_valid,response_ready;wire [31:0] request_index;
 reg response_valid=0,response_error=0;reg [7:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 reg command_valid=0,command_write=0,command_channel=0;reg [31:0] command_bin=0;
 wire command_ready,upload_ready,download_valid,completion_valid,completion_error;
 reg upload_valid=0,download_ready=0,completion_ready=0;reg [15:0] upload_data=0;wire [15:0] download_data;
 stack_edge_accumulator #(.BINS(BINS)) dut(.*);
 reg [15:0] wave[0:65535];
 integer tick=0,pending=0,delay=0,address=0;
 wire request_ready=!pending && !response_valid && tick%5!=0;
 always @(posedge clk)begin
  tick<=tick+1;
  if(reset)begin pending<=0;response_valid<=0;end
  else begin
   if(request_valid && request_ready)begin
    if(request_index+1>=record_samples)$fatal(1,"sample range %0d",request_index);
    pending<=1;delay<=tick%4;address<=request_index;
   end
   if(pending)begin
    if(delay!=0)delay<=delay-1;
    else begin
     pending<=0;response_valid<=1;
     {ch1_left,ch0_left}<=wave[address];{ch1_right,ch0_right}<=wave[address+1];
    end
   end
   if(response_valid && response_ready)response_valid<=0;
  end
 end
 integer cycles,i,fd,r,op,a0,a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12;reg [319:0] received;reg [4095:0] file_name;
 task wait_done;
 begin cycles=0;while(!done)begin @(negedge clk);cycles=cycles+1;if(cycles>50000000)$fatal(1,"run timeout");end end
 endtask
 initial begin
  if(!$value$plusargs("wave=%s",file_name))$fatal(1,"missing +wave");
  $readmemh(file_name,wave);
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"missing +input");
  fd=$fopen(file_name,"r");
  repeat(4)@(negedge clk);reset=0;
  #1;cycles=0;while(!initialized)begin @(negedge clk);cycles=cycles+1;if(cycles>100000)$fatal(1,"init timeout");end
  while(!$feof(fd))begin
   r=$fscanf(fd,"%s",op);
   if(r==1 && op=="s")begin
    r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d %d %d %d %d",a0,a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12);
    first_index=a0;record_samples=a1;pre_samples=a2;min_separation=a3;channel=a4;falling=a5;level=a6;hysteresis=a7;
    bin_count=a8;first_bin=a9;factor=a10;initial_hits=a11;channel_mask=a12;
    start=1;#1;if(!start_ready)$fatal(1,"start not ready");@(negedge clk);start=0;
    // Configuration is latched at acceptance; scrambling it must not matter
    // for the accumulator geometry (the scanner holds its own copy).
    bin_count=0;first_bin=32'hffffffff;factor=0;channel_mask=0;initial_hits=32'hffffffff;
    wait_done;
    $display("D %0d %0d %0d %0d",accepted_hits,crossings,invalid,rejected);
    @(negedge clk);
   end else if(r==1 && op=="q")begin
    r=$fscanf(fd,"%d %d %d %d %d",a0,a1,a2,a3,a4);
    qualify_enable=a0;qualify_count=a1;qualify_stride=a2;qualify_pre=a3;qualify_threshold=a4;
   end else if(r==1 && op=="t")begin
    r=$fscanf(fd,"%d",a0);
    for(i=0;i<a0;i=i+1)begin r=$fscanf(fd,"%d",a1);template[i]=a1;end
   end else if(r==1 && op=="p")begin
    r=$fscanf(fd,"%d",a0);first_index=a0;
    peek=1;#1;if(!start_ready)$fatal(1,"peek not ready");@(negedge clk);peek=0;
    wait_done;$display("P %08x %0d",peek_data,invalid);@(negedge clk);
   end else if(r==1 && op=="r")begin
    r=$fscanf(fd,"%d %d",a0,a1);command_bin=a0;command_channel=a1;command_write=0;
    command_valid=1;#1;cycles=0;
    while(!command_ready)begin @(negedge clk);cycles=cycles+1;if(cycles>700)$fatal(1,"host ready timeout");end
    @(negedge clk);command_valid=0;received=0;i=0;cycles=0;
    while(!completion_valid)begin
     if(download_valid)begin received[16*i+:16]=download_data;i=i+1;download_ready=1;end
     @(negedge clk);download_ready=0;cycles=cycles+1;if(cycles>700)$fatal(1,"completion timeout");
    end
    if(completion_error || i!=20)$fatal(1,"download result");
    completion_ready=1;@(negedge clk);completion_ready=0;
    $display("S %080x",received);
   end
  end
  $display("PASS edge accumulator");$finish;
 end
endmodule
