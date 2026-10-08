// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Acquisition-domain event input to stable, retryable host halfword readout.
// reset is a common epoch boundary. Overflow is represented in-band by loss
// records; source_overflow is diagnostic and must be synchronized if read by host.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
module decoded_event_transport #(parameter AW=4,HOST_AW=0,VALUE_BITS=32,COUNT_BITS=32)(
 input wire reset,source_clk,host_clk,
 input wire [31:0] epoch,
 input wire in_valid,input wire [63:0] in_sample,
 input wire [7:0] in_kind,in_protocol,in_flags,
 input wire [31:0] in_record,in_value,in_count,
 input wire host_pop,host_rewind,host_clear_error,
 output wire host_available,host_error,
 output wire [3:0] host_cursor,output wire [15:0] host_data,
 output wire source_overflow,output wire [15:0] host_count
);
 (* async_reg="true" *) reg [1:0] source_reset=3,host_reset=3;
 always @(posedge source_clk or posedge reset)
  if(reset)source_reset<=3;else source_reset<={source_reset[0],1'b0};
 always @(posedge host_clk or posedge reset)
  if(reset)host_reset<=3;else host_reset<={host_reset[0],1'b0};
 wire queue_valid,queue_ready,event_valid,event_ready;
 wire [255:0] queue_data,event_data;
 // Separate RAM/forwarding/loss reconstruction from the CDC mailbox enable.
 // This one-entry elastic stage keeps the complete ABI payload and order.
 reg staged_valid=0;
 reg [255:0] staged_data=0;
 wire bridge_ready;
 assign queue_ready=!staged_valid || bridge_ready;
 always @(posedge source_clk)begin
  if(source_reset[1])staged_valid<=0;
  else if(queue_ready)begin
   staged_valid<=queue_valid;
   if(queue_valid)staged_data<=queue_data;
  end
 end
 decoded_event_queue #(.AW(AW),.VALUE_BITS(VALUE_BITS),.COUNT_BITS(COUNT_BITS)) queue(
  .clk(source_clk),.reset(source_reset[1]),.epoch(epoch),
  .in_valid(in_valid),.in_sample(in_sample),.in_kind(in_kind),.in_protocol(in_protocol),
  .in_flags(in_flags),.in_record(in_record),.in_value(in_value),.in_count(in_count),
  .out_valid(queue_valid),.out_ready(queue_ready),.out_data(queue_data),.overflow(source_overflow));
 decoded_event_bridge bridge(
  .reset(reset),.source_clk(source_clk),.host_clk(host_clk),
  .source_valid(staged_valid),.source_data(staged_data),.source_ready(bridge_ready),
  .host_valid(event_valid),.host_data(event_data),.host_ready(event_ready));
 decoded_event_reader #(.AW(HOST_AW),.COMPACT_IDENTITY(1)) reader(
  .clk(host_clk),.reset(host_reset[1]),.event_valid(event_valid),.event_data(event_data),
  .event_ready(event_ready),.pop(host_pop),.rewind(host_rewind),.clear_error(host_clear_error),
  .available(host_available),.cursor(host_cursor),.data(host_data),.error(host_error),.count(host_count));
endmodule
