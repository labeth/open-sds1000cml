// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-042, REQ-SDS-057, REQ-SDS-058
module tb_finite_writer_faults;
 localparam AW=13;
 reg clk=0;always #2 clk=~clk;
 reg reset=1,start=0,capture_allowed=1,halt=0,source_valid=0,source_fault=0,trigger=0;
 reg [AW:0] pre_count=0,post_count=1;
 reg [31:0] source_data=32'h53ad2e71;
 wire start_ready,active,source_enable,source_ready,frozen,fault,request_error,triggered;
 wire [AW-1:0] record_start;wire [AW:0] record_words,trigger_index;
 wire writer_request,writer_command,writer_valid,writer_stop;wire [31:0] writer_data;
 reg writer_ready=1,writer_write_ready=0,drop_ready=0;
 reg [AW-1:0] position=0;
 integer writes=0;
 sram_finite_writer #(.AW(AW),.PRIME_WORDS(2)) dut(.*);
 // Deliberately simple transport handshake, for fault injection only.
 // The integration bench separately exercises the real transport and pins.
 always @(posedge clk)begin
  if(reset)begin writer_ready<=1;writer_write_ready<=0;position<=0;writes<=0;end
  else begin
   if(writer_command && writer_ready)begin writer_ready<=0;writer_write_ready<=1;end
   else if(writer_stop)begin writer_ready<=1;writer_write_ready<=0;end
   if(drop_ready)writer_write_ready<=0;
   if(writer_valid && writer_write_ready)begin position<=position+1'b1;writes<=writes+1;end
  end
 end
 task tick;begin @(negedge clk);#0.1;end endtask
 task new_epoch;
 begin reset=1;source_valid=0;source_fault=0;trigger=0;halt=0;drop_ready=0;tick;reset=0;end endtask
 task require_invalid;
 begin wait(!active);tick;
  if(!fault || frozen || start_ready)$fatal(1,"frontend fault left a valid epoch: fault=%b frozen=%b ready=%b writes=%0d",fault,frozen,start_ready,writes);
 end endtask
 task launch;
 begin wait(start_ready);tick;start=1;tick;start=0;end endtask
 initial begin
  repeat(3)tick;reset=0;
  launch;halt=1;source_valid=1;tick;halt=0;
  wait(!active);tick;
  if(!frozen || fault || record_words!=0 || writes!=2)$fatal(1,"early halt accepted an ADC word");
  // HALT while idle must preserve the previous frozen metadata.
  halt=1;repeat(3)tick;halt=0;
  if(!frozen || record_words!=0)$fatal(1,"idle halt changed frozen record");
  source_valid=0;launch;wait(source_enable);tick;
  drop_ready=1;tick;drop_ready=0;source_valid=1;tick;source_valid=0;
  wait(!active);tick;
  if(!fault || frozen || start_ready)$fatal(1,"lost word did not invalidate epoch");
  start=1;tick;start=0;if(!request_error)$fatal(1,"faulted rearm accepted");
  reset=1;tick;reset=0;launch;wait(source_enable);tick;
  trigger=1;source_valid=1;tick;source_valid=0;trigger=0;
  wait(!active);tick;
  if(fault || !frozen || record_words!=1 || !triggered || writes!=3)
   $fatal(1,"reset recovery or single-word trigger");
  // A late frontend report must invalidate even an already frozen capture.
  source_fault=1;tick;source_fault=0;require_invalid;
  // Failed startup: no ADC or priming word may be accepted on the fault edge.
  new_epoch;launch;source_fault=1;source_valid=1;tick;source_fault=0;source_valid=0;
  require_invalid;if(writes!=0)$fatal(1,"startup fault wrote SRAM");
  // Fault on a candidate trigger word suppresses its write and trigger.
  new_epoch;launch;wait(source_enable);tick;
  source_valid=1;trigger=1;source_fault=1;tick;
  source_valid=0;trigger=0;source_fault=0;require_invalid;
  if(writes!=2 || triggered)$fatal(1,"faulted trigger word accepted");
  // Coincident final-ready and fault must not publish a valid frozen record.
  new_epoch;launch;wait(source_enable);tick;
  source_valid=1;trigger=1;tick;source_valid=0;trigger=0;
  wait(dut.state==9 && writer_ready);source_fault=1;@(posedge clk);tick;source_fault=0;require_invalid;
  // An asserted source fault also rejects a start from a clean idle epoch.
  new_epoch;source_fault=1;start=1;tick;start=0;
  if(!request_error || active || writer_request)$fatal(1,"faulted source start accepted");
  // The new launch stage owns the writer immediately and rejects a second start.
  new_epoch;launch;
  if(!active || start_ready || !dut.launch_pending)$fatal(1,"pending launch did not reserve writer");
  start=1;post_count=2;tick;start=0;post_count=1;
  if(!request_error)$fatal(1,"second start during pending launch accepted");
  wait(source_enable);tick;source_valid=1;trigger=1;tick;source_valid=0;trigger=0;
  wait(!active);tick;
  if(!frozen || fault || record_words!=1)$fatal(1,"pending launch lost accepted geometry");
  // Reset must cancel a pending start without issuing a command or data write.
  new_epoch;launch;reset=1;tick;reset=0;repeat(3)tick;
  if(active || writer_request || writes!=0 || frozen || fault)$fatal(1,"reset failed to cancel launch");
  // A halt coincident with acceptance is retained through startup.
  new_epoch;wait(start_ready);tick;start=1;halt=1;tick;start=0;halt=0;source_valid=1;
  wait(!active);tick;source_valid=0;
  if(!frozen || fault || record_words!=0 || writes!=2)$fatal(1,"coincident start/halt captured data");
  // Rearming a frozen epoch: rejection preserves it; acceptance masks it on
  // the first edge even when transport cannot yet grant the new operation.
  capture_allowed=0;start=1;tick;start=0;
  if(!request_error || !frozen || active || writer_request)
   $fatal(1,"rejected rearm changed frozen ownership");
  capture_allowed=1;tick;force writer_ready=1'b0;
  start=1;tick;start=0;
  if(frozen || !active || !writer_request || request_error)
   $fatal(1,"accepted rearm did not invalidate frozen record immediately");
  repeat(3)begin tick;if(frozen || !active)$fatal(1,"frozen record resurfaced during delayed grant");end
  release writer_ready;new_epoch;tick;
  $display("PASS frozen rearm: rejected start preserves record; accepted start invalidates immediately across delayed grant");
  $display("PASS pending launch: immediate reservation, repeated-start rejection, held geometry, reset and coincident halt");
  $display("PASS frontend fault: startup, trigger word, final drain, frozen record, idle start rejection");
  $display("PASS finite writer: pending/idle halt, lost readiness invalidates epoch, rearm rejected, reset recovery");
  $finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule
