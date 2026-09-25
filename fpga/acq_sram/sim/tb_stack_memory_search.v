// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_memory_search;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,pair_valid=0,verdict_valid=0,verdict_accept=0;
 reg [31:0] first_position=0,window_count=0,gate_length=0,min_separation=0;
 reg signed [49:0] threshold=0;
 reg [7:0] reference_sample=0,candidate_sample=0;
 wire pair_ready=0;
 wire candidate_valid,busy,done,invalid,left_present,right_present;
 wire [31:0] candidate_position;
 wire signed [49:0] candidate_score,left_score,right_score;
 wire signed [24:0] candidate_delta;
wire request_valid,response_ready;
 wire [31:0] reference_address,candidate_address;
 reg response_valid=0,response_error=0;
 reg [7:0] reference_data=0,candidate_data=0;
 reg mem_pending=0,poison=0;
 reg [31:0] saved_ref=0,saved_candidate=0;
 integer response_delay=0,error_enabled=0,short_reference=0,short_record=0;
 wire request_ready=!mem_pending && cycles%4!=1;
 wire [31:0] record_samples=first_position+n-(short_record!=0 ? gate_length:0);
 wire [31:0] reference_samples=gate_length-(short_reference!=0 ? 1:0);
 stack_memory_search dut(.*);
 always @(posedge clk)begin
  if(reset)begin mem_pending<=0;response_valid<=0;poison<=0;end
  else begin
   if(start && mem_pending)poison<=1;
   if(request_valid && request_ready)begin
    if(mem_pending)$fatal(1,"multiple outstanding reads");
    if(reference_address!=sent%gate_length || candidate_address!=first_position+sent/gate_length+sent%gate_length)$fatal(1,"window address sequence");
    if(reference_address>=gate_length || candidate_address-first_position>=n)$fatal(1,"read outside retained data");
    saved_ref<=reference_address;saved_candidate<=candidate_address-first_position;
    mem_pending<=1;response_delay<=2+sent%7;poison<=0;sent=sent+1;
   end
   if(mem_pending && !response_valid)begin
    if(response_delay==0)begin
     response_valid<=1;response_error<=poison || error_enabled!=0;
     reference_data<=poison ? 8'hff:reference[saved_ref];
     candidate_data<=poison ? 8'h00:samples[saved_candidate];
    end else response_delay<=response_delay-1;
   end
   if(response_valid && response_ready)begin
    mem_pending<=0;response_valid<=0;
   end
  end
 end
 reg [7:0] reference[0:511],samples[0:4095];
 integer accept_hit[0:4095];
 integer fd,r,n,k,i,idx,j,cycles=0,waited=0,delay,abort_cycle,abort_kind,abort_used=0;
 integer sent=0,window_idx=0,within_window=0;
 reg [4095:0] file_name;
 reg [208:0] held;
 reg holding=0;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input");
  r=$value$plusargs("fault=%d",error_enabled);
  r=$value$plusargs("short_reference=%d",short_reference);
  r=$value$plusargs("short_record=%d",short_record);
  fd=$fopen(file_name,"r");
  r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d\n",first_position,window_count,gate_length,min_separation,threshold,n,delay,abort_cycle,abort_kind);
  if(r!=9 || gate_length>512 || n>4096)$fatal(1,"header");
  for(k=0;k<gate_length;k=k+1)begin r=$fscanf(fd,"%d\n",reference[k]);if(r!=1)$fatal(1,"reference");end
  for(k=0;k<n;k=k+1)begin r=$fscanf(fd,"%d\n",samples[k]);if(r!=1)$fatal(1,"samples");end
  for(k=0;k<window_count;k=k+1)begin r=$fscanf(fd,"%d\n",accept_hit[k]);if(r!=1)$fatal(1,"verdict");end
  $fclose(fd);
  repeat(3)@(negedge clk);reset=0;start=1;
  @(negedge clk);start=0;
  while(!done)begin
   pair_valid=0;verdict_valid=0;
   if(!abort_used && abort_kind!=0 && ((abort_cycle>=0 && cycles>=abort_cycle) || (abort_cycle==-1 && candidate_valid && waited>=3500) || (abort_cycle==-2 && mem_pending && response_delay>0) || (abort_cycle==-3 && response_valid && response_ready)))begin
    if(abort_cycle==-1 && (dut.search.state!=4 || dut.search.peaks_ready))$fatal(1,"held-score abort did not reach a blocked result");
    if(abort_cycle==-3)response_error=1;
    if(abort_kind==1)reset=1;else start=1;
    @(negedge clk);
    if(candidate_valid || pair_ready || done)$fatal(1,"abort exposed stale handshake");
    reset=0;start=1;@(negedge clk);start=0;
    $display("R");
    sent=0;window_idx=0;within_window=0;holding=0;waited=0;abort_used=1;
   end else begin
    if(candidate_valid)begin
     if(holding && held!=={candidate_position,candidate_score,left_score,right_score,left_present,right_present,candidate_delta})$fatal(1,"candidate changed while waiting");
     held={candidate_position,candidate_score,left_score,right_score,left_present,right_present,candidate_delta};holding=1;
     idx=candidate_position-first_position;
     if(waited>=delay+idx%11)begin
      $display("A %0d %0d",candidate_position,candidate_delta);
      $display("H %0d %0d %0d %0d %0d %0d %0d",candidate_position,candidate_score,left_present ? left_score:0,right_present ? right_score:0,left_present,right_present,accept_hit[idx]);
      verdict_valid=1;verdict_accept=accept_hit[idx];holding=0;waited=0;
     end else waited=waited+1;
    end
    if(pair_ready && window_idx<window_count && cycles%5!=2)begin
     reference_sample=reference[within_window];candidate_sample=samples[window_idx+within_window];pair_valid=1;
     sent=sent+1;
     if(within_window+1==gate_length)begin within_window=0;window_idx=window_idx+1;end
     else within_window=within_window+1;
    end
   end
   @(negedge clk);cycles=cycles+1;
   if(cycles>4000000)$fatal(1,"timeout");
  end
  pair_valid=0;verdict_valid=0;
  if(busy)$fatal(1,"busy result");
  if(!invalid && sent!=window_count*gate_length)$fatal(1,"sample accounting");
  if(abort_kind!=0 && !abort_used)$fatal(1,"abort not exercised");
  $display("D %0d %0d",invalid,cycles);
  repeat(8)begin @(negedge clk);if(done || candidate_valid || pair_ready)$fatal(1,"late result");end
  $finish(0);
 end
endmodule
