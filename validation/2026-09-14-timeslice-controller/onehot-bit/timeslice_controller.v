// Continuous SRAM capture scheduler. All ports are in the 250 MHz core domain.
// Host banks are reserved only after reaching the read target, allowing ARM to
// drain the preceding banks during the address scan. Busy banks never stall a
// scan: return to writing and retry, retaining unread words in SRAM.
//
// The caller supplies a common ingress epoch reset, stops offering samples when
// source_enable drops, and asserts source_finished AFTER its final offered word.
// Host release tokens must be validated before bank_release. A bank_done pulse
// means all words were emitted; RAM/CPU publication must wait for actual RAM
// writes to finish. Host writes cannot be backpressured during a read burst.
// read_bias is a board-calibrated origin correction, zero in the ideal model.
// This scheduler does not yet implement trigger-driven record geometry.
module sram_timeslice_controller #(parameter AW=19,BANK_WORDS=2560,READ_RESERVE=32,READ_WARM=16)(
 input wire clk,reset,start,stop,source_finished,
 output wire start_ready,source_enable,
 input wire [AW-1:0] read_bias,
 input wire [35:0] ingress_data,input wire ingress_valid,
 input wire [15:0] ingress_pending,input wire ingress_fault,host_fault,
 output wire ingress_ready,
 output reg active=0,done=0,capture_done=0,fault=0,
 output reg [3:0] error_code=0,
 output wire [63:0] committed,read_ordinal,
 output reg [AW:0] unread=0,
 input wire [1:0] bank_release,
 output reg [1:0] bank_busy=0,bank_begin=0,bank_done=0,
 output reg [63:0] bank_first0=0,bank_first1=0,
 output reg [AW:0] bank_words0=0,bank_words1=0,
 output wire host_valid,host_bank,output wire [31:0] host_data,
 output wire [$clog2(BANK_WORDS)-1:0] host_index,
 input wire transport_ready,transport_write_ready,transport_done,
 input wire [AW-1:0] position,
 input wire transport_read_valid,input wire [31:0] transport_read_data,
 output reg command=0,command_read=0,command_discard=0,
 output wire command_continue,output reg [AW:0] command_count=0,
 output wire write_valid,write_stop,output wire [31:0] write_data
);
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 localparam [14:0] IDLE=15'h0001,
  W_LAUNCH=15'h0002,
  W_WAIT=15'h0004,
  WRITING=15'h0008,
  W_STOP=15'h0010,
  SEEK_CALC=15'h0020,
  SEEK_ISSUE=15'h0040,
  SEEK_WAIT=15'h0080,
  TARGET=15'h0100,
  READING=15'h0200,
  FROZEN=15'h0400,
  FAILED=15'h0800,
  READ_ISSUE=15'h1000,
  SELECT_BANKS=15'h2000,
  COUNT_READY=15'h4000;
 (* syn_encoding = "user", preserve *) reg [14:0] state=IDLE;
 reg stop_seen=0,final_stop=0,returning=0,current_bank=0;
 reg [AW-1:0] origin=0,return_head=0,seek_target=0,seek_distance=0;
 localparam CW=$clog2(2*BANK_WORDS+1),WW=READ_WARM>0 ? $clog2(READ_WARM+1) : 1;
 reg [CW-1:0] remaining=0,payload_count=0;
 reg [WW-1:0] warm_left=0;
 (* preserve *) reg payload_enabled=0,warm_done=0,remaining_last=0,index_last=0,index_first=1;
 reg [$clog2(BANK_WORDS)-1:0] index=0;
 // Address lookahead is consumed only after transport drain, never during
 // payload reads. Those drain clocks let both arithmetic stages settle.
 (* preserve *) reg [AW-1:0] read_address=0,read_target=0;
 always @(posedge clk)begin
  read_address<=origin+read_ordinal[AW-1:0];
  read_target<=read_address-READ_WARM;
 end
 wire arm=start && start_ready;
 wire [1:0] free_banks=~(bank_busy & ~bank_release);
 (* preserve *) reg [1:0] sampled_free=0;
 (* preserve *) reg [AW:0] sampled_unread=0;
 wire [AW:0] bank_capacity=&sampled_free ? 2*BANK_WORDS : BANK_WORDS;
 (* preserve *) reg [AW:0] next_count=0;
 wire first_bank=!sampled_free[0];
 wire [1:0] selected_banks=next_count>BANK_WORDS ? 2'b11 : (first_bank ? 2'b10 : 2'b01);
 assign start_ready=state[$clog2(IDLE)] && transport_ready && bank_busy==0 && !reset;
 assign source_enable=active && !stop_seen && !stop && !fault && !reset;
 assign ingress_ready=state[$clog2(WRITING)] && transport_write_ready && !unread[AW] && !fault && !reset;
 assign write_valid=state[$clog2(WRITING)] && ingress_valid && !unread[AW] && !fault && !reset;
 assign write_data=ingress_data[31:0];
 assign write_stop=reset || (!state[$clog2(W_LAUNCH)] && !state[$clog2(W_WAIT)] && !state[$clog2(WRITING)]);
 assign command_continue=0;
 wire write_step=ingress_ready && ingress_valid;
 wire read_step=transport_read_valid && payload_enabled && !fault && !reset;
 assign host_valid=read_step;assign host_data=transport_read_data;
 assign host_bank=current_bank;assign host_index=index;
 // No word can be accepted during start/transport setup. Pipeline epoch
 // clearing here to keep start_ready's bank decode off every ordinal register.
 (* preserve *) reg ordinal_clear=1;
 always @(posedge clk)ordinal_clear<=reset || arm;
 sram_ordinal_counter wc(clk,ordinal_clear,write_step,committed);
 sram_ordinal_counter rc(clk,ordinal_clear,read_step,read_ordinal);
 // Metadata is invalid until bank_done. Register its small enable signals
 // separately so runtime reset/word-phase decode does not fan through 128-bit
 // metadata updates. Delay bank_done with its final count by one clock.
 reg [63:0] snapshot_ordinal=0;
 reg [AW:0] snapshot_count=0;
 reg [1:0] first_pending=0,last_pending=0;
 always @(posedge clk)begin
  snapshot_ordinal<=read_ordinal;snapshot_count<=index+1'b1;
  if(reset)begin first_pending<=0;last_pending<=0;bank_done<=0;end
  else begin
   first_pending<={2{read_step && index_first}} & (current_bank ? 2'b10 : 2'b01);
   last_pending<={2{read_step && (index_last || remaining_last)}} & (current_bank ? 2'b10 : 2'b01);
   bank_done<=last_pending & {2{!fault}};
  end
  if(first_pending[0])bank_first0<=snapshot_ordinal;
  if(first_pending[1])bank_first1<=snapshot_ordinal;
  if(last_pending[0])bank_words0<=snapshot_count;
  if(last_pending[1])bank_words1<=snapshot_count;
 end
 always @(posedge clk)begin
  command<=0;bank_begin<=0;
  if(reset)begin
   state<=IDLE;active<=0;done<=0;capture_done<=0;fault<=0;error_code<=0;
   stop_seen<=0;final_stop<=0;returning<=0;bank_busy<=0;unread<=0;
   origin<=0;return_head<=0;seek_target<=0;seek_distance<=0;
   remaining<=0;payload_count<=0;warm_left<=0;index<=0;current_bank<=0;
   payload_enabled<=0;warm_done<=0;remaining_last<=0;index_last<=0;index_first<=1;
   command_read<=0;command_discard<=0;command_count<=0;
  end else begin
   bank_busy<=bank_busy & ~bank_release;
   if(active && stop)stop_seen<=1;
   if(write_step!=read_step)unread<=unread+(read_step ? {(AW+1){1'b1}} : 1'b1);
   case(1'b1) // synthesis parallel_case
    state[$clog2(IDLE)]:if(arm)begin
     active<=1;done<=0;capture_done<=0;fault<=0;error_code<=0;
     stop_seen<=0;unread<=0;origin<=position+read_bias;state<=W_LAUNCH;
    end
    state[$clog2(W_LAUNCH)]:if(transport_ready)begin
     command<=1;command_read<=0;command_discard<=0;command_count<=0;state<=W_WAIT;
    end
    state[$clog2(W_WAIT)]:if(transport_write_ready)state<=WRITING;
    state[$clog2(WRITING)]:begin
     if(ingress_valid && unread==DEPTH)begin fault<=1;error_code<=2;state<=FAILED;end
     else if(stop_seen && source_finished && ingress_pending==0)begin final_stop<=1;state<=W_STOP;end
     else if(unread>=2*BANK_WORDS+READ_RESERVE && ingress_pending<=4)begin final_stop<=0;state<=W_STOP;end
    end
    state[$clog2(W_STOP)]:if(transport_ready)begin
     return_head<=position;
     if(final_stop)begin capture_done<=1;state<=FROZEN;end
     else begin seek_target<=read_target;returning<=0;state<=SEEK_CALC;end
    end
    state[$clog2(SEEK_CALC)]:begin seek_distance<=seek_target-position;state<=SEEK_ISSUE;end
    state[$clog2(SEEK_ISSUE)]:if(transport_ready)begin
     if(seek_distance==0)begin if(returning)state<=W_LAUNCH;else state<=TARGET;end
     else begin command<=1;command_read<=1;command_discard<=1;command_count<={1'b0,seek_distance};state<=SEEK_WAIT;end
    end
    state[$clog2(SEEK_WAIT)]:if(transport_done)begin if(returning)state<=W_LAUNCH;else state<=TARGET;end
    state[$clog2(TARGET)]:if(transport_ready)begin
     sampled_free<=free_banks;sampled_unread<=unread;state<=SELECT_BANKS;
    end
    state[$clog2(SELECT_BANKS)]:begin
     next_count<=capture_done && sampled_unread<bank_capacity ? sampled_unread : bank_capacity;
     state<=COUNT_READY;
    end
    state[$clog2(COUNT_READY)]:begin
     if(sampled_free==0 || next_count==0)begin
      if(capture_done)state<=FROZEN;
      else begin seek_target<=return_head;returning<=1;state<=SEEK_CALC;end
     end else begin
      bank_busy<=(bank_busy & ~bank_release) | selected_banks;bank_begin<=selected_banks;
      remaining<=next_count;payload_count<=next_count;warm_left<=READ_WARM;index<=0;current_bank<=first_bank;
      remaining_last<=next_count==1;index_last<=BANK_WORDS==1;index_first<=1;
      warm_done<=READ_WARM==0;payload_enabled<=0;state<=READ_ISSUE;
     end
    end
    state[$clog2(READ_ISSUE)]:if(transport_ready)begin
     command<=1;command_read<=1;command_discard<=0;command_count<=payload_count+READ_WARM;
     payload_enabled<=READ_WARM==0;state<=READING;
    end
    state[$clog2(READING)]:begin
     if(transport_read_valid && !warm_done)begin
      warm_left<=warm_left-1'b1;
      if(warm_left==1)begin warm_done<=1;payload_enabled<=1;end
     end
     if(read_step)begin
      remaining<=remaining-1'b1;remaining_last<=remaining==2;
      if(remaining_last)payload_enabled<=0;
      if(index_last || remaining_last)begin
       current_bank<=!current_bank;index<=0;index_first<=1;index_last<=BANK_WORDS==1;
      end else begin index<=index+1'b1;index_first<=0;index_last<=index==BANK_WORDS-2;end
     end
     if(transport_read_valid && warm_done && !payload_enabled)begin fault<=1;error_code<=3;state<=FAILED;end
     else if(transport_done)begin
      if(remaining!=0 || !warm_done)begin fault<=1;error_code<=3;state<=FAILED;end
      else if(capture_done)state<=FROZEN;
      else begin seek_target<=return_head;returning<=1;state<=SEEK_CALC;end
     end
    end
    state[$clog2(FROZEN)]:begin
     if(unread==0)begin active<=0;done<=1;state<=IDLE;end
     else if(free_banks!=0)begin seek_target<=read_target;returning<=0;state<=SEEK_CALC;end
    end
    state[$clog2(FAILED)]:if(transport_ready)active<=0;
   endcase
   if(active && (ingress_fault || host_fault))begin fault<=1;error_code<=1;state<=FAILED;command<=0;end
  end
 end
endmodule
