// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two direct-mapped pages over the frozen sequential-address SRAM transport.
// Raw eight-bit words contain CH1[n], CH2[n], CH1[n+1], CH2[n+1] in
// ascending byte order. Requests return one sample or an adjacent pair on both
// channels. Page misses seek and read the SRAM; nothing is ever written.
//
// Clocking: clk is the 125 MHz numerical clock and transport_clk the 250 MHz
// transport clock. Both MUST come from one PLL, transport_clk = 2 x clk, phase
// aligned, so every crossing below is an ordinary timed synchronous path.
// The fast side is a small page reader: it waits for an idle transport,
// registers the counter, seeks, reads READ_WARM discarded words plus the page,
// and packs words into pairs. A pair register changes at most once per clk
// period, so clk sees every pair exactly once. Completion is a toggle returned
// after the last pair, with an error flag.
//
// The caller grants exclusive transport ownership through response consumption
// and holds record metadata stable. transport_owned is the fast-domain grant;
// owned and record metadata belong to clk. Losing either, or any metadata
// change, poisons the reader until reset; an accepted burst always drains
// first. Commands may pass through up to SETTLE-1 caller register stages.
// Reset invalidates the retained-record epoch, not just the cache.
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
 localparam WW=$clog2(READ_WARM+1),SETTLE=6;
 localparam [AW-1:0] WARM_ADDRESS=READ_WARM;localparam [AW:0] WARM_COUNT=READ_WARM;
 localparam [WW-1:0] WARM_WORDS=READ_WARM;localparam [CACHE_AW:0] PAGE_LENGTH=PAGE_WORDS;
 localparam [CACHE_AW-1:0] BANK_PAIRS=PAGE_WORDS/2;

 // ---- resets: asynchronous assertion, synchronous release per domain ----
 (* async_reg="true" *) reg [1:0] slow_reset=3,fast_reset=3;
 always @(posedge clk or posedge reset)
  if(reset)slow_reset<=3;else slow_reset<={slow_reset[0],1'b0};
 always @(posedge transport_clk or posedge reset)
  if(reset)fast_reset<=3;else fast_reset<={fast_reset[0],1'b0};
 wire slow_rst=slow_reset[1],fast_rst=fast_reset[1];

 // ---- 125 MHz controller ----
 localparam IDLE=0,CHECK=1,LOOKUP=2,READ=3,CAPTURE=4,PREP=5,WAIT=6,RESPONSE=7,ERROR_WAIT=8,VALIDATE=9;
 reg [3:0] state=IDLE;
 reg [31:0] held_epoch=0,held_record=0;
 reg [AW-1:0] held_start=0,held_bias=0,word_offset=0;
 reg [AW:0] held_words=0;
 reg odd=0,pair=0,second=0,error_l=0;
 reg grant_q=0,idle_q=0;
 wire key_matches={epoch,record_id,record_start,read_bias,record_words}==
  {held_epoch,held_record,held_start,held_bias,held_words};
 // The identity match is registered: a metadata change is seen one clock
 // later. Callers already hold metadata stable through each response.
 reg key_q=0;
 wire permission=owned && grant_q && frozen && raw8 && key_q;
 reg [31:0] held_index=0;reg held_adjacent=0;
 wire [32:0] requested_end={1'b0,held_index}+(held_adjacent ? 33'd2:33'd1);
 reg [1:0] cache_valid=0;
 reg [AW-CACHE_AW-1:0] tag0=0,tag1=0;
 wire bank=word_offset[CACHE_AW];
 wire [AW-CACHE_AW-1:0] page=word_offset[AW-1:CACHE_AW];
 wire hit=cache_valid[bank] && (bank ? tag1:tag0)==page;
 wire [AW:0] page_base={1'b0,word_offset[AW-1:CACHE_AW],{CACHE_AW{1'b0}}};
 wire [AW:0] page_left=held_words-page_base;
 // Refill request: stable from the toggle until the matching completion.
 reg request_toggle=0,refill_bank=0;
 reg [AW-CACHE_AW-1:0] refill_page=0;
 reg [AW-1:0] refill_address=0;
 reg [CACHE_AW:0] refill_length=0;
 // Completion, registered on the fast side.
 reg done_toggle=0,done_error=0;
 // Every fast-side signal lands in a plain clk register before any decision:
 // the crossing is a logic-free half period and the logic gets a full one.
 // Pairs and completion are delayed alike, so their order is preserved.
 reg done_toggle_s=0,done_error_s=0,pair_toggle_s=0;
 wire refill_pending=request_toggle!=done_toggle_s;
 wire bridge_quiet=!refill_pending && idle_q;
 assign request_ready=state==IDLE && owned && idle_q && grant_q && !fault && !reset && !slow_rst;
 assign response_valid=state==RESPONSE && !reset && !slow_rst;
 assign response_error=error_l || fault || !permission;
 assign busy=state!=IDLE && !reset;

 // Page memory: written from fast-side pairs, read locally.
 (* ramstyle="M9K" *) reg [63:0] memory[0:PAGE_WORDS-1];
 reg [63:0] fetched=0;
 reg read_phase=0,pair_seen=0;
 reg pair_toggle=0;reg [63:0] pair_data=0,pair_data_s=0;reg [CACHE_AW-1:0] pair_address=0,pair_address_s=0;
 always @(posedge clk)begin
  done_toggle_s<=done_toggle;done_error_s<=done_error;
  pair_toggle_s<=pair_toggle;pair_data_s<=pair_data;pair_address_s<=pair_address;
  if(pair_toggle_s!=pair_seen)memory[pair_address_s]<=pair_data_s;
  fetched<=memory[word_offset[CACHE_AW:1]];
 end
 wire [31:0] fetched_word=word_offset[0] ? fetched[63:32]:fetched[31:0];

 always @(posedge clk)begin
  grant_q<=transport_owned;idle_q<=transport_ready;pair_seen<=pair_toggle_s;key_q<=key_matches;
  if(reset || slow_rst)begin
   state<=IDLE;fault<=0;cache_valid<=0;error_l<=0;read_phase<=0;request_toggle<=0;
  end else begin
   case(state)
    IDLE:if(request_valid && request_ready)begin
     held_epoch<=epoch;held_record<=record_id;held_start<=record_start;held_bias<=read_bias;held_words<=record_words;
     if(!key_matches)cache_valid<=0;
     word_offset<=sample_index[AW:1];odd<=sample_index[0];pair<=adjacent;second<=0;
     held_index<=sample_index;held_adjacent<=adjacent;key_q<=1;
     ch0_left<=0;ch0_right<=0;ch1_left<=0;ch1_right<=0;
     state<=VALIDATE;
    end
    // Range checks use the registered request, off the caller's index mux.
    VALIDATE:begin
     error_l<=!frozen || !raw8 || held_words==0 || held_words>DEPTH || requested_end>{1'b0,held_words,1'b0};
     state<=CHECK;
    end
    CHECK:begin
     if(error_l)state<=RESPONSE;
     else if(!permission)begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
     else state<=LOOKUP;
    end
    LOOKUP:if(hit)begin read_phase<=0;state<=READ;end else state<=PREP;
    // The RAM samples the settled address, then fetched holds its word.
    READ:if(read_phase)state<=CAPTURE;else read_phase<=1;
    CAPTURE:begin
     if(second)begin ch0_right<=fetched_word[7:0];ch1_right<=fetched_word[15:8];state<=RESPONSE;end
     else begin
      ch0_left<=odd ? fetched_word[23:16]:fetched_word[7:0];ch1_left<=odd ? fetched_word[31:24]:fetched_word[15:8];
      if(pair && odd)begin word_offset<=word_offset+1'b1;second<=1;state<=LOOKUP;end
      else begin
       if(pair)begin ch0_right<=fetched_word[23:16];ch1_right<=fetched_word[31:24];end
       state<=RESPONSE;
      end
     end
    end
    PREP:begin
     refill_bank<=bank;refill_page<=page;cache_valid[bank]<=0;
     refill_address<=held_start+held_bias+page_base[AW-1:0];
     refill_length<=page_left>PAGE_WORDS ? PAGE_LENGTH:page_left[CACHE_AW:0];
     request_toggle<=!request_toggle;state<=WAIT;
    end
    WAIT:if(!refill_pending)begin
     if(done_error_s)begin fault<=1;error_l<=1;cache_valid<=0;state<=ERROR_WAIT;end
     else begin
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
   if(state!=IDLE && state!=VALIDATE && state!=CHECK && !error_l && !permission)begin
    fault<=1;error_l<=1;cache_valid<=0;
    if(state!=RESPONSE)state<=ERROR_WAIT;
   end
   if(!owned || !grant_q || !frozen || !raw8)cache_valid<=0;
  end
 end

 // ---- 250 MHz page reader ----
 // Every decision below uses registered predicates. Count and flags are
 // valid no later than the command strobe and held until the next command,
 // so a caller may register count and strobe separately by equal stages.
 localparam F_IDLE=0,F_TARGET=1,F_DISTANCE=2,F_ZERO=3,F_SEEK=4,F_SEEK_WAIT=5,F_READ=6,F_READING=7,F_DRAIN=8,F_FINISH=9,F_MATCH=10,F_CHOOSE=11,F_DIST_HIGH=12;
 // The seek distance is subtracted in two halves with a registered borrow.
 localparam LO=AW/2;
 reg [3:0] fstate=F_IDLE;
 reg request_seen=0,fail=0,ready_q=0;
 reg [AW-1:0] target=0,idle_position=0,distance=0;reg distance_zero=0;
 reg borrow=0,low_zero=0;reg [AW-LO-1:0] position_high=0;
 reg [AW:0] read_total=0;
 reg [CACHE_AW:0] words_left=0;
 reg [WW-1:0] warm_left=0;
 reg warm_done=0,words_done=0;
 reg [CACHE_AW-1:0] pair_index=0;
 reg half_valid=0;reg [31:0] half=0;
 reg command_r=0,discard_r=0,continue_r=0;reg [AW:0] count_r=0,count_next=0;
 // A refill starting exactly where the previous successful read ended, with
 // the transport held throughout, continues that read: the transport's
 // prefetched word replaces the warm-up and no seek is needed. Losing the
 // grant (a new capture may use the SRAM), reset or any failure breaks it.
 reg chain_ok=0,chained=0,address_match=0,length_zero=0;reg [AW-1:0] next_address=0,start_address=0,end_address=0;
 reg fast_bank=0;reg [CACHE_AW:0] fast_length=0;
 reg [2:0] settle=0;
 // Registered copies keep the transport's read strobe off a wide fanout; data,
 // done and ready are delayed alike, so their order is unchanged.
 reg read_valid_q=0,done_q=0;reg [31:0] read_data_q=0;
 // A word beyond the request poisons the read one clock later; completion
 // waits at least SETTLE clocks, so the flag always lands first.
 reg extra_word=0;
 // Commands leave registered; losing the grant suppresses them immediately.
 assign command=command_r && transport_owned && !fast_rst;
 assign command_read=1'b1;
 assign command_discard=discard_r;
 assign command_continue=continue_r;
 assign command_count=count_r;
 // After SETTLE clocks an accepted command has made the transport busy, so
 // an idle transport without done means the command was never taken.
 // Registered: once taken, the transport stays busy far longer than a clock.
 reg never_taken=0;
 wire [CACHE_AW-1:0] next_pair_address=(fast_bank ? BANK_PAIRS:{CACHE_AW{1'b0}})|pair_index;
 always @(posedge transport_clk)begin
  command_r<=0;ready_q<=transport_ready;never_taken<=settle==0 && ready_q && !done_q;
  // States choose count_next; the transport count is its registered copy,
  // settled no later than the strobe it accompanies.
  count_r<=count_next;
  read_valid_q<=transport_read_valid;read_data_q<=transport_read_data;done_q<=transport_done;
  if(settle!=0)settle<=settle-1'b1;
  // Datapath loads carry no state decode. half always holds the previous
  // word. The pair payload follows {data, half} while a half is pending and
  // freezes on the clock the pair completes (or the final odd half drains);
  // only the gated toggle publishes it, and clk samples it on that toggle
  // before it can change again. A final odd pair's upper half lies past the
  // record end, which requests never reach. The counter is sampled every
  // clock; the copy used is the one taken as TARGET leaves an idle transport.
  extra_word<=0;
  if(extra_word)fail<=1;
  if(read_valid_q)half<=read_data_q;
  if(half_valid)begin pair_data<={read_data_q,half};pair_address<=next_pair_address;end
  idle_position<=position;
  if(fast_rst)begin
   fstate<=F_IDLE;request_seen<=0;done_toggle<=0;done_error<=0;fail<=0;half_valid<=0;pair_toggle<=0;chain_ok<=0;
  end else begin
   if(!transport_owned && fstate!=F_IDLE)fail<=1;
   if(!transport_owned)chain_ok<=0;
   case(fstate)
    F_IDLE:if(request_toggle!=request_seen)begin
     request_seen<=request_toggle;fail<=0;
     fast_bank<=refill_bank;fast_length<=refill_length;start_address<=refill_address;fstate<=F_MATCH;
    end
    // The request lands in local registers first; compare and subtract here.
    // Chain decision over two clocks: compare, then combine, then branch.
    F_MATCH:begin
     address_match<=start_address==next_address;length_zero<=fast_length==0;
     target<=start_address-WARM_ADDRESS;end_address<=start_address+{{(AW-CACHE_AW-1){1'b0}},fast_length};fstate<=F_TARGET;
    end
    // The counter moves only for this reader's commands while owned, so it
    // is sampled once the transport has been idle for a clock.
    F_TARGET:begin
     read_total<={{(AW-CACHE_AW){1'b0}},fast_length}+WARM_COUNT;
     chained<=chain_ok && address_match;continue_r<=chain_ok && address_match;
     // Speculative: a chained read uses these as-is; a seek overwrites them.
     count_next<={{(AW-CACHE_AW){1'b0}},fast_length};discard_r<=0;fstate<=F_CHOOSE;
    end
    F_CHOOSE:begin
     if(fail)fstate<=F_FINISH;
     else if(chained)fstate<=F_READ;
     else if(ready_q)fstate<=F_DISTANCE;
    end
    F_DISTANCE:begin
     {borrow,distance[LO-1:0]}<={1'b0,target[LO-1:0]}-{1'b0,idle_position[LO-1:0]};
     position_high<=idle_position[AW-1:LO];fstate<=F_DIST_HIGH;
    end
    F_DIST_HIGH:begin
     distance[AW-1:LO]<=target[AW-1:LO]-position_high-borrow;low_zero<=distance[LO-1:0]==0;fstate<=F_ZERO;
    end
    F_ZERO:begin count_next<={1'b0,distance};discard_r<=1;distance_zero<=low_zero && distance[AW-1:LO]==0;fstate<=F_SEEK;end
    // A zero distance needs no seek: switch the settled count to the read.
    F_SEEK:if(fail)fstate<=F_FINISH;
     else if(distance_zero)begin count_next<=read_total;discard_r<=0;fstate<=F_READ;end
     else begin command_r<=1;settle<=SETTLE;never_taken<=0;fstate<=F_SEEK_WAIT;end
    F_SEEK_WAIT:if(done_q)begin
      count_next<=read_total;discard_r<=0;fstate<=fail ? F_FINISH : F_READ;
     end
     else if(never_taken)begin fail<=1;fstate<=F_FINISH;end
    // Here fail can only mean a lost grant, which suppresses the command;
    // the never-taken check in F_READING then finishes with the error.
    F_READ:if(ready_q)begin
     command_r<=1;settle<=SETTLE;never_taken<=0;
     words_left<=fast_length;warm_left<=chained ? {WW{1'b0}} : WARM_WORDS;warm_done<=chained || WARM_WORDS==0;words_done<=length_zero;
     pair_index<=0;half_valid<=0;fstate<=F_READING;
    end
    F_READING:begin
     if(read_valid_q)begin
      if(!warm_done)begin warm_left<=warm_left-1'b1;warm_done<=warm_left==1;end
      else if(words_done)extra_word<=1;
      else begin
       words_left<=words_left-1'b1;words_done<=words_left==1;
       if(!half_valid)half_valid<=1;
       else begin pair_toggle<=!pair_toggle;pair_index<=pair_index+1'b1;half_valid<=0;end
      end
     end
     if(done_q)begin
      if(!words_done || !warm_done || read_valid_q)fail<=1;
      fstate<=F_DRAIN;
     end else if(never_taken)begin fail<=1;fstate<=F_FINISH;end
    end
    // An odd final word is published with a zero upper half; transport
    // drain separates it by several clocks from the previous pair.
    F_DRAIN:begin
     if(read_valid_q)extra_word<=1;
     if(half_valid)begin pair_toggle<=!pair_toggle;half_valid<=0;end
     settle<=SETTLE;fstate<=F_FINISH;
    end
    // Let clk consume the last pair, and the transport go idle, first.
    F_FINISH:if(settle==0 && ready_q)begin
     done_error<=fail || !transport_owned;done_toggle<=request_seen;fstate<=F_IDLE;
     chain_ok<=!fail && transport_owned;next_address<=end_address;
    end
    default:begin fail<=1;fstate<=F_FINISH;end
   endcase
  end
 end
endmodule
