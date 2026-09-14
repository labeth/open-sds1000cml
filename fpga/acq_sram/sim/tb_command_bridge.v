`timescale 1ns/1ps
module tb_command_bridge;
 parameter PHASE=0,SHORT_RESET=0;
 reg host_clk=0,core_clk=0,reset=1,host_send=0;
 reg [191:0] host_payload=0;
 wire host_busy,host_rejected,core_valid;
 wire [191:0] core_payload;
 always #5 host_clk=~host_clk;
 reg core_run=1;
 initial begin #(PHASE*0.3);forever begin #2;if(core_run)core_clk=~core_clk;end end
 acq_command_bridge dut(.*);
 reg [191:0] expected[0:8191];
 integer sent=0,received=0,rejected=0,epochs=0,j,k;
 reg expected_reject=0;
 always @(posedge host_clk)begin
  if(reset)expected_reject=0;
  else begin
   expected_reject=host_send && host_busy && !dut.host_reset[1];
   if(host_send && !host_busy)begin
    expected[sent]=host_payload;sent=sent+1;
   end
   #0.01;
   if(host_rejected!==expected_reject)$fatal(1,"rejection timing");
   if(host_rejected)rejected=rejected+1;
  end
 end
 always @(posedge core_clk)if(!reset && core_valid)begin
  if(received>=sent)$fatal(1,"duplicate or unsolicited command");
  if(core_payload!==expected[received])$fatal(1,"torn/reordered command %0d",received);
  received=received+1;
 end
 task drain;
  begin
   @(negedge host_clk);host_send=0;
   wait(!host_busy);repeat(8)@(negedge host_clk);
   if(received!=sent)$fatal(1,"lost command");
  end
 endtask
 task clear_epoch;
  begin
   reset=1;host_send=0;#(SHORT_RESET ? 1 : 40);
   sent=0;received=0;
   @(negedge host_clk);reset=0;
   wait(!host_busy);epochs=epochs+1;
  end
 endtask
 initial begin
  clear_epoch;
  for(k=0;k<4;k=k+1)begin
   // Change the entire bus even while a command is in flight; only accepted
   // snapshots may appear. Include sustained requests and randomized gaps.
   for(j=0;j<1000;j=j+1)begin
    @(negedge host_clk);host_send=k==0 || ($urandom%4!=0);
    host_payload={$urandom,$urandom,$urandom,$urandom,$urandom,$urandom};
   end
   drain;
   if(sent<100)$fatal(1,"insufficient delivered traffic");
   // Abort at different points before/after request synchronization.
   @(negedge host_clk);host_send=1;host_payload=192'habc;
   @(posedge host_clk);#(0.2+k*2);clear_epoch;
   repeat(8)@(negedge host_clk);
   if(core_valid || host_busy || received!=0)$fatal(1,"stale command after reset");
  end
  if(rejected<100)$fatal(1,"busy rejection not exercised");
  @(negedge core_clk);core_run=0;clear_epoch;
  if(!dut.core_reset[1] || core_valid)$fatal(1,"stopped core did not hold reset");
  @(negedge host_clk);host_send=1;host_payload=192'h87654321;
  @(negedge host_clk);host_send=0;repeat(8)@(negedge host_clk);
  if(!host_busy || received!=0)$fatal(1,"stopped core acknowledged command");
  core_run=1;drain;
  if(received!=1)$fatal(1,"resumed core lost command");
  $display("PASS command bridge phase=%0d short_reset=%0d: coherent payloads, busy rejection, reset aborts, stopped-clock recovery, epochs=%0d",PHASE,SHORT_RESET,epochs);
  $finish;
 end
 initial begin #1000000;$fatal(1,"command bridge timeout");end
endmodule
