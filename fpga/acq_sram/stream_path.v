// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-STREAM-PATH
// Slower-rate continuous SRAM acquisition path. Source words are offered in
// core_clk domain; stop source offers when source_enable falls, then assert
// source_finished after the last offer. All components share an epoch reset.
// The board top supplies phase-qualified core/sample clocks and read_bias.
// This module does not implement trigger geometry or the GPMC register ABI.
// TRLC-LINKS: REQ-SDS-044, REQ-SDS-045, REQ-SDS-046, REQ-SDS-047, REQ-SDS-048, REQ-SDS-051, REQ-SDS-052, REQ-SDS-053
module sram_stream_path #(parameter AW=19)(
 input wire reset,locked,core_clk,sample_clk,ram_clk,host_clk,
 input wire start,stop,source_finished,input wire [AW-1:0] read_bias,
 input wire source_valid,source_fault,input wire [35:0] source_data,
 output wire source_ready,start_ready,source_enable,
 output wire active,done,capture_done,fault,output wire [3:0] error_code,
 output wire [63:0] committed,read_ordinal,output wire [AW:0] unread,
 output wire [1:0] bank_busy,
 input wire host_release,release_bank,release_token,
 output wire host_fault,output wire [1:0] host_ready,host_token,
 output wire [63:0] host_first0,host_first1,
 output wire [11:0] host_words0,host_words1,
 input wire read_enable,input wire [13:0] read_halfword,
 output wire read_valid,read_error,output wire [15:0] read_data,
 inout wire [31:0] dq,output wire k1,k2,g1
);
 wire transport_ready,transport_write_ready,transport_done,transport_read_valid;
 wire [31:0] transport_read_data;wire [AW-1:0] position;
 wire command,command_read,command_discard,command_continue,write_valid,write_stop;
 wire [AW:0] command_count;wire [31:0] write_data;
 wire host_core_fault;wire [1:0] bank_release,bank_done;
 wire [63:0] first0,first1;wire [AW:0] words0,words1;
 wire [19:0] words0_host=words0,words1_host=words1;
 wire word_valid,word_bank;wire [31:0] word_data;wire [11:0] word_index;
 sram_stream_engine #(.AW(AW)) engine(
  .reset(reset),
  .core_clk(core_clk),
  .ram_clk(ram_clk),
  .start(start),
  .stop(stop),
  .source_finished(source_finished),
  .read_bias(read_bias),
  .source_valid(source_valid),.source_fault(source_fault),
  .source_data(source_data),
  .source_ready(source_ready),
  .start_ready(start_ready),
  .source_enable(source_enable),
  .active(active),
  .done(done),
  .capture_done(capture_done),
  .fault(fault),
  .error_code(error_code),
  .committed(committed),
  .read_ordinal(read_ordinal),
  .unread(unread),
  .bank_busy(bank_busy),
  .host_core_fault(host_core_fault),
  .bank_release(bank_release),
  .bank_done(bank_done),
  .first0(first0),
  .first1(first1),
  .words0(words0),
  .words1(words1),
  .word_valid(word_valid),
  .word_bank(word_bank),
  .word_data(word_data),
  .word_index(word_index),
  .transport_ready(transport_ready),
  .transport_write_ready(transport_write_ready),
  .transport_done(transport_done),
  .transport_read_valid(transport_read_valid),
  .transport_read_data(transport_read_data),
  .position(position),
  .command(command),
  .command_read(command_read),
  .command_discard(command_discard),
  .command_continue(command_continue),
  .write_valid(write_valid),
  .write_stop(write_stop),
  .command_count(command_count),
  .write_data(write_data));
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1)) transport(
  .clk(core_clk),.sample_clk(sample_clk),.reset(reset),.locked(locked),
  .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),
  .command_count(command_count),.ready(transport_ready),.write_data(write_data),.write_valid(write_valid),.write_stop(write_stop),
  .write_ready(transport_write_ready),.read_data(transport_read_data),.read_valid(transport_read_valid),.done(transport_done),
  .position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 sram_host_path host(reset,core_clk,ram_clk,host_clk,word_valid,word_bank,word_data,word_index,
  bank_done,first0,first1,words0_host,words1_host,host_core_fault,host_fault,bank_release,
  host_release,release_bank,release_token,host_ready,host_token,host_first0,host_first1,host_words0,host_words1,
  read_enable,read_halfword,read_valid,read_error,read_data);
endmodule
