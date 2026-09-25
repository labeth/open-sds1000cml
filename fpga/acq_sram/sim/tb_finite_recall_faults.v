// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-043, REQ-SDS-052, REQ-SDS-058
module tb_finite_recall_faults;
 reg clk=0;always #2 clk=~clk;
 reg reset=1,start=0,frozen=1;
 reg [12:0] record_start=0,read_bias=0,position=0;
 reg [13:0] record_words=8,offset=0,length=4;
 wire start_ready,active,done,request_error,fault;wire [3:0] error_code;
 wire [63:0] recalled,first0,first1;wire [13:0] words0,words1,command_count;
 reg host_core_fault=0;reg [1:0] bank_release=0;
 wire [1:0] bank_busy,bank_done;wire word_valid,word_bank;wire [31:0] word_data;
 wire [11:0] word_index;
 reg transport_ready=1,transport_done=0,transport_read_valid=0;
 reg [31:0] transport_read_data=0;
 wire command,command_discard,command_continue,command_read;
 sram_finite_recall #(.AW(13),.READ_WARM(0)) dut(.*);
 task tick;begin @(negedge clk);#0.1;end endtask
 task clear_epoch;
 begin
  tick;reset=1;start=0;frozen=1;host_core_fault=0;bank_release=0;
  transport_ready=1;transport_done=0;transport_read_valid=0;length=4;
  repeat(3)tick;reset=0;repeat(3)tick;
  if(fault || active || bank_busy || !start_ready)$fatal(1,"reset did not recover");
 end endtask
 task launch;
 begin
  wait(start_ready);tick;start=1;tick;start=0;
  wait(command);#0.1;
  if(!command_read || command_discard || command_count!=4)$fatal(1,"wrong read request");
  tick;transport_ready=0;
 end endtask
 task samples(input integer n);
 integer k;
 begin
  for(k=0;k<n;k=k+1)begin
   transport_read_valid=1;transport_read_data=k;#0.1;
   if(!word_valid || word_index!=k || word_data!=k)$fatal(1,"word forwarding");
   tick;
  end
  transport_read_valid=0;
 end endtask
 task check_fault(input integer code);
 begin
  tick;
  if(!fault || error_code!=code || word_valid || bank_done || command)$fatal(1,"fault did not suppress output");
  transport_read_valid=0;transport_done=1;transport_ready=1;tick;transport_done=0;
  repeat(4)tick;
  if(active || done || start_ready || !fault || bank_busy==0)$fatal(1,"fault epoch was reused");
 end endtask
 initial begin
  clear_epoch;launch;samples(1);host_core_fault=1;check_fault(3);
  clear_epoch;launch;samples(1);frozen=0;check_fault(2);
  clear_epoch;launch;samples(1);transport_done=1;check_fault(1);
  clear_epoch;launch;samples(4);transport_read_valid=1;check_fault(1);
  // Recovery and successful completion require the final host release.
  clear_epoch;launch;samples(4);transport_done=1;transport_ready=1;tick;transport_done=0;
  wait(bank_done!=0);#0.1;
  if(words0!=4 || first0!=0 || recalled!=4)$fatal(1,"completion metadata");
  repeat(6)tick;
  if(done || !active || start_ready || bank_busy!=1)$fatal(1,"premature completion");
  bank_release=1;tick;bank_release=0;wait(done);#0.1;
  if(active || bank_busy!=0 || fault || !start_ready)$fatal(1,"release completion");
  $display("PASS finite recall faults: host error, lost freeze, short/extra read, reset recovery, final release");$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule
