`timescale 1ns/1ps
module tb;
 parameter SOURCE_HALF=4,DEST_HALF=2,PHASE=0;
 reg source_clk=0,dest_clk=0;
 always #(SOURCE_HALF)source_clk=~source_clk;
 initial begin #(PHASE);forever #(DEST_HALF)dest_clk=~dest_clk;end
 reg reset=1,source_valid=0,dest_ready=0;
 reg [35:0] source_data=0;
 wire source_ready,dest_valid;wire [35:0] dest_data;
 sram_word_bridge #(.WIDTH(36)) dut(.*);
 integer sent=0,received=0,total=0,i,epoch=0;
 reg stalled=0;reg [35:0] previous;
 realtime began,elapsed;
 function [35:0] pattern(input integer n,input integer e);
  pattern={4'(n^e),32'((n*32'h9e3779b9)^(e*32'h85ebca6b))};
 endfunction
 always @(negedge source_clk)source_data=pattern(sent,epoch);
 always @(posedge source_clk)if(!reset && source_valid && source_ready)sent=sent+1;
 always @(posedge dest_clk)begin
  if(reset)stalled=0;
  else begin
   if(stalled && (!dest_valid || dest_data!==previous))$fatal(1,"stalled payload changed");
   if(dest_valid && dest_ready)begin
    if(dest_data!==pattern(received,epoch))$fatal(1,"word order/epoch got=%h want=%h",dest_data,pattern(received,epoch));
    received=received+1;total=total+1;
   end
   stalled=dest_valid && !dest_ready;previous=dest_data;
  end
 end
 task new_epoch;
 begin
  // Exercise unpublished payload capture with valid high during reset.
  // Drop valid before release; no old word may appear in the next epoch.
  reset=1;source_valid=1;dest_ready=0;#100;source_valid=0;
  sent=0;received=0;epoch=epoch+1;reset=0;#100;
 end endtask
 initial begin
  new_epoch;source_valid=1;dest_ready=1;began=$realtime;
  wait(received==2000);elapsed=$realtime-began;
  if(SOURCE_HALF==4 && DEST_HALF==2 && elapsed>81000)$fatal(1,"insufficient catch-up service rate %0f ns",elapsed);
  @(negedge source_clk);source_valid=0;wait(received==sent);
  $display("throughput source_half=%0d dest_half=%0d phase=%0d ns_per_word=%0f",SOURCE_HALF,DEST_HALF,PHASE,elapsed/2000);
  // Stop the sink with an outstanding word, then resume. Source backpressure
  // must retain the mailbox; no overwrite is possible while request is pending.
  dest_ready=0;source_valid=1;#200;source_valid=0;
  #1000;dest_ready=1;wait(received==sent);#100;
  // Reset with an unread destination word and with a request still crossing.
  for(i=0;i<8;i=i+1)begin
   new_epoch;source_valid=1;#(i*3+1);new_epoch;
   source_valid=1;dest_ready=1;wait(received==20);
   @(negedge source_clk);source_valid=0;wait(received==sent);
  end
  $display("PASS word bridge delivered=%0d reset/stall epochs checked",total);$finish;
 end
 initial begin #1000000;$fatal(1,"timeout");end
endmodule
