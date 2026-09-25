// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-048
module tb;
 parameter STALL_BLOCK=5,REQUIRE_STALL=0,REAL_HOST=0,AW=10,BANK_WORDS=16,FIFO=4608,TARGET=6003,HOST_CYCLES_PER_WORD=100,STALL_CYCLES=10000,NO_RELEASE=0,RESET_PHASE=0,EPOCHS=1;
 localparam N=1<<AW;
 reg clk=0,ram_clk=0;always #2 clk=~clk;always #4 ram_clk=~ram_clk;
 wire sample_clk;assign #0.4 sample_clk=clk;
 reg reset=1,start=0,stop=0,source_finished=0;
 wire start_ready,source_enable,active,done,capture_done,fault;
 wire [3:0] error_code;wire [63:0] committed,read_ordinal;
 wire [AW:0] unread;reg [1:0] model_release=0;wire [1:0] bank_release;wire path_fault;
 wire [1:0] bank_busy,bank_begin,bank_done;
 wire [63:0] bank_first0,bank_first1;
 wire [AW:0] bank_words0,bank_words1;
 wire host_valid,host_bank;wire [31:0] host_data;
 wire [$clog2(BANK_WORDS)-1:0] host_index;
 wire transport_ready,transport_write_ready,transport_done,transport_read_valid;
 wire [31:0] transport_read_data;wire [AW-1:0] position;
 wire command,command_read,command_discard,command_continue,write_valid,write_stop;
 wire [AW:0] command_count;wire [31:0] write_data;
 wire [31:0] dq;wire k1,k2,g1;
 wire [35:0] ingress_data;wire ingress_valid,ingress_ready,ingress_fault,input_ready;
 wire [$clog2(FIFO+11)-1:0] pending;
 integer epoch_base=0,epoch_address=0,epoch=0;
 integer stalls_exercised=0;
 integer cycles=0,produced=0,physical_writes=0,host_words=0,blocks=0,max_pending=0,max_unread=0,skipped_scans=0,overlap_scans=0;
 reg source_valid=0;reg [35:0] source_data=0;
 wire [15:0] ingress_pending=pending;
 wire [63:0] next_read_ordinal=epoch_base+read_ordinal+dut.read_event;
 wire [3:0] next_commit_marker=committed[3:0]+dut.write_event;
 sram_ingress_path #(.BANKS(FIFO/512)) ingress(.reset(reset),.core(clk),.ram_clk(ram_clk),
  .source_valid(source_valid),.source_data(source_data),.source_ready(input_ready),
  .ready(ingress_ready),.valid(ingress_valid),.data_out(ingress_data),.pending(pending),.fault(ingress_fault));
 sram_timeslice_controller #(.AW(AW),.BANK_WORDS(BANK_WORDS)) dut(
  .clk(clk),.reset(reset),.start(start),.stop(stop),.source_finished(source_finished),
  .start_ready(start_ready),.source_enable(source_enable),.read_bias({AW{1'b0}}),
  .ingress_data(ingress_data),.ingress_valid(ingress_valid),.ingress_pending(ingress_pending),
  .ingress_fault(ingress_fault),.host_fault(path_fault),.ingress_ready(ingress_ready),
  .active(active),.done(done),.capture_done(capture_done),.fault(fault),.error_code(error_code),
  .committed(committed),.read_ordinal(read_ordinal),.unread(unread),
  .bank_release(bank_release),.bank_busy(bank_busy),.bank_begin(bank_begin),.bank_done(bank_done),
  .bank_first0(bank_first0),.bank_first1(bank_first1),.bank_words0(bank_words0),.bank_words1(bank_words1),
  .host_valid(host_valid),.host_bank(host_bank),.host_data(host_data),.host_index(host_index),
  .transport_ready(transport_ready),.transport_write_ready(transport_write_ready),.transport_done(transport_done),
  .position(position),.transport_read_valid(transport_read_valid),.transport_read_data(transport_read_data),
  .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),
  .command_count(command_count),.write_valid(write_valid),.write_stop(write_stop),.write_data(write_data));
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1)) transport(.clk(clk),.sample_clk(sample_clk),.reset(reset),.locked(1'b1),
  .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),
  .command_count(command_count),.ready(transport_ready),.write_data(write_data),.write_valid(write_valid),.write_stop(write_stop),
  .write_ready(transport_write_ready),.read_data(transport_read_data),.read_valid(transport_read_valid),.done(transport_done),
  .position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] memory[0:N-1],stage1=0,stage2=0;
 reg [AW-1:0] address=0;
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)begin
   if(address!=(epoch_address+physical_writes-epoch_base)%N || dq!==physical_writes)$fatal(1,"physical write sequence/address");
   if(physical_writes-epoch_base-read_ordinal>=N)$fatal(1,"overwrote unread SRAM word");
   memory[address]<=dq;physical_writes=physical_writes+1;
  end else begin stage1<=memory[address];stage2<=stage1;end
  address<=address+1'b1;
 end
 always @(negedge clk)begin
  source_valid=source_enable && produced<epoch_base+TARGET && cycles%128==0;
  source_data={4'(produced-epoch_base),32'(produced)};
  stop=produced>=epoch_base+TARGET;
 end
 reg finish_delay=0;
 reg [31:0] host_memory[0:2*BANK_WORDS-1];
 // REAL_HOST reads actual host RAM at 100 MHz and uses the production
 // descriptor/release handshake. The ideal scoreboard remains for fast
 // controller-only tests. SRAM pin behavior is modeled in both modes.
 wire [1:0] visible_done;
 generate if(REAL_HOST)begin: real_host
  reg arm_clk=0;always #5 arm_clk=~arm_clk;
  wire arm_fault;wire [1:0] ready,token;
  wire [63:0] first0,first1;wire [11:0] words0,words1;
  wire [11:0] index12=host_index;
  wire [19:0] count0=bank_words0,count1=bank_words1;
  reg release_en=0,release_bank=0,release_token=0;
  reg read_en=0;reg [13:0] read_address=0;
  wire read_valid,read_error;wire [15:0] read_data;
  sram_host_path path(reset,clk,ram_clk,arm_clk,host_valid,host_bank,host_data,index12,
   bank_done,bank_first0,bank_first1,count0,count1,path_fault,arm_fault,bank_release,
   release_en,release_bank,release_token,ready,token,first0,first1,words0,words1,
   read_en,read_address,read_valid,read_error,read_data);
  reg [1:0] seen_ready=0;
  always @(posedge clk)begin
   if(reset)seen_ready<=0;else seen_ready<=ready;
   if(!reset && (path_fault || arm_fault))$fatal(1,"real host path fault");
  end
  assign visible_done=ready & ~seen_ready;
  integer selected_bank,word_count,word_offset,drain_wait;
  reg [15:0] low_half;reg [31:0] assembled;
  task read_half(input integer address,output reg [15:0] value);
   begin
    @(negedge arm_clk);read_address=address;read_en=1;
    @(negedge arm_clk);read_en=0;
    @(posedge arm_clk);#0.1;
    if(!read_valid || read_error)$fatal(1,"ARM RAM read response");
    value=read_data;
   end
  endtask
  reg [15:0] high_half;
  initial begin
   wait(!reset);
   forever begin
    @(negedge arm_clk);
    if(!NO_RELEASE && ((ready[0] && first0==host_words-epoch_base) ||
                       (ready[1] && first1==host_words-epoch_base)))begin
     selected_bank=(ready[0] && first0==host_words-epoch_base) ? 0 : 1;
     word_count=selected_bank ? words1 : words0;
     if(word_count<1 || word_count>2560)$fatal(1,"ARM descriptor count");
     if(blocks==STALL_BLOCK && STALL_CYCLES>0)begin
      stalls_exercised=stalls_exercised+1;
      repeat((STALL_CYCLES*4+9)/10)@(negedge arm_clk);
     end
     for(word_offset=0;word_offset<word_count;word_offset=word_offset+1)begin
      if(!ready[selected_bank])$fatal(1,"ARM lost bank ownership");
      read_half(selected_bank*5120+word_offset*2,low_half);
      read_half(selected_bank*5120+word_offset*2+1,high_half);
      assembled={high_half,low_half};
      if(assembled!==host_words)$fatal(1,"ARM actual RAM ordering got=%h expected=%h",assembled,host_words);
      host_words=host_words+1;
      // Four read-port cycles plus idle cycles model requested average drain.
      drain_wait=(HOST_CYCLES_PER_WORD*4+9)/10-4;
      if(drain_wait>0)repeat(drain_wait)@(negedge arm_clk);
     end
     @(negedge arm_clk);release_en=1;release_bank=selected_bank;release_token=token[selected_bank];
     @(negedge arm_clk);release_en=0;blocks=blocks+1;
    end
   end
  end
  initial if(BANK_WORDS!=2560)$fatal(1,"real host requires 2560-word banks");
 end else begin: ideal_host
  assign bank_release=model_release;assign path_fault=1'b0;
  assign visible_done=bank_done;
  always @(posedge clk)if(host_valid)host_memory[host_bank*BANK_WORDS+host_index]<=host_data;
 end endgenerate
 reg [1:0] published=0;
 integer host_bank_active=-1,host_left=0,host_index_active=0,host_count_active=0;
 integer i;
 always @(posedge clk)begin
  cycles<=cycles+1;
  finish_delay<=!source_enable && !source_valid;
  source_finished<=finish_delay;
  model_release<=0;
  if(source_valid)begin
   if(!input_ready)$fatal(1,"source overload");
   produced<=produced+1;
  end
  if(ingress_ready && ingress_valid && ingress_data[35:32] !== next_commit_marker)$fatal(1,"commit marker misalignment");
  if(!reset && dut.unread_full !== unread[AW])$fatal(1,"full guard/accounting mismatch");
  if(dut.arm && bank_busy!=0)$fatal(1,"armed while a host bank was owned");
  if(dut.arm && unread!=0)$fatal(1,"rearm before unread drained");
  if(dut.read_step && dut.state!=dut.READING)$fatal(1,"read accepted outside payload phase");
  if(dut.write_step && dut.read_step)$fatal(1,"simultaneous read/write accounting");
  if(pending>max_pending)max_pending<=pending;
  if(unread>max_unread)max_unread<=unread;
  if(dut.state==dut.TARGET && transport_ready && dut.free_banks==0 && !capture_done)skipped_scans<=skipped_scans+1;
  if(dut.state==dut.W_STOP && transport_ready && !dut.final_stop && bank_busy!=0)overlap_scans<=overlap_scans+1;
  if(!NO_RELEASE && STALL_CYCLES==0 && unread>2*BANK_WORDS+32+64)$fatal(1,"steady-state backlog grows despite adequate host bandwidth");
  if(host_valid && host_data!==next_read_ordinal[31:0])$fatal(1,"read ordinal/payload mismatch");
  if(host_valid && published[host_bank])$fatal(1,"wrote a host bank before release");
  if(visible_done[0])begin if(published[0])$fatal(1,"overwrote busy host bank 0");published[0]=1;end
  if(visible_done[1])begin if(published[1])$fatal(1,"overwrote busy host bank 1");published[1]=1;end
  if(REAL_HOST)published=published & ~bank_release;
  if(!NO_RELEASE && !REAL_HOST)begin
   if(host_bank_active<0)begin
    if(published[0] && bank_first0==host_words-epoch_base)host_bank_active=0;
    else if(published[1] && bank_first1==host_words-epoch_base)host_bank_active=1;
    if(host_bank_active>=0)begin
     host_count_active=host_bank_active ? bank_words1 : bank_words0;
     if(host_count_active<1 || host_count_active>BANK_WORDS)$fatal(1,"invalid host count");
     host_index_active=0;host_left=HOST_CYCLES_PER_WORD;
     if(blocks==STALL_BLOCK && STALL_CYCLES>0)begin host_left=host_left+STALL_CYCLES;stalls_exercised=stalls_exercised+1;end
    end
   end else if(host_left>1)host_left=host_left-1;
   else begin
    if(host_memory[host_bank_active*BANK_WORDS+host_index_active]!==host_words)$fatal(1,"ARM read ordering");
    host_words=host_words+1;host_index_active=host_index_active+1;host_left=HOST_CYCLES_PER_WORD;
    if(host_index_active==host_count_active)begin
     model_release[host_bank_active]<=1;published[host_bank_active]=0;host_bank_active=-1;blocks=blocks+1;
    end
   end
  end
  if(!NO_RELEASE && fault)$fatal(1,"controller fault %0d",error_code);
 end
 initial begin
  repeat(10)@(negedge clk);reset=0;repeat(10)@(negedge clk);
  wait(start_ready);epoch_address=position;start=1;@(negedge clk);start=0;
  if(NO_RELEASE)begin
   wait(fault);repeat(100)@(negedge clk);
   if(error_code!=2 || physical_writes-read_ordinal!=N || memory[read_ordinal%N]!==32'(read_ordinal))$fatal(1,"unread protection failed");
   $display("PASS controller unread protection: writes=%0d read=%0d",physical_writes,read_ordinal);
  end else begin
   for(epoch=0;epoch<EPOCHS;epoch=epoch+1)begin
    if(epoch>0)begin
     #0.1;epoch_base=epoch*TARGET;
     wait(start_ready);epoch_address=position;start=1;@(negedge clk);start=0;
    end
    wait(done && host_words==epoch_base+TARGET && bank_busy==0);repeat(5)@(negedge clk);
   if(produced!=epoch_base+TARGET || physical_writes!=epoch_base+TARGET || committed!=TARGET || read_ordinal!=TARGET || pending!=0 || unread!=0 || bank_busy!=0)$fatal(1,"final accounting");
   end
   if(AW==19 && overlap_scans==0)$fatal(1,"scan/readout overlap was not exercised");
   if(AW==19 && STALL_CYCLES>=2500000 && skipped_scans==0)$fatal(1,"busy-bank skip was not exercised");
   if(REQUIRE_STALL && stalls_exercised!=1)$fatal(1,"required stall not exercised exactly once");
   $display("PASS RTL controller: words=%0d blocks=%0d max_pending=%0d max_unread=%0d skipped=%0d overlap=%0d stalls=%0d",host_words,blocks,max_pending,max_unread,skipped_scans,overlap_scans,stalls_exercised);
  end
  $finish;
 end
 initial if(RESET_PHASE!=0)begin
  wait(active && committed>=8);
  if(RESET_PHASE==2)wait(host_valid);
  @(negedge clk);#0.1;reset=1;source_valid=0;
  #0.1;
  if(write_valid || ingress_ready || host_valid || source_enable)$fatal(1,"reset failed to suppress external activity");
  @(posedge clk);#0.1;
  if(active || fault || unread!=0 || committed!=0 || read_ordinal!=0 || bank_busy!=0 || bank_done!=0)$fatal(1,"reset accounting/state");
  $display("PASS active reset phase=%0d",RESET_PHASE);$finish;
 end
 initial begin #200000000;$fatal(1,"timeout state=%0d produced=%0d host=%0d unread=%0d",dut.state,produced,host_words,unread);end
endmodule
