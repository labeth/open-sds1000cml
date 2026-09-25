// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_peaks;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,valid=0,last=0,score_defined=0,score_invalid=0;
 reg verdict_valid=0,verdict_accept=0;
 reg [31:0] first_position=0,min_separation=0;
 reg signed [49:0] threshold=0,score=0;
 wire ready,candidate_valid,busy,done,invalid,left_present,right_present;
 wire [31:0] candidate_position;
 wire signed [49:0] candidate_score,left_score,right_score;
 stack_peaks dut(.*);
 reg signed [49:0] scores[0:4095];
 integer defs[0:4095],bads[0:4095],accepts[0:4095],delays[0:4095];
 integer fd,n,i,r,abort_mode,idx,waited=0,cycles=0,c;
 reg [4095:0] file_name;
 reg [183:0] held;
 reg holding=0;
 initial begin
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"input missing");
  fd=$fopen(file_name,"r");
  r=$fscanf(fd,"%d %d %d %d %d\n",first_position,min_separation,threshold,n,abort_mode);
  if(r!=5 || n<1 || n>4096)$fatal(1,"header");
  for(i=0;i<n;i=i+1)begin
   r=$fscanf(fd,"%d %d %d %d %d\n",scores[i],defs[i],bads[i],accepts[i],delays[i]);
   if(r!=5)$fatal(1,"row");
  end
  $fclose(fd);
  repeat(3)@(negedge clk);reset=0;start=1;
  @(negedge clk);start=0;i=0;
  while(!done)begin
   valid=0;verdict_valid=0;
   if(candidate_valid)begin
    if(ready)$fatal(1,"accepted input while awaiting segment verdict");
    if(holding && held!=={candidate_position,candidate_score,left_score,right_score,left_present,right_present})
     $fatal(1,"unstable candidate");
    held={candidate_position,candidate_score,left_score,right_score,left_present,right_present};holding=1;
    if(abort_mode!=0)begin
     if(abort_mode==1)reset=1;else start=1;
     @(negedge clk);
     if(candidate_valid || done)$fatal(1,"cancel did not suppress candidate");
     reset=0;start=1;
     @(negedge clk);start=0;
     abort_mode=0;i=0;holding=0;waited=0;
    end else begin
     idx=candidate_position-first_position;
     if(waited>=delays[idx])begin
      $display("H %0d %0d %0d %0d %0d %0d %0d",candidate_position,candidate_score,left_present ? left_score:0,right_present ? right_score:0,left_present,right_present,accepts[idx]);
      verdict_valid=1;verdict_accept=accepts[idx];holding=0;waited=0;
     end else waited=waited+1;
    end
   end else if(ready && i<n && cycles%4!=1)begin
    score=scores[i];score_defined=defs[i];score_invalid=bads[i];last=i==n-1;valid=1;i=i+1;
   end
   @(negedge clk);cycles=cycles+1;
   if(cycles>100000)$fatal(1,"timeout");
  end
  valid=0;verdict_valid=0;
  if(busy)$fatal(1,"busy on completion");
  $display("D %0d",invalid);
  repeat(5)begin @(negedge clk);if(done || candidate_valid)$fatal(1,"late result");end
  $finish(0);
 end
endmodule
