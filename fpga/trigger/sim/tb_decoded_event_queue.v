// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013
module tb_decoded_event_queue #(parameter COMPACT=0);
 reg clk=0;always #5 clk=~clk;
 reg reset=1,iv=0,ready=0;
 reg [31:0] epoch=7,value=0;
 reg [63:0] sample=0;
 wire valid,overflow;wire [255:0] data;
 localparam [31:0] WIDE_VALUE=COMPACT ? 255 : 32'hfedcba98;
 localparam [31:0] NORMAL_COUNT=COMPACT ? 127 : 32'h87654321;
 decoded_event_queue #(.AW(1),.VALUE_BITS(COMPACT?8:32),.COUNT_BITS(COMPACT?7:32)) dut(clk,reset,epoch,iv,sample,8'd2,8'd8,8'd5,32'd0,value,NORMAL_COUNT,valid,ready,data,overflow);
// TRLC-LINKS: REQ-SDS-013
 task step;begin @(posedge clk);#1;end endtask
// TRLC-LINKS: REQ-SDS-013
 task send(input [31:0] v);begin @(negedge clk);iv=1;value=v;sample=64'h100000000+v;step;end endtask
// TRLC-LINKS: REQ-SDS-013
 task take(input [7:0] kind,input [31:0] seq,input [31:0] val,input [31:0] count);begin
  @(negedge clk);
  if(!valid || data[15:8]!=kind || data[95:64]!=seq || data[223:192]!=val || data[255:224]!=(kind==5?count:NORMAL_COUNT) || data[63:32]!=epoch)
   $fatal(1,"unexpected event %h",data);
  ready=1;step;
 end endtask
 initial begin
  step;@(negedge clk);reset=0;
  send(WIDE_VALUE);send(2); // queue full, including the widest producer value
  send(3);send(4); // these become one loss marker
  @(negedge clk);iv=0;
  if(COMPACT)dut.lost=32'hfabcdeff; // full-width loss must survive compact payload
  if(!overflow)$fatal(1,"overflow was hidden");
  take(2,0,WIDE_VALUE,0);take(2,1,2,0);take(5,2,0,COMPACT?32'hfabcdeff:2);
  if(valid)$fatal(1,"queue should be empty");
  // full pop/push sustains traffic without reporting loss
  @(negedge clk);ready=0;
  send(5);send(6);
  @(negedge clk);ready=1;iv=1;value=7;step;
  iv=0;
  take(2,4,6,0);take(2,5,7,0);
  // reset flushes queued data and pending loss and restarts sequence in new epoch
  @(negedge clk);ready=0;send(8);send(9);send(10);
  @(negedge clk);iv=0;reset=1;epoch=8;step;
  if(valid || overflow)$fatal(1,"epoch reset retained stale state");
  @(negedge clk);reset=0;send(11);@(negedge clk);iv=0;
  take(2,0,11,0);
  // A new event arriving while an older loss marker is inserted must itself
  // be accounted for, not overwrite the marker or silently disappear.
  @(negedge clk);ready=0;reset=1;step;
  @(negedge clk);reset=0;
  send(20);send(21);send(22);
  @(negedge clk);ready=1;value=23;sample=64'h100000017;step;
  iv=0;
  take(2,1,21,0);
  take(5,2,0,1);
  take(5,3,0,1);
  if(valid)$fatal(1,"unexpected data after loss markers");
  $display("PASS decoded events: wide data, FIFO order, loss, full exchange, epoch reset");$finish;
 end
 initial begin #10000;$fatal(1,"timeout");end
endmodule
