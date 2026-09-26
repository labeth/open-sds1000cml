// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two direct-mapped pages over the frozen sequential-address SRAM transport.
// Raw eight-bit words contain CH1[n], CH2[n], CH1[n+1], CH2[n+1] in
// ascending byte order. Requests return one sample or an adjacent pair on both
// channels. Page misses use the existing seek/warmup reader, never writes.
// The caller grants exclusive transport ownership through response consumption
// and holds record metadata stable. Both this module and transport share reset;
// reset invalidates the retained-record epoch, not just the cache. This interface
// uses the 125 MHz numerical clock clk. Only burst transport uses the
// 250 MHz transport_clk. transport_owned is the fast-domain arbiter grant;
// owned and record metadata belong to clk and remain stable through response.
// CACHE_AW is 1..11, less than AW, with page size + READ_WARM <= SRAM depth.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043, REQ-SDS-044
module stack_record_cache #(parameter AW=19,CACHE_AW=8,READ_WARM=16)(
 input wire clk,transport_clk,reset,owned,frozen,raw8,transport_owned,
 input wire [31:0] epoch,record_id,
 input wire [AW-1:0] record_start,read_bias,input wire [AW:0] record_words,
 input wire request_valid,output wire request_ready,input wire [31:0] sample_index,input wire adjacent,
 output wire response_valid,input wire response_ready,output wire response_error,
 output reg [7:0] ch0_left=0,ch0_right=0,ch1_left=0,ch1_right=0,
 output wire busy,output reg fault=0,
 input wire transport_ready,transport_done,transport_read_valid,input wire [31:0] transport_read_data,
 input wire [AW-1:0] position,
 output wire command,command_read,command_discard,command_continue,output wire [AW:0] command_count
);
 localparam PAGE_WORDS=1<<CACHE_AW;
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 localparam IDLE=0,CHECK=1,LOOKUP=2,READ=3,CAPTURE=4,PREP=5,START=6,WAIT=7,RESPONSE=8,ERROR_WAIT=9;
 reg [3:0] state=IDLE;
 reg [31:0] held_epoch=0,held_record=0;
 reg [AW-1:0] held_start=0,held_bias=0,word_offset=0;
 reg [AW:0] held_words=0;
 reg odd=0,pair=0,second=0,error_l=0;
 wire key_matches={epoch,record_id,record_start,read_bias,record_words}==
  {held_epoch,held_record,held_start,held_bias,held_words};
 wire permission=owned && grant_sync[1] && frozen && raw8 && key_matches;
 wire [32:0] requested_end={1'b0,sample_index}+(adjacent ? 33'd2:33'd1);
 wire [AW+1:0] sample_limit={record_words,1'b0};
 assign request_ready=state==IDLE && owned && idle_sync[1] && grant_sync[1] && !fault && !reset && !local_reset[1];
 assign response_valid=state==RESPONSE && !reset && !local_reset[1];
 assign response_error=error_l || fault || !permission;
 assign busy=state!=IDLE && !reset;
 reg [1:0] cache_valid=0;
 reg [AW-CACHE_AW-1:0] tag0=0,tag1=0;
 wire bank=word_offset[CACHE_AW];
 wire [AW-CACHE_AW-1:0] page=word_offset[AW-1:CACHE_AW];
 wire hit=cache_valid[bank] && (bank ? tag1:tag0)==page;
 reg refill_bank=0;
 reg [AW-CACHE_AW-1:0] refill_page=0;
 reg [AW:0] refill_offset=0,refill_length=0;
 wire [AW:0] page_base={1'b0,word_offset[AW-1:CACHE_AW],{CACHE_AW{1'b0}}};
 wire [AW:0] page_left=held_words-page_base;
 // One bundled refill request crosses to the transport clock. Geometry is
 // held until completion; the fast domain snapshots it before launching.
 (* async_reg="true" *) reg [1:0] local_reset=3,core_reset=3;
 always @(posedge clk or posedge reset)
  if(reset)local_reset<=3;else local_reset<={local_reset[0],1'b0};
 always @(posedge transport_clk or posedge reset)
  if(reset)core_reset<=3;else core_reset<={core_reset[0],1'b0};
 reg request_toggle=0,completion_seen=0,refill_done=0,refill_error=0;
 (* async_reg="true",preserve *) reg [1:0] completion_sync=0,idle_sync=0,grant_sync=0,available_sync=0;
 (* async_reg="true",preserve *) reg [1:0] request_sync=0,abort_sync=0;
 reg request_seen=0,completion_toggle=0,core_failed=0;
 localparam CORE_IDLE=0,CORE_LAUNCH=1,CORE_RUN=2,CORE_DRAIN=3;
 reg [1:0] core_state=CORE_IDLE;
 reg [AW-1:0] core_start=0,core_bias=0;
 reg [AW:0] core_words=0,core_offset=0,core_length=0;
 reg core_bank=0;
 wire recall_ready,recall_active,recall_done,recall_error,recall_fault,word_valid;
 wire [1:0] bank_done,cache_release;
 wire [11:0] word_index;wire [31:0] word_data,fetched;
 wire recall_command,memory_fault,memory_core_fault,read_ready,read_response;
 wire core_abort=abort_sync[1] || memory_core_fault || !transport_owned;
 wire core_available=core_state==CORE_IDLE && recall_ready && transport_owned;
 wire bridge_quiet=request_toggle==completion_seen && idle_sync[1];
 always @(posedge clk or posedge local_reset[1])begin
  if(local_reset[1])begin
   request_toggle<=0;completion_seen<=0;refill_done<=0;refill_error<=0;
   completion_sync<=0;idle_sync<=0;grant_sync<=0;available_sync<=0;
  end else begin
   completion_sync<={completion_sync[0],completion_toggle};
   idle_sync<={idle_sync[0],transport_ready};grant_sync<={grant_sync[0],transport_owned};
   available_sync<={available_sync[0],core_available};
   refill_done<=0;
   if(state==START && available_sync[1] && !fault)request_toggle<=!request_toggle;
   if(completion_sync[1]!=completion_seen)begin
    completion_seen<=completion_sync[1];refill_error<=core_failed;refill_done<=1;
   end
  end
 end
 always @(posedge transport_clk or posedge core_reset[1])begin
  if(core_reset[1])begin
   request_sync<=0;abort_sync<=0;request_seen<=0;completion_toggle<=0;core_state<=CORE_IDLE;core_failed<=0;
  end else begin
   request_sync<={request_sync[0],request_toggle};
   abort_sync<={abort_sync[0],fault || !owned || !frozen || !raw8};
   case(core_state)
    CORE_IDLE:if(request_sync[1]!=request_seen)begin
     request_seen<=request_sync[1];core_failed<=0;
     core_start<=held_start;core_bias<=held_bias;core_words<=held_words;
     core_offset<=refill_offset;core_length<=refill_length;core_bank<=refill_bank;
     if(core_abort)begin core_failed<=1;core_state<=CORE_DRAIN;end
     else core_state<=CORE_LAUNCH;
    end
    CORE_LAUNCH:begin
     if(core_abort)begin core_failed<=1;core_state<=CORE_DRAIN;end
     else if(recall_ready)core_state<=CORE_RUN;
    end
    CORE_RUN:begin
     if(core_abort || recall_error || recall_fault)begin core_failed<=1;core_state<=CORE_DRAIN;end
     else if(recall_done)begin completion_toggle<=request_seen;core_state<=CORE_IDLE;end
    end
    CORE_DRAIN:if(transport_ready)begin completion_toggle<=request_seen;core_state<=CORE_IDLE;end
   endcase
  end
 end
 wire [1:0] page_done=(|bank_done) ? (core_bank ? 2'b10:2'b01):2'b00;
 wire [1:0] recall_release={1'b0,|cache_release};
 stack_cache_memory #(.CACHE_AW(CACHE_AW)) storage(
 .reset(reset),.core_clk(transport_clk),.memory_clk(clk),
 .word_valid(word_valid),.word_bank(core_bank),.word_data(word_data),.word_index(word_index),
 .bank_done(page_done),.first0({{(63-AW){1'b0}},core_offset}),.first1({{(63-AW){1'b0}},core_offset}),
 .words0({{(19-AW){1'b0}},core_length}),.words1({{(19-AW){1'b0}},core_length}),
 .core_fault(memory_core_fault),.core_release(cache_release),.read_fault(memory_fault),
 .read_valid(state==READ && !fault),.read_ready(read_ready),.read_word(word_offset[CACHE_AW:0]),
 .response_valid(read_response),.response_ready(state==CAPTURE),.response_data(fetched));
 assign command=recall_command && transport_owned && !core_abort && !reset && !core_reset[1];
 sram_finite_recall #(.AW(AW),.BANK_WORDS(PAGE_WORDS),.READ_WARM(READ_WARM),.CONTINUE_READS(0)) recall(
 .clk(transport_clk),.reset(core_reset[1]),.start(core_state==CORE_LAUNCH && !core_abort),.frozen(transport_owned),
 .record_start(core_start),.read_bias(core_bias),.record_words(core_words),.offset(core_offset),.length(core_length),
 .start_ready(recall_ready),.active(recall_active),.done(recall_done),.request_error(recall_error),.fault(recall_fault),
 .host_core_fault(core_abort),.bank_release(recall_release),.bank_done(bank_done),
 .word_valid(word_valid),.word_data(word_data),.word_index(word_index),
 .transport_ready(transport_ready),.transport_done(transport_done),.transport_read_valid(transport_read_valid),
 .transport_read_data(transport_read_data),.position(position),
 .command(recall_command),.command_read(command_read),.command_discard(command_discard),
 .command_continue(command_continue),.command_count(command_count));
 always @(posedge clk)begin
  if(reset || local_reset[1])begin state<=IDLE;fault<=0;cache_valid<=0;error_l<=0;end
  else begin
   case(state)
    IDLE:if(request_valid && request_ready)begin
     held_epoch<=epoch;held_record<=record_id;held_start<=record_start;held_bias<=read_bias;held_words<=record_words;
     if(!key_matches)cache_valid<=0;
     word_offset<=sample_index[AW:1];odd<=sample_index[0];pair<=adjacent;second<=0;
     ch0_left<=0;ch0_right<=0;ch1_left<=0;ch1_right<=0;
     error_l<=!frozen || !raw8 || record_words==0 || record_words>DEPTH || requested_end>sample_limit;
     state<=CHECK;
    end
    CHECK:begin
     if(error_l)state<=RESPONSE;
     else if(!permission)begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
     else state<=LOOKUP;
    end
    LOOKUP:if(hit)state<=READ;else state<=PREP;
    READ:if(read_ready)state<=CAPTURE;
    CAPTURE:if(read_response)begin
     if(second)begin ch0_right<=fetched[7:0];ch1_right<=fetched[15:8];state<=RESPONSE;end
     else begin
      ch0_left<=odd ? fetched[23:16]:fetched[7:0];ch1_left<=odd ? fetched[31:24]:fetched[15:8];
      if(pair && odd)begin word_offset<=word_offset+1'b1;second<=1;state<=LOOKUP;end
      else begin
       if(pair)begin ch0_right<=fetched[23:16];ch1_right<=fetched[31:24];end
       state<=RESPONSE;
      end
     end
    end
    PREP:begin
     refill_bank<=bank;refill_page<=page;cache_valid[bank]<=0;
     refill_offset<=page_base;refill_length<=page_left>PAGE_WORDS ? PAGE_WORDS:page_left;
     state<=START;
    end
    START:if(available_sync[1])state<=WAIT;
    WAIT:begin
     if(memory_fault || (refill_done && refill_error))begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
     else if(refill_done)begin
      cache_valid[refill_bank]<=1;if(refill_bank)tag1<=refill_page;else tag0<=refill_page;
      state<=LOOKUP;
     end
    end
    RESPONSE:if(response_ready)state<=IDLE;
    ERROR_WAIT:if(bridge_quiet)state<=RESPONSE;
    default:begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
   endcase
   // A live record/ownership change invalidates even a held response. The
   // combinational response_error also covers the simultaneous consume edge.
   if(state!=IDLE && state!=CHECK && !error_l && !permission)begin
    fault<=1;error_l<=1;cache_valid<=0;
    if(state!=RESPONSE)state<=ERROR_WAIT;
   end
   if(!owned || !grant_sync[1] || !frozen || !raw8)cache_valid<=0;
   if(memory_fault)begin
    fault<=1;error_l<=1;cache_valid<=0;
    // A sticky fault must not override drain completion or manufacture an
    // unsolicited response after the failed transaction was consumed.
    if(state!=IDLE && state!=RESPONSE && state!=ERROR_WAIT)state<=ERROR_WAIT;
   end
  end
 end
endmodule
