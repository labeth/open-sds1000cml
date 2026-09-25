// Resource probe only: no deployable image or protocol implementation.
module event_budget(input wire reset,source_clk,host_clk,input wire [31:0] epoch,
 input wire in_valid,input wire [63:0] in_sample,input wire [2:0] in_kind,
 input wire [3:0] in_protocol,input wire [2:0] in_flags,
 input wire [31:0] value,input wire [6:0] count,
 input wire host_pop,host_rewind,host_clear_error,
 output wire host_available,host_error,output wire [3:0] host_cursor,
 output wire [15:0] host_data,host_count,output wire source_overflow);
 decoded_event_transport #(.AW(10),.HOST_AW(9)) events(
 .reset(reset),.source_clk(source_clk),.host_clk(host_clk),.epoch(epoch),
 .in_valid(in_valid),.in_sample(in_sample),.in_kind({5'd0,in_kind}),
 .in_protocol({4'd0,in_protocol}),.in_flags({5'd0,in_flags}),.in_record(32'd0),
 .in_value(value),.in_count({25'd0,count}),
 .host_pop(host_pop),.host_rewind(host_rewind),.host_clear_error(host_clear_error),
 .host_available(host_available),.host_error(host_error),.host_cursor(host_cursor),
 .host_data(host_data),.host_count(host_count),.source_overflow(source_overflow));
endmodule
