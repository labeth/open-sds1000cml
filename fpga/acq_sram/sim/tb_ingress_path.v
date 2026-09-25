// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-046
module tb;
 parameter PHASE=0;
 reg core=0,ram_clk=0;always #2 core=~core;
 initial begin #(PHASE);forever #4 ram_clk=~ram_clk;end
 reg reset=1,source_valid=0,ready=0;
 reg [35:0] source_data=0;
 wire source_ready,valid,fault;wire [35:0] data_out;wire [12:0] pending;
 sram_ingress_path dut(.*);
 integer cycle=0,sent=0,received=0,max_pending=0,i,epoch=0,source_div=128;
 reg generating=0,expect_fault=0;
 function [35:0] pattern(input integer n,input integer e);
  pattern={4'(n^e),32'((n*32'h9e3779b9)^(e*32'h85ebca6b))};
 endfunction
 always @(negedge core)begin
  cycle=cycle+1;
  source_valid=generating && cycle%source_div==0;
  source_data=pattern(sent,epoch);
 end
 always @(posedge core)begin
  if(!reset && !dut.core_reset[1])begin
   if(source_valid && source_ready)sent=sent+1;
   if(valid && ready)begin
    if(data_out!==pattern(received,epoch))$fatal(1,"path sequence/marker mismatch");
    received=received+1;
   end
   #0.5;
   if(!reset && !dut.core_reset[1])begin
   if(!expect_fault && fault)$fatal(1,"unexpected path fault");
   if(pending!=sent-received)$fatal(1,"pending accounting %0d vs %0d",pending,sent-received);
   if(pending>max_pending)max_pending=pending;
   end
  end
 end
 task clear;
 begin
  reset=1;generating=0;ready=0;#100;
  sent=0;received=0;max_pending=0;epoch=epoch+1;reset=0;#100;
 end endtask
 initial begin
  clear;generating=1;
  // Approximately one full SRAM counter circuit of writer unavailability.
  wait(sent==4100);ready=1;
  wait(received>=8192);generating=0;
  @(negedge core);wait(pending==0);#100;
  if(sent!=received || max_pending<4100)$fatal(1,"incomplete test");
  $display("PASS ingress path phase=%0d words=%0d max_pending=%0d",PHASE,received,max_pending);
  // Epoch reset discards a pending mailbox and buffered data without leakage.
  clear;generating=1;wait(sent==5);clear;generating=1;ready=1;
  wait(received==100);generating=0;@(negedge core);wait(pending==0);
  $display("PASS ingress path reset with buffered data");
  clear;expect_fault=1;source_div=1;generating=1;wait(fault);generating=0;
  #100;if(!fault)$fatal(1,"source overload fault did not stick");
  $display("PASS ingress path source overload detected");
  clear;source_div=128;generating=1;wait(fault);generating=0;
  if(!dut.queue.overflow)$fatal(1,"expected RAM capacity overflow");
  $display("PASS ingress path RAM overflow detected pending=%0d",pending);$finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule
