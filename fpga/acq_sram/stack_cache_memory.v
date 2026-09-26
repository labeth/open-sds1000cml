// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two cache pages in 125 MHz M9K storage, filled from the 250 MHz recall path.
// Only the owning cache controller may issue requests: invalidate a victim tag
// before refill, and read only after core_release acknowledges publication.
// Common reset invalidates both domains and all cached data. CDC route bounds
// and shared-reset assertions must be checked in the integrated image.
// CACHE_AW is 1..11; each page contains 2**CACHE_AW raw 32-bit SRAM words.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043, REQ-SDS-050, REQ-SDS-078
module stack_cache_memory #(parameter CACHE_AW=8)(
 input wire reset,core_clk,memory_clk,
 input wire word_valid,word_bank,input wire [31:0] word_data,
 input wire [11:0] word_index,input wire [1:0] bank_done,
 input wire [63:0] first0,first1,input wire [19:0] words0,words1,
 output wire core_fault,output wire [1:0] core_release,output wire read_fault,
 input wire read_valid,output wire read_ready,input wire [CACHE_AW:0] read_word,
 output reg response_valid=0,input wire response_ready,output reg [31:0] response_data=0
);
 localparam PAGE_WORDS=1<<CACHE_AW,PAGE_PAIRS=PAGE_WORDS/2;
 (* async_reg="true" *) reg [1:0] memory_reset=3;
 always @(posedge memory_clk or posedge reset)
  if(reset)memory_reset<=3;else memory_reset<={memory_reset[0],1'b0};
 wire memory_write,page_fault;wire [11:0] memory_pair,page_words0,page_words1;
 wire [63:0] memory_data;wire [1:0] page_ready,page_release;
 reg memory_fault=0;
 wire write_bank=memory_pair>=1280;
 wire [11:0] local_pair=memory_pair-(write_bank ? 12'd1280:12'd0);
 wire write_legal=local_pair<PAGE_PAIRS;
 wire [CACHE_AW-1:0] write_address=(write_bank ? PAGE_PAIRS:0)+local_pair;
 wire [1:0] descriptor_legal={page_words1!=0 && page_words1<=PAGE_WORDS,
                              page_words0!=0 && page_words0<=PAGE_WORDS};
 assign page_release=page_ready & descriptor_legal & {2{!memory_fault}};
 always @(posedge memory_clk or posedge memory_reset[1])begin
  if(memory_reset[1])memory_fault<=0;
  else if((memory_write && !write_legal) || |(page_ready & ~descriptor_legal))memory_fault<=1;
 end
 stack_page_transfer transfer(reset,core_clk,memory_clk,word_valid,word_bank,word_data,word_index,
  bank_done,first0,first1,words0,words1,core_fault,core_release,
  memory_write,memory_pair,memory_data,memory_fault,page_fault,page_ready,
  , ,page_words0,page_words1,page_release);

 // Cache-hit reads are local to the 125 MHz memory/numerical clock.
 // The controller owns the page and serializes reads with refills.
 assign read_fault=page_fault;
 reg [1:0] read_state=0;
 reg [CACHE_AW:0] memory_word=0;
 assign read_ready=!reset && !memory_reset[1] && !page_fault && read_state==0 && !response_valid;
 (* ramstyle="M9K" *) reg [63:0] memory[0:PAGE_WORDS-1];
 reg [63:0] fetched=0;
 always @(posedge memory_clk)begin
  if(memory_write && write_legal && !memory_reset[1] && !page_fault)
   memory[write_address]<=memory_data;
  if(read_state==1)fetched<=memory[memory_word[CACHE_AW:1]];
 end
 always @(posedge memory_clk or posedge memory_reset[1])begin
  if(memory_reset[1])begin read_state<=0;response_valid<=0;response_data<=0;end
  else begin
   if(response_valid && response_ready)response_valid<=0;
   case(read_state)
    0:if(read_valid && read_ready)begin memory_word<=read_word;read_state<=1;end
    1:read_state<=2;
    2:begin
     response_data<=memory_word[0] ? fetched[63:32]:fetched[31:0];
     response_valid<=!page_fault;read_state<=0;
    end
   endcase
   if(page_fault)response_valid<=0;
  end
 end
endmodule
