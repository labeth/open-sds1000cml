// Shared acquisition/recall backend, with one physical transport and host RAM.
// Board top supplies ADC/precision, record bookkeeping and GPMC ABI.
// writer_request reserves the bus for the finite capture writer. Hold it until
// the last write drains. Grant cannot steal an active backend or owned host bank.
// Commands must only be issued with the matching ready signal. All writer ports
// are core_clk synchronous. Reset must reset both clients and transport together.
// position is never reset during handoff: record geometry uses this same origin.
module sram_board_capture_path #(parameter AW=19,CONTINUE_READS=1,READ_DELAY=0,START_VALIDATED=0)(
 input wire reset,locked,core_clk,sample_clk,ram_clk,host_clk,
 input wire start,finite_mode,stop,source_finished,
 input wire frozen,input wire [AW-1:0] record_start,read_bias,
 input wire [AW:0] record_words,offset,length,
 input wire source_valid,source_fault,input wire [35:0] source_data,
 output wire source_ready,start_ready,source_enable,
 output wire active,done,capture_done,fault,request_error,
 output wire start_rejected,selected_finite,
 output wire [3:0] error_code,output wire [63:0] committed,read_ordinal,
 output wire [AW:0] unread,output wire [1:0] bank_busy,
 input wire host_release,release_bank,release_token,
 output wire host_fault,output wire [1:0] host_ready,host_token,
 output wire [63:0] host_first0,host_first1,
 output wire [11:0] host_words0,host_words1,
 input wire read_enable,input wire [13:0] read_halfword,
 output wire read_valid,read_error,output wire [15:0] read_data,
 input wire writer_request,writer_command,writer_valid,writer_stop,
 input wire [31:0] writer_data,
 output reg writer_granted=0,
 output wire writer_ready,writer_write_ready,writer_done,
 output wire [AW-1:0] position,
 inout wire [31:0] dq,output wire k1,k2,g1
);
 wire transport_ready,transport_write_ready,transport_done,transport_read_valid;
 wire [31:0] transport_read_data;
 wire command,command_read,command_discard,command_continue,write_valid,write_stop;
 wire [AW:0] command_count;wire [31:0] write_data;
 // A waiting writer blocks new backend starts; an already active operation
 // keeps its transport access until its host banks have all been released.
 wire backend_ready=transport_ready && !writer_granted &&
                    (!writer_request || active || bank_busy!=0);
 always @(posedge core_clk)begin
  if(reset)writer_granted<=0;
  else if(!writer_granted)begin
   if(writer_request && !active && bank_busy==0 && !fault &&
      transport_ready && !command)writer_granted<=1;
  end else if(!writer_request && transport_ready && !writer_command)
   writer_granted<=0;
 end
 assign writer_ready=writer_granted && transport_ready && !reset;
 assign writer_write_ready=writer_granted && transport_write_ready && !reset;
 assign writer_done=writer_granted && transport_done;
 sram_capture_engine #(.AW(AW),.CONTINUE_READS(CONTINUE_READS),.START_VALIDATED(START_VALIDATED)) engine(
  .reset(reset),
  .core_clk(core_clk),
  .ram_clk(ram_clk),
  .host_clk(host_clk),
  .start(start),
  .finite_mode(finite_mode),
  .stop(stop),
  .source_finished(source_finished),
  .frozen(frozen),
  .record_start(record_start),
  .read_bias(read_bias),
  .record_words(record_words),
  .offset(offset),
  .length(length),
  .source_valid(source_valid),.source_fault(source_fault),
  .source_data(source_data),
  .source_ready(source_ready),
  .start_ready(start_ready),
  .source_enable(source_enable),
  .active(active),
  .done(done),
  .capture_done(capture_done),
  .fault(fault),
  .request_error(request_error),
  .start_rejected(start_rejected),
  .selected_finite(selected_finite),
  .error_code(error_code),
  .committed(committed),
  .read_ordinal(read_ordinal),
  .unread(unread),
  .bank_busy(bank_busy),
  .host_release(host_release),
  .release_bank(release_bank),
  .release_token(release_token),
  .host_fault(host_fault),
  .host_ready(host_ready),
  .host_token(host_token),
  .host_first0(host_first0),
  .host_first1(host_first1),
  .host_words0(host_words0),
  .host_words1(host_words1),
  .read_enable(read_enable),
  .read_halfword(read_halfword),
  .read_valid(read_valid),
  .read_error(read_error),
  .read_data(read_data),
  .transport_ready(backend_ready),
  .transport_write_ready(transport_write_ready && !writer_granted),
  .transport_done(transport_done && !writer_granted),
  .transport_read_valid(transport_read_valid && !writer_granted),
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
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1),.READ_DELAY(READ_DELAY)) transport(
  .clk(core_clk),.sample_clk(sample_clk),.reset(reset),.locked(locked),
  .command(writer_granted ? writer_command : command),
  .command_read(writer_granted ? 1'b0 : command_read),
  .command_discard(!writer_granted && command_discard),
  .command_continue(!writer_granted && command_continue),
  .command_count(writer_granted ? {(AW+1){1'b0}} : command_count),.ready(transport_ready),
  .write_data(writer_granted ? writer_data : write_data),
  .write_valid(writer_granted ? writer_valid : write_valid),
  .write_stop(writer_granted ? writer_stop : write_stop),.write_ready(transport_write_ready),.read_data(transport_read_data),
  .read_valid(transport_read_valid),.done(transport_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
endmodule
