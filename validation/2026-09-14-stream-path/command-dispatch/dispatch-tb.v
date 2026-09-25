`timescale 1ns/1ps
// Only ADC/CIC are mocked. Acquisition control, record geometry, transport,
// ingress, host RAM and ownership are real RTL.
module adc_interleave #(parameter SYNC_ENCODE=0)(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,
 output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign word_data=lane[31:0];assign valid=enable;assign fault=enable && lane[33];
 assign snapshot=lane;assign snapshot_ack=snapshot_request;assign enc_p=0;assign enc_n=0;assign locked=1;
 always @(posedge memclk)if(enable && !consume)$fatal(1,"ADC paused during SRAM access");
endmodule
module adc_precision #(parameter SHARED_TAIL=0)(input core,packclk,clk100,enable,input [4:0] decim_log,input [31:0] raw,input raw_valid,
 output [31:0] data,output valid,fault);
 reg [19:0] count=0;
 always @(posedge core)if(!enable)count<=0;else count<=count+1'b1;
 assign data=raw^32'h3a69c75d;
 assign valid=enable && raw_valid && (count & ((1<<(decim_log-1))-1))==0;
 assign fault=0;
endmodule
module tb_acquisition_path #(parameter READY_ONLY=0,HOST_FAULT_ONLY=0);
 localparam AW=13,N=1<<AW;
 reg reset=1,locked=1,core_clk=0,ram_clk=0,host_clk=0,clk100=0;
 always #2 core_clk=~core_clk;always #4 ram_clk=~ram_clk;
 always #5 host_clk=~host_clk;always #5 clk100=~clk100;
 wire sample_clk;assign #0.4 sample_clk=core_clk;
 reg start=0,halt=0;reg [1:0] operation=0,trigger_mode=0;
 reg [AW:0] pre_count=0,post_count=1,offset=0,length=0;
 reg [AW-1:0] read_bias=0;reg [4:0] decim_log=0;reg [9:0] encode_enable=10'h3ff;
 reg trigger_channel=0,trigger_falling=0,force_trigger=0,match_trigger=0;
 reg [15:0] trigger_level=16'h0f00;
 reg [79:0] lane=0;reg snapshot_request=0;
 wire snapshot_ack;wire [79:0] snapshot;wire [4:0] enc_p,enc_n;wire adc_locked;
 wire [31:0] source_word;wire source_valid,precision_mode,start_ready,active,done,fault,record_frozen,triggered,request_error,trigger_second;
 wire [AW-1:0] record_start;wire [AW:0] record_words,trigger_index,unread;
 wire [63:0] committed,read_ordinal;wire [1:0] bank_busy;
 reg host_release=0,release_bank=0,release_token=0,read_enable=0;
 reg [13:0] read_halfword=0;wire host_fault;wire [1:0] host_ready,host_token;
 wire [63:0] host_first0,host_first1;wire [11:0] host_words0,host_words1;
 wire read_valid,read_error;wire [15:0] read_data;
 wire [31:0] dq;wire k1,k2,g1;
 sram_acquisition_path #(.AW(AW)) dut(.*);
 reg [31:0] memory[0:N-1],expected[0:16383],s1=0,s2=0;
 reg [AW-1:0] address=0;
 assign dq=k1 && g1 ? s2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)memory[address]<=dq;else begin s1<=memory[address];s2<=s1;end
  address<=address+1'b1;
 end
 integer cycles=0,sent=0,received=0,expected_count=0,cases=0,blocks=0;
 reg collect=0,allow_final_release=0,expect_fault=0;
 always @(negedge core_clk)begin
  cycles=cycles+1;
  // Rising edge is in the second CH1 sample of every raw word. CH2 varies
  // independently; precision mock preserves varying fractional bits.
  lane[31:0]={8'(cycles*7),8'd20,8'(cycles*13),8'd10};
 end
 // Independent two-cycle data delay for the finite trigger pipeline.
 reg [31:0] expected_d0=0,expected_d1=0;
 reg expected_v0=0,expected_v1=0;
 always @(posedge core_clk)begin
  if(!dut.frontend_enable)begin expected_v0<=0;expected_v1<=0;end
  else begin
   expected_d0<=source_word;expected_d1<=expected_d0;
   expected_v0<=source_valid;expected_v1<=expected_v0;
  end
  if(collect && ((dut.ca && dut.cr && expected_v1) || (!dut.ca && dut.ben && source_valid)))begin
   if(sent>=16384)$fatal(1,"scoreboard capacity");
   expected[sent]=dut.ca ? expected_d1 : source_word;sent=sent+1;
  end
  if(!reset && !expect_fault && fault)$fatal(1,"unexpected acquisition fault");
 end
 task tick;begin @(negedge core_clk);#0.1;end endtask
 task launch(input integer op);
 begin wait(start_ready);tick;operation=op;start=1;tick;start=0;
  if(request_error)$fatal(1,"valid start rejected op=%0d",op);
  if(!active || start_ready || done)$fatal(1,"accepted command did not reserve immediately or retained done");
 end endtask
 task reject_start(input integer op);
 begin tick;operation=op;start=1;tick;start=0;
  if(!request_error)$fatal(1,"invalid/busy start accepted op=%0d",op);
 end endtask
 integer bank,n,i;reg [31:0] wanted;
 initial begin
  wait(!reset);
  forever begin
   @(negedge host_clk);
   if(!expect_fault && ((host_ready[0] && host_first0==received) || (host_ready[1] && host_first1==received)))begin
    bank=host_ready[0] && host_first0==received ? 0 : 1;
    n=bank ? host_words1 : host_words0;
    if(n<1 || n>2560)$fatal(1,"host bank count");
    if(blocks==0)repeat(1000)@(negedge host_clk);
    for(i=0;i<n*2+2;i=i+1)begin
     @(negedge host_clk);
     if(!host_ready[bank])$fatal(1,"bank released while reading");
     if(i>=2)begin
      wanted=expected[received];
      if(!read_valid || read_error || read_data!==((i-2)%2 ? wanted[31:16] : wanted[15:0]))
       $fatal(1,"capture %0d word %0d half %0d got=%h wanted=%h",cases,received,(i-2)%2,read_data,wanted);
      if((i-2)%2)received=received+1;
     end
     read_enable=i<n*2;read_halfword=bank*5120+i;
    end
    if(received==expected_count)wait(allow_final_release);
    @(negedge host_clk);host_release=1;release_bank=bank;release_token=host_token[bank];
    @(negedge host_clk);host_release=0;blocks=blocks+1;
   end
  end
 end
 task complete_read;
 begin
  wait(received==expected_count);tick;
  if(done || start_ready || bank_busy==0)$fatal(1,"final bank ownership lost");
  reject_start(0);allow_final_release=1;wait(done);repeat(5)tick;
  if(read_ordinal!=expected_count || bank_busy!=0)$fatal(1,"read accounting");
  cases=cases+1;$display("PASS integrated operation %0d: %0d words, %0d banks",cases,received,blocks);$fflush();
 end endtask
 task recall;
 begin
  received=0;blocks=0;expected_count=sent;allow_final_release=0;
  offset=0;length=record_words;launch(2);
  offset=N;length=N;read_bias=7;operation=3;tick;read_bias=0;
  if(HOST_FAULT_ONLY)begin
   wait(dut.backend.engine.finite.state==10);tick;expect_fault=1;
   force dut.backend.engine.host_core_fault=1'b1;#0.1;
   if(record_frozen)$fatal(1,"host fault left public frozen status valid");
   tick;if(!fault || !dut.backend.engine.ff)$fatal(1,"recall did not handle host fault locally");
   wait(!active);tick;
   $display("PASS recall host fault: immediate public invalidation, local failure and transport drain");
   $finish;
  end
  complete_read;
 end endtask
 initial begin
  repeat(10)tick;reset=0;repeat(10)tick;
  // Mask cached readiness immediately when an external invalidation arrives.
  wait(start_ready);tick;locked=0;start=1;#0.1;
  if(start_ready)$fatal(1,"lock loss left stale readiness");
  tick;start=0;if(!request_error || active)$fatal(1,"accepted start on lock loss");
  locked=1;wait(start_ready);tick;
  force dut.source_fault=1'b1;start=1;#0.1;
  if(start_ready)$fatal(1,"source fault left stale readiness");
  tick;start=0;if(!request_error || active)$fatal(1,"accepted faulted start");
  release dut.source_fault;wait(start_ready);tick;
  reset=1;start=1;#0.1;if(start_ready)$fatal(1,"reset left stale readiness");
  tick;start=0;if(active)$fatal(1,"accepted start during reset");
  reset=0;wait(start_ready);tick;
  expect_fault=1;force dut.bf=1'b1;start=1;#0.1;
  if(start_ready)$fatal(1,"backend fault left stale readiness");
  tick;start=0;if(!request_error || active)$fatal(1,"accepted backend-faulted start");
  release dut.bf;expect_fault=0;wait(start_ready);tick;
  force dut.bank_busy=2'b01;start=1;#0.1;
  if(start_ready)$fatal(1,"owned bank left stale readiness");
  tick;start=0;if(!request_error || active)$fatal(1,"accepted start with owned bank");
  release dut.bank_busy;wait(start_ready);tick;
  $display("PASS cached readiness: lock loss, source/backend faults, reset and bank ownership reject starts immediately");
  // Pending command must reserve immediately and cancel without SRAM traffic.
  launch(0);reset=1;tick;reset=0;repeat(5)tick;
  if(active || record_frozen || dut.writer_request || dut.backend.command)
   $fatal(1,"reset failed to cancel pending dispatch");
  wait(start_ready);expect_fault=1;launch(0);force dut.source_fault=1'b1;
  tick;release dut.source_fault;
  if(!fault || !request_error || active || record_frozen || dut.writer_request)
   $fatal(1,"pending fault did not cancel dispatch and invalidate epoch");
  reset=1;tick;reset=0;expect_fault=0;wait(start_ready);
  decim_log=8;expect_fault=1;launch(1);locked=0;tick;locked=1;
  if(!fault || !request_error || active || dut.ben || record_frozen)
   $fatal(1,"pending stream lock loss did not cancel dispatch");
  reset=1;tick;reset=0;expect_fault=0;decim_log=0;wait(start_ready);
  $display("PASS dispatch: immediate reservation, pending reset/source-fault/stream-lock cancellation");
  if(READY_ONLY)begin
   halt=1;launch(0);halt=0;wait(!active);tick;
   if(!record_frozen || record_words!=0 || fault)
    $fatal(1,"dispatch lost coincident finite halt");
   $display("PASS dispatch: coincident halt retained through child launch");
   $finish;
  end
  // Reject each mode's illegal settings before any operation can start.
  pre_count=N;post_count=1;reject_start(0);
  pre_count=0;post_count=0;reject_start(0);post_count=1;
  decim_log=3;reject_start(0);decim_log=0;
  trigger_mode=3;reject_start(0);trigger_mode=0;
  decim_log=7;reject_start(1);decim_log=0;
  reject_start(2);reject_start(3);
  if(active || record_frozen || triggered)$fatal(1,"invalid request changed acquisition state");
  pre_count=3000;post_count=17;trigger_mode=1;decim_log=0;collect=1;
  launch(0);pre_count=N;post_count=0;decim_log=3;trigger_level=16'hffff;trigger_mode=3;
  reject_start(1);wait(record_frozen && !active);collect=0;
  if(sent!=3017 || trigger_index!=3000 || !trigger_second || !triggered)$fatal(1,"raw trigger geometry/phase");
  offset=record_words;length=1;reject_start(2);
  if(!record_frozen || record_words!=3017 || trigger_index!=3000)$fatal(1,"invalid recall damaged frozen record");
  recall;
  // Fractional precision words share the same record and host path.
  sent=0;pre_count=31;post_count=17;trigger_mode=0;decim_log=8;collect=1;
  launch(0);decim_log=0;wait(record_frozen && !active);collect=0;
  if(sent!=48 || record_words!=48 || trigger_index!=31 || !precision_mode)$fatal(1,"precision capture");
  recall;
  sent=0;received=0;blocks=0;expected_count=6001;allow_final_release=0;
  decim_log=8;collect=1;launch(1);
  if(record_frozen)$fatal(1,"stream kept stale finite record valid");
  wait(sent==6001);tick;halt=1;collect=0;
  complete_read;halt=0;
  if(committed!=6001 || unread!=0)$fatal(1,"stream accounting");
  reject_start(2);
  // A live ADC failure must also stop/invalidate the streaming controller.
  expect_fault=1;decim_log=8;launch(1);wait(dut.ben);repeat(10)tick;
  lane[33]=1;wait(fault);lane[33]=0;wait(!active);repeat(5)tick;
  if(!fault || !dut.bf || record_frozen || start_ready)$fatal(1,"stream frontend fault not retained");
  reject_start(0);
  $display("PASS integrated ADC-source control, raw second-sample edge, precision capture, streaming, recall ownership, stale-record rejection and frontend fault");$finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule
