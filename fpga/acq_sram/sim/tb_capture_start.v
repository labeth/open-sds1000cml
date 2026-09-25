// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-043, REQ-SDS-045, REQ-SDS-058
module tb_capture_start;
 localparam AW=13,N=1<<AW;
 reg core_clk=0,ram_clk=0,host_clk=0;
 always #2 core_clk=~core_clk;always #4 ram_clk=~ram_clk;always #5 host_clk=~host_clk;
 wire sample_clk;assign #0.4 sample_clk=core_clk;
 reg reset=1,start=0,finite_mode=0,stop=0,source_finished=0,frozen=1;
 reg [AW-1:0] record_start=N-73,read_bias=0;reg [AW:0] record_words=N,offset=19,length=5121;
 reg source_valid=0,source_fault=0;reg [35:0] source_data=0;
 wire source_ready,start_ready,source_enable,active,done,capture_done,fault,request_error,start_rejected,selected_finite;
 wire [3:0] error_code;wire [63:0] committed,read_ordinal;wire [AW:0] unread;
 wire [1:0] bank_busy,host_ready,host_token;
 reg host_release=0,release_bank=0,release_token=0,read_enable=0;
 reg [13:0] read_halfword=0;wire read_valid,read_error;wire [15:0] read_data;
 wire host_fault;wire [63:0] host_first0,host_first1;wire [11:0] host_words0,host_words1;
 reg transport_ready=1,transport_write_ready=0,transport_done=0,transport_read_valid=0;
 reg [31:0] transport_read_data=0;reg [AW-1:0] position=17;
 wire command,command_read,command_discard,command_continue,write_valid,write_stop;
 wire [AW:0] command_count;wire [31:0] write_data;
 sram_capture_engine #(.AW(AW)) dut(.record_words_sampled(),.*);
 task tick;begin @(negedge core_clk);#0.1;end endtask
 initial begin
  repeat(10)tick;reset=0;repeat(5)tick;
  finite_mode=1;record_start=128;read_bias=3;record_words=100;offset=5;length=10;
  wait(start_ready);tick;start=1;tick;
  if(!active || !selected_finite || done || start_ready)$fatal(1,"pending launch not reserved");
  // Change every setting immediately after acceptance and hold start high.
  // The second start is rejected; the first command must use the old settings.
  finite_mode=0;record_start=999;read_bias=0;record_words=0;offset=0;length=0;
  tick;
  if(!start_rejected || !selected_finite)$fatal(1,"pending mode changed");
  start=0;wait(command);#0.1;
  if(!command_read || !command_discard || command_count!=103)$fatal(1,"accepted address settings lost");
  tick;transport_ready=0;repeat(2)tick;transport_done=1;tick;transport_done=0;transport_ready=1;
  wait(command && !command_discard);#0.1;
  if(!command_read || command_count!=26 || done || !active || request_error || fault)$fatal(1,"accepted length settings lost");
  // Coordinated epoch reset discards this mock operation. Invalid finite
  // settings cannot reject a later streaming request.
  tick;reset=1;repeat(6)tick;reset=0;repeat(6)tick;
  if(bank_busy || active || selected_finite || fault)$fatal(1,"epoch reset failed");
  finite_mode=0;wait(start_ready);tick;start=1;tick;start=0;
  wait(command);#0.1;
  if(command_read || command_count!=0 || selected_finite || !active || request_error)$fatal(1,"stream launch failed");
  $display("PASS shared start pipeline: accepted settings retained, pending restart rejected, epoch reset and stream launch");$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule
