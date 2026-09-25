// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013
module tb_decoded_event_batch;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,event_valid=0,pop=0,rewind=0,clear_error=0;
 reg [255:0] event_data=0;
 wire event_ready,available,error;wire [3:0] cursor;wire [15:0] data,count;
 decoded_event_reader #(.AW(2)) dut(.*);
 integer i,j;
 // TRLC-LINKS: REQ-SDS-013
 task tick;begin @(posedge clk);#1;end endtask
 initial begin
  tick;@(negedge clk);reset=0;
  for(i=0;i<4;i=i+1)begin
   for(j=0;j<16;j=j+1)event_data[j*16+:16]=i*16+j;
   event_valid=1;tick;@(negedge clk);
  end
  event_valid=0;tick;
  if(count!=4 || event_ready)$fatal(1,"full host queue reservation");
  @(negedge clk);pop=1;tick;tick;
  @(negedge clk);pop=0;rewind=1;tick;
  if(count!=4 || cursor!=0 || data!=0)$fatal(1,"rewind changed record reservation");
  @(negedge clk);rewind=0;
  for(j=0;j<16;j=j+1)event_data[j*16+:16]=64+j;
  event_valid=1;pop=1;
  for(i=0;i<80;i=i+1)begin
   if(!available || data!==i)$fatal(1,"batch word %0d got %h",i,data);
   // Stop presenting the fifth event immediately after its single handshake.
   if(event_ready && event_valid)begin tick;@(negedge clk);event_valid=0;end
   else begin tick;@(negedge clk);end
  end
  pop=0;tick;
  if(available || count!=0 || error)$fatal(1,"batch left stale records or error");
  $display("PASS reserved batch across RAM wrap, concurrent arrival and partial rewind");$finish;
 end
 initial begin #10000;$fatal(1,"timeout");end
endmodule

// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
module tb_decoded_event_identity;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,event_valid=0,pop=0,rewind=0,clear_error=0;
 reg [255:0] event_data=0;
 wire event_ready,available,error;wire [3:0] cursor;wire [15:0] data,count;
 decoded_event_reader #(.AW(2),.COMPACT_IDENTITY(1)) dut(.*);
 integer i,j;
 // TRLC-LINKS: REQ-SDS-013
 function [255:0] payload(input [31:0] seq,input [31:0] epoch);
  payload={32'h87654321,32'hfedcba98,32'h11223344,64'h123456789abcdef0,seq,epoch,32'h07080201};
 endfunction
 // TRLC-LINKS: REQ-SDS-013
 task tick;begin @(posedge clk);#1;end endtask
 // TRLC-LINKS: REQ-SDS-013
 task send(input [31:0] seq,input [31:0] epoch);begin
  @(negedge clk);if(!event_ready)$fatal(1,"reader not ready");
  event_data=payload(seq,epoch);event_valid=1;tick;
  @(negedge clk);event_valid=0;
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task take(input [31:0] seq,input [31:0] epoch);reg [255:0] expected;begin
  expected=payload(seq,epoch);
  @(negedge clk);pop=1;
  for(j=0;j<16;j=j+1)begin
   if(!available || data!==expected[16*j+:16])$fatal(1,"identity/payload mismatch seq=%h half=%0d got=%h",seq,j,data);
   tick;@(negedge clk);
  end
  pop=0;
 end endtask
 initial begin
  tick;@(negedge clk);reset=0;
  for(i=0;i<4;i=i+1)send(32'hfffffffe+i,32'h76543210);
  if(count!=4 || event_ready)$fatal(1,"compact queue capacity");
  @(negedge clk);pop=1;repeat(5)tick;
  @(negedge clk);pop=0;rewind=1;tick;
  @(negedge clk);rewind=0;
  if(count!=4 || cursor!=0)$fatal(1,"rewind consumed identity");
  for(i=0;i<4;i=i+1)take(32'hfffffffe+i,32'h76543210);
  for(i=2;i<6;i=i+1)send(i,32'h76543210);
  for(i=2;i<6;i=i+1)take(i,32'h76543210);
  if(count || available || error)$fatal(1,"wrap/drain failed");
  send(7,32'h76543210); // Expected 6: reject instead of reconstructing over it.
  if(!error || event_ready || available)$fatal(1,"sequence corruption hidden");
  @(negedge clk);clear_error=1;tick;
  if(!error || event_ready)$fatal(1,"identity error cleared without epoch reset");
  @(negedge clk);clear_error=0;reset=1;tick;
  @(negedge clk);reset=0;
  send(15,99);take(15,99);
  send(16,100);
  if(!error || event_ready || available)$fatal(1,"epoch corruption hidden");
  $display("PASS compact identity: wide payload, sequence wrap, RAM wrap, rewind, reset and corruption rejection");$finish;
 end
 initial begin #20000;$fatal(1,"timeout");end
endmodule
