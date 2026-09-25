// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-INGRESS-PATH
// Reduced-rate acquisition ingress: core -> RAM clock -> core SRAM writer.
// Source samples cannot be retried: a word offered without source_ready latches
// fault. Intended for /256 or slower, never the raw ADC word stream.
// WIDTH=36 leaves four marker bits alongside the Q8.8 channel pair without
// increasing the nine-bank M9K allocation. The SRAM payload itself stays 32 bits.
// pending is in core's domain and includes both mailboxes and RAM prefetch slots.
// TRLC-LINKS: REQ-SDS-046
module sram_ingress_path #(parameter WIDTH=36,BANKS=9,ROWS=512)(
 input wire reset,core,ram_clk,
 input wire source_valid,input wire [WIDTH-1:0] source_data,
 output wire source_ready,
 input wire ready,output wire valid,output wire [WIDTH-1:0] data_out,
 output reg [$clog2(BANKS*ROWS+11)-1:0] pending=0,
 output reg fault=0
);
 (* async_reg="true" *) reg [1:0] core_reset=3,ram_reset=3;
 always @(posedge core or posedge reset)
  if(reset)core_reset<=3;else core_reset<={core_reset[0],1'b0};
 always @(posedge ram_clk or posedge reset)
  if(reset)ram_reset<=3;else ram_reset<={ram_reset[0],1'b0};
 wire input_ready,input_valid,queue_ready,queue_valid,overflow,underflow;
 wire [WIDTH-1:0] input_data,queue_data;
 wire [$clog2(BANKS*ROWS+9)-1:0] queue_count;
 assign source_ready=input_ready && !fault && !core_reset[1];
 sram_word_bridge #(.WIDTH(WIDTH)) incoming(
  .reset(reset),.source_clk(core),.dest_clk(ram_clk),
  .source_valid(source_valid && !fault),.source_data(source_data),.source_ready(input_ready),
  .dest_valid(input_valid),.dest_data(input_data),.dest_ready(1'b1));
 sram_ingress_stream #(.WIDTH(WIDTH),.BANKS(BANKS),.ROWS(ROWS)) queue(
  .clk(ram_clk),.reset(ram_reset[1]),.push(input_valid),.data_in(input_data),
  .ready(queue_ready),.valid(queue_valid),.data_out(queue_data),.count(queue_count),
  .overflow(overflow),.underflow(underflow));
 sram_word_bridge #(.WIDTH(WIDTH)) outgoing(
  .reset(reset),.source_clk(ram_clk),.dest_clk(core),
  .source_valid(queue_valid),.source_data(queue_data),.source_ready(queue_ready),
  .dest_valid(valid),.dest_data(data_out),.dest_ready(ready));
 wire accepted=source_valid && source_ready,delivered=valid && ready;
 (* async_reg="true" *) reg [2:0] queue_fault_sync=0;
 always @(posedge core or posedge core_reset[1])begin
  if(core_reset[1])begin queue_fault_sync<=0;pending<=0;fault<=0;end
  else begin
   queue_fault_sync<={queue_fault_sync[1:0],overflow || underflow};
   if(queue_fault_sync[2] || (source_valid && !source_ready))fault<=1;
   // Direction depends only on the sink. Source handshake affects the
   // enable, avoiding two adders plus a source-ready-dependent result mux.
   if(accepted!=delivered)pending<=pending+(delivered ? {$clog2(BANKS*ROWS+11){1'b1}} : 1'b1);
  end
 end
endmodule
