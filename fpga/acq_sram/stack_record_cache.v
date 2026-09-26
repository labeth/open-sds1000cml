// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two direct-mapped pages over the frozen sequential-address SRAM transport.
// Raw eight-bit words contain CH1[n], CH2[n], CH1[n+1], CH2[n+1] in
// ascending byte order. Requests return one sample or an adjacent pair on both
// channels. Page misses use the existing seek/warmup reader, never writes.
// The caller grants exclusive transport ownership through response consumption
// and holds record metadata stable. Both this module and transport share reset;
// reset invalidates the retained-record epoch, not just the cache. This interface
// is in the transport clock domain; crossing to the numerical clock is external.
// CACHE_AW is 1..11, less than AW, with page size + READ_WARM <= SRAM depth.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043, REQ-SDS-044
module stack_record_cache #(parameter AW=19,CACHE_AW=8,READ_WARM=16)(
 input wire clk,reset,owned,frozen,raw8,
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
 wire permission=owned && frozen && raw8 && key_matches;
 wire [32:0] requested_end={1'b0,sample_index}+(adjacent ? 33'd2:33'd1);
 wire [AW+1:0] sample_limit={record_words,1'b0};
 assign request_ready=state==IDLE && owned && transport_ready && !fault && !reset;
 assign response_valid=state==RESPONSE && !reset;
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
 (* ramstyle="M9K" *) reg [31:0] memory[0:2*PAGE_WORDS-1];
 reg [31:0] fetched=0;
 wire recall_ready,recall_active,recall_done,recall_error,recall_fault,word_valid;
 wire [1:0] bank_done;
 wire [11:0] word_index;
 wire [31:0] word_data;
 wire recall_command;
 always @(posedge clk)begin
  if(word_valid && permission && !fault && !reset)memory[{refill_bank,word_index[CACHE_AW-1:0]}]<=word_data;
  if(state==READ)fetched<=memory[word_offset[CACHE_AW:0]];
 end
 assign command=recall_command && permission && !fault && !reset;
 sram_finite_recall #(.AW(AW),.BANK_WORDS(PAGE_WORDS),.READ_WARM(READ_WARM),.CONTINUE_READS(0)) recall(
 .clk(clk),.reset(reset),.start(state==START && recall_ready && permission && !fault),.frozen(permission),
 .record_start(held_start),.read_bias(held_bias),.record_words(held_words),.offset(refill_offset),.length(refill_length),
 .start_ready(recall_ready),.active(recall_active),.done(recall_done),.request_error(recall_error),.fault(recall_fault),
 .host_core_fault(!permission),.bank_release(bank_done),.bank_done(bank_done),
 .word_valid(word_valid),.word_data(word_data),.word_index(word_index),
 .transport_ready(transport_ready),.transport_done(transport_done),.transport_read_valid(transport_read_valid),
 .transport_read_data(transport_read_data),.position(position),
 .command(recall_command),.command_read(command_read),.command_discard(command_discard),
 .command_continue(command_continue),.command_count(command_count));
 always @(posedge clk)begin
  if(reset)begin state<=IDLE;fault<=0;cache_valid<=0;error_l<=0;end
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
    READ:state<=CAPTURE;
    CAPTURE:begin
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
    START:if(recall_ready)state<=WAIT;
    WAIT:begin
     if(recall_error || recall_fault)begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
     else if(recall_done)begin
      cache_valid[refill_bank]<=1;if(refill_bank)tag1<=refill_page;else tag0<=refill_page;
      state<=LOOKUP;
     end
    end
    RESPONSE:if(response_ready)state<=IDLE;
    ERROR_WAIT:if(transport_ready)state<=RESPONSE;
    default:begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
   endcase
   // A live record/ownership change invalidates even a held response. The
   // combinational response_error also covers the simultaneous consume edge.
   if(state!=IDLE && state!=CHECK && !error_l && !permission)begin
    fault<=1;error_l<=1;cache_valid<=0;
    if(state!=RESPONSE)state<=ERROR_WAIT;
   end
   if(!owned || !frozen || !raw8)cache_valid<=0;
  end
 end
endmodule
