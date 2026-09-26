// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Retained-page transfer: 250 MHz ordered words -> 125 MHz paired RAM writes.
// Ordinals are bounded to the 19-bit physical retained-record word range.
// Oversized metadata faults instead of truncating.
// Reuses the host packet format (bank 1 starts at pair address 1280). A cache
// maps that address to its own page bank and rejects addresses beyond its size.
// Writes are non-backpressurable. The caller reserves banks before input and
// waits for core_release before reuse. Completion follows the final word.
// Common reset abandons the epoch in both domains. CDC timing constraints are
// required at integration. This module provides no SRAM command arbitration.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-050, REQ-SDS-078
module stack_page_transfer(
 input wire reset,core_clk,memory_clk,
 input wire word_valid,word_bank,input wire [31:0] word_data,
 input wire [11:0] word_index,input wire [1:0] bank_done,
 input wire [63:0] first0,first1,input wire [19:0] words0,words1,
 output wire core_fault,output reg [1:0] core_release=0,
 output wire memory_write,output wire [11:0] memory_pair,
 output wire [63:0] memory_data,input wire memory_fault,
 output wire page_fault,output wire [1:0] page_ready,
 output wire [63:0] page_first0,page_first1,
 output wire [11:0] page_words0,page_words1,
 input wire [1:0] page_release
);
 (* async_reg="true" *) reg [1:0] core_reset=3,memory_reset=3;
 always @(posedge core_clk or posedge reset)
  if(reset)core_reset<=3;else core_reset<={core_reset[0],1'b0};
 always @(posedge memory_clk or posedge reset)
  if(reset)memory_reset<=3;else memory_reset<={memory_reset[0],1'b0};
 wire push,pack_fault,fifo_ready,overflow,valid,ready,sink_fault;
 wire [79:0] packet,data;
 wire [1:0] publish;
 reg [1:0] occupied=0,release_toggle=0;
 reg ownership_fault=0;
 (* async_reg="true",preserve *) reg [1:0] source_bad_memory=0,memory_bad_core=0;
 (* async_reg="true",preserve *) reg [1:0] release_meta=0,release_sync=0;
 reg [1:0] release_seen=0;
 wire source_bad=pack_fault || overflow;
 wire owned_packet=valid && (occupied[data[78]] || publish[data[78]]);
 assign page_fault=sink_fault || ownership_fault || memory_fault || source_bad_memory[1];
 assign core_fault=source_bad || memory_bad_core[1];
 assign page_ready=occupied & {2{!page_fault && !reset && !memory_reset[1]}};
 always @(posedge memory_clk or posedge memory_reset[1])begin
  if(memory_reset[1])begin
   source_bad_memory<=0;occupied<=0;release_toggle<=0;ownership_fault<=0;
  end else begin
   source_bad_memory<={source_bad_memory[0],source_bad};
   if(|(page_release & ~page_ready) || |(publish & occupied))ownership_fault<=1;
   if(!page_fault)begin
    occupied<=(occupied | publish) & ~page_release;
    release_toggle<=release_toggle ^ (page_release & page_ready);
   end
  end
 end
 always @(posedge core_clk or posedge core_reset[1])begin
  if(core_reset[1])begin memory_bad_core<=0;release_meta<=0;release_sync<=0;release_seen<=0;core_release<=0;end
  else begin
   memory_bad_core<={memory_bad_core[0],page_fault};
   release_meta<=release_toggle;release_sync<=release_meta;release_seen<=release_sync;
   core_release<=release_sync ^ release_seen;
  end
 end
 sram_host_packer #(.ORDINAL_BITS(19)) pack(core_clk,core_reset[1],word_valid,word_bank,word_data,word_index,
  bank_done,first0,first1,words0,words1,push,packet,pack_fault);
 (* preserve *) reg [79:0] fifo_packet=0;
 reg fifo_push=0;
 always @(posedge core_clk)begin
  if(core_reset[1])begin fifo_push<=0;fifo_packet<=0;end
  else begin fifo_push<=push;fifo_packet<=packet;end
 end
 sram_host_fifo fifo(reset,core_clk,memory_clk,fifo_push,fifo_packet,fifo_ready,overflow,valid,data,ready);
 sram_host_sink #(.ORDINAL_BITS(19)) sink(memory_clk,memory_reset[1],source_bad_memory[1] || ownership_fault || owned_packet,
  valid,data,ready,memory_write,memory_pair,memory_data,memory_fault,sink_fault,publish,
  page_first0,page_first1,page_words0,page_words1);
endmodule
