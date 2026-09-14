`timescale 1ns/1ps
module tb;
 parameter BANKS=9,ROWS=512;
 localparam DEPTH=BANKS*ROWS;
 reg clk=0;always #2 clk=~clk;
 reg reset=1,push=0,ready=0;reg [31:0] data_in=0;
 wire valid,overflow,underflow;wire [31:0] data_out;
 wire [$clog2(DEPTH+9)-1:0] count;
 sram_ingress_stream #(.BANKS(BANKS),.ROWS(ROWS)) dut(.*);
 integer written=0,read=0,total_read=0,i,seed=32'h55621831;
 reg stalled=0;reg [31:0] previous;
 always @(posedge clk)begin
  if(!reset)begin
   if(stalled && (!valid || data_out!==previous))$fatal(1,"changed stalled output");
   if(push)written=written+1;
   if(valid && ready)begin
    if(data_out!==read)$fatal(1,"stream sequence got=%0d want=%0d",data_out,read);
    read=read+1;total_read=total_read+1;
   end
   stalled=valid && !ready;previous=data_out;
  end else begin written=0;read=0;stalled=0;end
  #0.5;
  if(count!=written-read)$fatal(1,"stream occupancy got=%0d want=%0d",count,written-read);
  if(overflow || underflow || dut.held>8 || dut.reserved>8)$fatal(1,"unexpected stream fault");
 end
 task cycle(input wr,input rd);
 begin @(negedge clk);push=wr;ready=rd;data_in=written;@(posedge clk);#1;end
 endtask
 initial begin
  cycle(0,0);reset=0;
  // Fill nearly to capacity, then require full-rate output after prefetch.
  for(i=0;i<DEPTH-1;i=i+1)cycle(1,0);
  repeat(8)cycle(0,0);
  for(i=0;i<DEPTH*4;i=i+1)begin cycle(1,1);if(!valid)$fatal(1,"throughput bubble");end
  for(i=0;i<DEPTH*8;i=i+1)cycle((written-read<DEPTH-4) && (($random(seed)&3)!=0),($random(seed)&3)!=0);
  while(written!=read)cycle(0,1);
  // Empty/near-empty and abrupt stalls during pending synchronous reads.
  for(i=0;i<DEPTH*4;i=i+1)cycle(($random(seed)&7)==0,($random(seed)&1)!=0);
  while(written!=read)cycle(0,1);
  cycle(0,0);
  // Reset at each phase of a pending RAM response; no stale word may leak.
  for(i=0;i<6;i=i+1)begin
   cycle(1,0);repeat(i)cycle(0,0);
   reset=1;cycle(0,0);reset=0;
   cycle(1,1);repeat(8)cycle(0,1);
  end
  $display("PASS ingress stream depth=%0d words=%0d full-rate and stalled output",DEPTH,total_read);$finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule
