// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_resample;
 reg [31:0] first_bin=0;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,ready=0,odd=0;
 reg [31:0] hit_position=0,bin_count=0,factor=0,record_samples=0;
 reg signed [24:0] delta=0;reg [1:0] channel_mask=0;
 wire request_valid,response_ready,valid,result_odd,busy,done,invalid;
 wire [31:0] request_index,bin;wire [36:0] ch0_value,ch1_value;wire [1:0] result_mask;
 reg response_valid=0,response_error=0;
 reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 reg mem_pending=0,poison=0;reg [31:0] saved_index=0;
 integer cycles=0,delay=0,read_count=0,error_enabled=0,abort_kind=0,aborted=0;
 wire request_ready=!mem_pending && cycles%4!=1;
 stack_resample dut(.*);
 reg [36:0] sig0[0:255],sig1[0:255];
 always @(posedge clk)begin
  if(reset)begin mem_pending<=0;response_valid<=0;poison<=0;end
  else begin
   if(start && mem_pending)poison<=1;
   if(request_valid && request_ready)begin
    if(mem_pending || request_index+1>=record_samples)$fatal(1,"illegal memory request");
    saved_index<=request_index;mem_pending<=1;poison<=0;delay<=2+read_count%5;read_count=read_count+1;
   end
   if(mem_pending && !response_valid)begin
    if(delay==0)begin
     response_valid<=1;response_error<=poison || error_enabled!=0;
     ch0_left<=sig0[saved_index];ch0_right<=sig0[saved_index+1];
     ch1_left<=sig1[saved_index];ch1_right<=sig1[saved_index+1];
    end else delay<=delay-1;
   end
   if(response_valid && response_ready)begin mem_pending<=0;response_valid<=0;end
  end
 end
 reg [4095:0] file_name;integer fd,r,k,hold_count=0;
 reg holding=0;reg [108:0] held;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  fd=$fopen(file_name,"r");
  r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d\n",hit_position,bin_count,factor,record_samples,delta,channel_mask,odd,abort_kind,error_enabled);
  if(r!=9 || record_samples>256)$fatal(1,"header");
  for(k=0;k<record_samples;k=k+1)begin r=$fscanf(fd,"%d %d\n",sig0[k],sig1[k]);if(r!=2)$fatal(1,"sample");end
  $fclose(fd);repeat(3)@(negedge clk);reset=0;start=1;@(negedge clk);start=0;
  while(!done || (abort_kind==4 && !aborted))begin
   ready=cycles%4==0;
   if(!aborted && abort_kind!=0 &&
    ((abort_kind==1 && mem_pending && delay>0) ||
     (abort_kind==2 && response_valid && response_ready) ||
     (abort_kind==3 && valid) || (abort_kind==4 && done)))begin
    if(abort_kind==2)response_error=1;
    if(abort_kind==4 && !invalid)$fatal(1,"expected initial memory error");
    if(abort_kind==4)error_enabled=0;
    ready=0;start=1;@(negedge clk);start=0;
    $display("R");aborted=1;read_count=0;holding=0;
   end else if(valid)begin
    if(holding && held!=={bin,ch0_value,ch1_value,result_mask,result_odd})$fatal(1,"unstable output");
    held={bin,ch0_value,ch1_value,result_mask,result_odd};holding=!ready;
    if(ready)$display("B %0d %0d %0d %0d %0d",bin,ch0_value,ch1_value,result_mask,result_odd);
   end
   @(negedge clk);cycles=cycles+1;if(cycles>1000000)$fatal(1,"timeout");
  end
  if(busy)$fatal(1,"busy result");
  if(abort_kind!=0 && !aborted)$fatal(1,"restart not exercised");
  $display("D %0d %0d %0d",invalid,read_count,cycles);
  repeat(5)begin @(negedge clk);if(done || valid || request_valid)$fatal(1,"late result");end
  $finish(0);
 end
endmodule
