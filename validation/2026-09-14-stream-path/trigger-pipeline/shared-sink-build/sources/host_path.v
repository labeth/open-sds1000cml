// Controller-facing host storage: core 250 MHz, RAM 125 MHz, ARM 100 MHz.
// Common reset abandons all data/descriptors. Caller reserves each bank before
// emitting words and reuses it only after core_release. Completion follows the
// final word, never on the same edge. Host reads only a ready bank and releases
// after its final read response. Clock-crossing constraints still required.
module sram_host_path(
 input wire reset,core_clk,ram_clk,host_clk,
 input wire word_valid,word_bank,input wire [31:0] word_data,
 input wire [11:0] word_index,input wire [1:0] bank_done,
 input wire [63:0] first0,first1,input wire [19:0] words0,words1,
 output wire core_fault,host_fault,output wire [1:0] core_release,
 input wire host_release,release_bank,release_token,
 output wire [1:0] host_ready,host_token,
 output wire [63:0] host_first0,host_first1,
 output wire [11:0] host_words0,host_words1,
 input wire read_enable,input wire [13:0] read_halfword,
 output wire read_valid,read_error,output wire [15:0] read_data
);
 wire push,pack_fault,fifo_ready,overflow,fifo_valid,sink_ready;
 wire [79:0] packet,fifo_data;
 wire ram_write,ram_fault,sink_fault,ownership_fault;
 wire [11:0] ram_pair,count0,count1;
 wire [63:0] ram_data,base0,base1;wire [1:0] publish,busy;
 (* async_reg="true" *) reg [1:0] ram_reset=3,core_reset=3,host_reset=3;
 always @(posedge ram_clk or posedge reset)if(reset)ram_reset<=3;else ram_reset<={ram_reset[0],1'b0};
 always @(posedge core_clk or posedge reset)if(reset)core_reset<=3;else core_reset<={core_reset[0],1'b0};
 always @(posedge host_clk or posedge reset)if(reset)host_reset<=3;else host_reset<={host_reset[0],1'b0};
 (* async_reg="true",preserve *) reg [1:0] source_bad_ram=0,ram_bad_core=0,bad_host=0;
 wire source_bad=pack_fault || overflow;
 // publish fills the one-cycle gap before ownership captures its token.
 wire owned_packet=fifo_valid && (busy[fifo_data[78]] || publish[fifo_data[78]]);
 wire ram_bad=sink_fault || ram_fault || ownership_fault;
 assign core_fault=source_bad || ram_bad_core[1];
 assign host_fault=bad_host[1];
 always @(posedge ram_clk or posedge ram_reset[1])
  if(ram_reset[1])source_bad_ram<=0;else source_bad_ram<={source_bad_ram[0],source_bad};
 always @(posedge core_clk or posedge core_reset[1])
  if(core_reset[1])ram_bad_core<=0;else ram_bad_core<={ram_bad_core[0],ram_bad};
 always @(posedge host_clk or posedge host_reset[1])
  if(host_reset[1])bad_host<=0;else bad_host<={bad_host[0],core_fault};
 sram_host_packer pack(core_clk,core_reset[1],word_valid,word_bank,word_data,word_index,
  bank_done,first0,first1,words0,words1,push,packet,pack_fault);
 // Keep the FIFO's distributed write enables off the packet-selection path.
 (* preserve *) reg [79:0] fifo_packet=0;
 reg fifo_push=0;
 always @(posedge core_clk)begin
  if(core_reset[1])begin fifo_push<=0;fifo_packet<=0;end
  else begin fifo_push<=push;fifo_packet<=packet;end
 end
 sram_host_fifo fifo(reset,core_clk,ram_clk,fifo_push,fifo_packet,fifo_ready,overflow,fifo_valid,fifo_data,sink_ready);
 sram_host_sink sink(ram_clk,ram_reset[1],source_bad_ram[1] || owned_packet,
  fifo_valid,fifo_data,sink_ready,ram_write,ram_pair,ram_data,ram_fault,sink_fault,publish,base0,base1,count0,count1);
 sram_host_ram ram(ram_clk,ram_reset[1],ram_write,ram_pair,ram_data,ram_fault,
  host_clk,host_reset[1],read_enable,read_halfword,read_valid,read_error,read_data);
 sram_host_ownership ownership(reset,ram_clk,host_clk,core_clk,publish,base0,base1,count0,count1,
  ownership_fault,busy,host_release,release_bank,release_token,host_ready,host_token,
  host_first0,host_first1,host_words0,host_words1,core_release);
endmodule
