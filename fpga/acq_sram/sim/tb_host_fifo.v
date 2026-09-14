`timescale 1ns/1ps
module tb;
 parameter PHASE=1,SYNC_STALL=0;
 reg source_clk=0,dest_clk=0;always #2 source_clk=~source_clk;
 initial begin #PHASE;forever #4 dest_clk=~dest_clk;end
 reg reset=1,push=0,dest_ready=0;
 reg [79:0] source_data=0;wire source_ready,overflow,dest_valid;wire [79:0] dest_data;
 sram_host_fifo dut(.*);
 reg [79:0] expected[0:20000];
 integer written=0,read_count=0,max_pending=0,cycle=0,sent=0,dcycles=0,mode=0;
 // Verification-only delayed first-stage observations; pointers may be
 // observed a clock later without corrupting their Gray value.
 reg [3:0] delayed_write=0,delayed_read=0;
 integer scycles=0;
 always @(negedge source_clk)scycles=scycles+1;
 initial if(SYNC_STALL)forever begin
  @(negedge dest_clk);
  if(!reset && dcycles%13==0)begin
   delayed_write=dut.wgray_meta;force dut.wgray_meta=delayed_write;
   @(negedge dest_clk);release dut.wgray_meta;
  end
 end
 initial if(SYNC_STALL)forever begin
  @(negedge source_clk);
  if(!reset && scycles%11==0)begin
   delayed_read=dut.rgray_meta;force dut.rgray_meta=delayed_read;
   @(negedge source_clk);release dut.rgray_meta;
  end
 end
 reg held=0;reg [79:0] held_data=0;
 function [79:0] pattern(input integer value);
  pattern={16'(value^16'hcafe),32'(value*32'h45d9f3b),32'(~value)};
 endfunction
 always @(posedge source_clk)if(!reset && push && source_ready)begin
  expected[written]=source_data;written=written+1;
  if(written-read_count>max_pending)max_pending=written-read_count;
  if(written-read_count>9)$fatal(1,"FIFO exceeded slots plus output register");
 end
 always @(posedge dest_clk)begin
  if(reset)held=0;
  else begin
   if(held && (!dest_valid || dest_data!==held_data))$fatal(1,"changed blocked output");
   held=dest_valid && !dest_ready;held_data=dest_data;
   if(dest_valid && dest_ready)begin
    if(read_count>=written || dest_data!==expected[read_count])$fatal(1,"FIFO order/data got %h at %0d",dest_data,read_count);
    read_count=read_count+1;
   end
  end
 end
 always @(negedge dest_clk)begin
  dcycles=dcycles+1;
  dest_ready=!reset && (mode==1 || (mode==2 && dcycles%7<4));
 end
 task new_epoch;
  begin
   @(negedge source_clk);#0.1;reset=1;push=0;mode=0;
   repeat(6)@(negedge source_clk);
   written=0;read_count=0;max_pending=0;sent=0;
   reset=0;repeat(10)@(negedge source_clk);
   if(overflow || dest_valid || !source_ready)$fatal(1,"reset did not discard epoch");
  end
 endtask
 task drain;
  begin
   @(negedge source_clk);push=0;mode=1;
   wait(read_count==written);repeat(8)@(negedge dest_clk);
   if(dest_valid)$fatal(1,"phantom data after drain");
  end
 endtask
 initial begin
  new_epoch;mode=1;
  // One maximum-size two-bank burst: 2560 data pairs plus two metadata offers.
  for(cycle=0;cycle<5120;cycle=cycle+1)begin
   @(negedge source_clk);
   push=cycle%2==0 || cycle==2561 || cycle==5119;
   if(push)begin
    if(!source_ready)$fatal(1,"insufficient FIFO throughput for packed burst");
    source_data=pattern(sent);sent=sent+1;
   end
  end
  drain;
  if(overflow || written!=2562)$fatal(1,"packed burst accounting");
  $display("packed burst phase=%0d words=%0d max_pending=%0d",PHASE,written,max_pending);
  new_epoch;mode=2;
  for(cycle=0;cycle<10000;cycle=cycle+1)begin
   @(negedge source_clk);push=source_ready && cycle%5!=0;
   if(push)begin source_data=pattern(sent+10000);sent=sent+1;end
  end
  drain;
  if(overflow || written<1000)$fatal(1,"throttled stream accounting");
  new_epoch;
  for(cycle=0;cycle<40;cycle=cycle+1)begin
   @(negedge source_clk);push=source_ready;
   if(push)begin source_data=pattern(sent+20000);sent=sent+1;end
  end
  @(negedge source_clk);push=0;
  repeat(8)@(negedge source_clk);
  if(source_ready || written!=9)$fatal(1,"full capacity expected nine got %0d",written);
  push=1;source_data=80'hbad;
  @(negedge source_clk);push=0;
  if(!overflow || written!=9)$fatal(1,"overflow was not rejected");
  drain;
  if(!overflow)$fatal(1,"overflow not sticky");
  new_epoch;
  // Reset with data in storage/output, then distinguish the replacement epoch.
  repeat(6)begin
   @(negedge source_clk);push=1;source_data=pattern(sent+30000);sent=sent+1;
   @(negedge source_clk);push=0;
  end
  if(written==0)$fatal(1,"empty reset test");
  new_epoch;mode=1;
  repeat(64)begin
   @(negedge source_clk);push=1;source_data=pattern(sent+40000);sent=sent+1;
   @(negedge source_clk);push=0;
  end
  drain;
  if(overflow || written!=64)$fatal(1,"replacement epoch accounting");
  $display("PASS host FIFO phase=%0d sync_stall=%0d: packed throughput, wraps, stalls, overflow, common reset",PHASE,SYNC_STALL);$finish;
 end
 initial begin #2000000;$fatal(1,"timeout written=%0d read=%0d",written,read_count);end
endmodule
