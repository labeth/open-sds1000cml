// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_search_accumulator;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0;wire start_ready,busy,done,invalid,initialized,host_busy;
 reg [31:0] first_position=0,window_count=0,gate_length=8,record_samples=48,reference_samples=8,min_separation=4;
 reg signed [49:0] threshold=50'sh1000000000000;
 reg [31:0] bin_count=8,first_bin=3,factor=3,initial_hits=2;wire [31:0] accepted_hits;
 wire request_valid,response_ready;wire [31:0] reference_address,candidate_address;
 reg response_valid=0,response_error=0;reg [7:0] reference_data=0,candidate_data=0;
 wire candidate_valid,verdict_ready;reg verdict_valid=0,verdict_accept=0;reg [1:0] verdict_channel_mask=3;
 wire [31:0] candidate_position;wire signed [49:0] candidate_score,left_score,right_score;
 wire signed [24:0] candidate_delta;wire left_present,right_present;
 wire sample_request_valid,sample_response_ready;wire [31:0] sample_index;
 reg sample_response_valid=0,sample_response_error=0;reg [36:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0;
 reg command_valid=0,command_write=0,command_channel=0;reg [31:0] command_bin=0;
 wire command_ready,upload_ready,download_valid,completion_valid,completion_error;
 reg upload_valid=0,download_ready=0,completion_ready=0;reg [15:0] upload_data=0;wire [15:0] download_data;
 integer tick=0,pending=0,read_delay=0,ra=0,ca=0,reads=0;
 integer sample_pending=0,sample_delay=0,sa=0,sample_reads=0,mode=0,reject_first=0,fault_seen=0;
 wire request_ready=!pending && !response_valid && tick%4!=0;
 wire sample_request_ready=!sample_pending && !sample_response_valid && tick%5!=0;
 reg [7:0] reference[0:511],samples[0:4095];reg [319:0] prior[0:15];
 stack_search_accumulator dut(.*);
 always @(posedge clk)begin
  tick<=tick+1;
  if(reset)begin pending<=0;response_valid<=0;reads<=0;sample_pending<=0;sample_response_valid<=0;sample_reads<=0;end
  else begin
   if(request_valid && request_ready)begin
    if(reference_address>=8 || candidate_address>=48)$fatal(1,"paired read range");
    pending<=1;read_delay<=mode==2 ? 400:tick%7+1;ra<=reference_address;ca<=candidate_address;reads<=reads+1;
   end
   if(pending)begin
    if(read_delay!=0)read_delay<=read_delay-1;
    else begin
     pending<=0;response_valid<=1;response_error<=mode==2 && dut.state==2;
     if(mode==2 && dut.state==2)fault_seen=1;
     reference_data<=reference[ra];candidate_data<=samples[ca];
    end
   end
   if(response_valid && response_ready)response_valid<=0;
   if(sample_request_valid && sample_request_ready)begin
    if(sample_index>=47)$fatal(1,"normalized pair range");
    sample_pending<=1;sample_delay<=tick%7+1;sa<=sample_index;sample_reads<=sample_reads+1;
   end
   if(sample_pending)begin
    if(sample_delay!=0)sample_delay<=sample_delay-1;
    else begin
     sample_pending<=0;sample_response_valid<=1;sample_response_error<=mode==1 && sample_reads==3;
     if(mode==1 && sample_reads==3)fault_seen=1;
     ch0_left<=samples[sa]*37'd16777216;ch0_right<=samples[sa+1]*37'd16777216;
     ch1_left<=(255-samples[sa])*37'd16777216;ch1_right<=(255-samples[sa+1])*37'd16777216;
    end
   end
   if(sample_response_valid && sample_response_ready)sample_response_valid<=0;
  end
 end
 integer i,j,r,fd,cycles,waited,reset_used=0;reg [4095:0] file_name;
 reg [319:0] payload,received;reg [206:0] held;reg holding=0;reg [31:0] held_count=0;
 task host;
 begin
  command_valid=1;#1;cycles=0;
  while(!command_ready)begin @(negedge clk);cycles=cycles+1;if(cycles>1000)$fatal(1,"host ready timeout");end
  @(negedge clk);command_valid=0;
  if(command_write)for(i=0;i<20;i=i+1)begin
   repeat(i%3)@(negedge clk);upload_valid=1;upload_data=payload[16*i+:16];
   #1;if(!upload_ready)$fatal(1,"upload blocked");@(negedge clk);upload_valid=0;
  end
  received=0;i=0;cycles=0;
  while(!completion_valid)begin
   if(start_ready)$fatal(1,"search interrupted host transfer");
   if(download_valid)begin repeat(i%3)@(negedge clk);received[16*i+:16]=download_data;i=i+1;download_ready=1;end
   @(negedge clk);download_ready=0;cycles=cycles+1;if(cycles>1000)$fatal(1,"host completion timeout");
  end
  if(completion_error || (!command_write && i!=20))$fatal(1,"host result");
  repeat(3)begin @(negedge clk);if(start_ready)$fatal(1,"search interrupted held completion");end
  completion_ready=1;@(negedge clk);completion_ready=0;
 end
 endtask
 task preload;
 begin
  while(!initialized)@(negedge clk);
  for(j=0;j<16;j=j+1)begin command_write=1;command_bin=j/2;command_channel=j%2;payload=prior[j];host();end
 end
 endtask
 task launch;
 begin
  first_position=0;window_count=41;gate_length=8;record_samples=48;reference_samples=mode==5 ? 1048577:8;
  min_separation=reject_first!=0 ? 20:4;bin_count=mode==7 ? 65:8;first_bin=3;factor=3;initial_hits=mode==3 ? 32'hffffffff:2;
  start=1;#1;if(!start_ready)$fatal(1,"search not ready");@(negedge clk);start=0;
  // All per-search geometry must be latched. Only verdict mask is per hit.
  first_position=32'hffffffff;window_count=0;gate_length=0;record_samples=0;reference_samples=0;
  min_separation=32'hffffffff;bin_count=0;first_bin=32'hffffffff;factor=0;initial_hits=0;
 end
 endtask
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");r=$value$plusargs("mode=%d",mode);r=$value$plusargs("reject=%d",reject_first);
  fd=$fopen(file_name,"r");for(j=0;j<8;j=j+1)r=$fscanf(fd,"%d\n",reference[j]);
  for(j=0;j<48;j=j+1)r=$fscanf(fd,"%d\n",samples[j]);
  for(j=0;j<16;j=j+1)r=$fscanf(fd,"%h\n",prior[j]);$fclose(fd);
  if(mode==6)prior[0][206:175]=32'hffffffff;
  repeat(3)@(negedge clk);reset=0;preload();launch();cycles=0;waited=0;
  while(!done)begin
   command_valid=1;start=cycles%97==5;verdict_valid=0;verdict_channel_mask=0;
   #1;if(command_ready || start_ready)$fatal(1,"host or new search interrupted active search");
   if((mode==0 || mode==4) && dut.state==2 && !dut.store_done)begin
    if(held!=={candidate_position,candidate_score,left_score,right_score,candidate_delta})$fatal(1,"candidate advanced before commit");
    if(accepted_hits!=held_count)$fatal(1,"hit counted before writes completed");
   end
   if(candidate_valid)begin
    if(holding && held!=={candidate_position,candidate_score,left_score,right_score,candidate_delta})$fatal(1,"unstable candidate");
    held={candidate_position,candidate_score,left_score,right_score,candidate_delta};holding=1;
    if(waited>=29+candidate_position%7)begin
     verdict_valid=1;verdict_accept=mode!=8 && !(reject_first!=0 && candidate_position==2);
     verdict_channel_mask=candidate_position==18 ? 2:candidate_position==34 ? 1:3;
     if(!verdict_ready)$fatal(1,"verdict not ready");
     $display("H %0d %0d %0d %0d",candidate_position,candidate_delta,verdict_accept,verdict_channel_mask);
     held_count=accepted_hits;holding=0;waited=0;
    end else waited=waited+1;
   end
   @(negedge clk);cycles=cycles+1;
   if(mode==4 && !reset_used && dut.state==2 && sample_reads>=2)begin
    reset=1;command_valid=0;start=0;verdict_valid=0;@(negedge clk);reset=0;
    while(!initialized)@(negedge clk);
    command_write=0;command_bin=0;command_channel=0;host();if(received!=0)$fatal(1,"reset retained partial state");
    preload();launch();reset_used=1;holding=0;waited=0;$display("R");
   end
   if(cycles>1000000)$fatal(1,"search timeout");
  end
  start=0;command_valid=0;verdict_valid=0;
  if(busy || dut.store_busy || dut.store.access.engine_owned)$fatal(1,"early completion");
  if(mode==1 || mode==2 || mode==3 || mode==5 || mode==6 || mode==7)begin
   if(!invalid)$fatal(1,"missing fault");
   if((mode==1 || mode==2) && !fault_seen)$fatal(1,"fault not exercised");
   if(mode==3 && sample_reads!=0)$fatal(1,"hit count overflow wrote state");
   if(mode==7 && (reads!=0 || sample_reads!=0))$fatal(1,"bad tile accessed memory");
   if(mode==5 && reads!=0)$fatal(1,"bad geometry read memory");
   start=1;command_valid=1;repeat(30)begin @(negedge clk);if(start_ready || command_ready || request_valid || sample_request_valid)$fatal(1,"poisoned result exposed");end
   start=0;command_valid=0;
  end else begin
   if(invalid || (mode==4 && !reset_used))$fatal(1,"successful search invalid");
   $display("N %0d",accepted_hits);
   for(j=0;j<16;j=j+1)begin command_write=0;command_bin=j/2;command_channel=j%2;host();$display("S %h",received);end
  end
  $display("PASS search to tile mode %0d",mode);$finish;
 end
 initial begin repeat(2000000)@(negedge clk);$fatal(1,"global timeout");end
endmodule
