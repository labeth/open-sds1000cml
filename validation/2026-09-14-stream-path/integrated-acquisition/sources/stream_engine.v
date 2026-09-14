// Continuous acquisition engine with an external SRAM transport. The board
// integration can arbitrate this command/data interface with finite capture
// and recall, using one physical SRAM bus owner. Grant this engine exclusive
// transport access for the whole acquisition and drain; reset is a common
// epoch boundary for ingress and host ownership. The host buffer is external.
// Reserve/release belongs to the selected producer; never change producers
// while any bank is reserved, pending publication, or owned by the host.
// Source and host contracts are the same as sram_stream_path.
// Trigger geometry and the GPMC register ABI are supplied by the board top.
module sram_stream_engine #(parameter AW=19)(
 input wire reset,core_clk,ram_clk,
 input wire start,stop,source_finished,input wire [AW-1:0] read_bias,
 input wire source_valid,source_fault,input wire [35:0] source_data,
 output wire source_ready,start_ready,source_enable,
 output wire active,done,capture_done,fault,output wire [3:0] error_code,
 output wire [63:0] committed,read_ordinal,output wire [AW:0] unread,
 output wire [1:0] bank_busy,
 input wire host_core_fault,input wire [1:0] bank_release,
 output wire word_valid,word_bank,output wire [31:0] word_data,
 output wire [11:0] word_index,output wire [1:0] bank_done,
 output wire [63:0] first0,first1,output wire [AW:0] words0,words1,
 input wire transport_ready,transport_write_ready,transport_done,transport_read_valid,
 input wire [31:0] transport_read_data,input wire [AW-1:0] position,
 output wire command,command_read,command_discard,command_continue,write_valid,write_stop,
 output wire [AW:0] command_count,output wire [31:0] write_data
);
 wire controller_fault,controller_ready,controller_enable;
 reg source_failed=0;
 always @(posedge core_clk)if(reset)source_failed<=0;else if(source_fault)source_failed<=1;
 assign fault=controller_fault || source_failed;
 assign start_ready=controller_ready && !source_failed && !source_fault;
 assign source_enable=controller_enable && !source_failed && !source_fault;
 wire [35:0] ingress_data;wire ingress_valid,ingress_ready,ingress_fault;
 wire [12:0] ingress_pending;wire [15:0] pending16=ingress_pending;
 wire [1:0] bank_begin;
 sram_ingress_path #(.BANKS(9)) ingress(.reset(reset),.core(core_clk),.ram_clk(ram_clk),
  .source_valid(source_valid),.source_data(source_data),.source_ready(source_ready),
  .ready(ingress_ready),.valid(ingress_valid),.data_out(ingress_data),
  .pending(ingress_pending),.fault(ingress_fault));
 sram_timeslice_controller #(.AW(AW),.BANK_WORDS(2560)) controller(
  .clk(core_clk),.reset(reset),.start(start),.stop(stop),.source_finished(source_finished),
  .start_ready(controller_ready),.source_enable(controller_enable),.read_bias(read_bias),
  .ingress_data(ingress_data),.ingress_valid(ingress_valid),.ingress_pending(pending16),
  .ingress_fault(ingress_fault || source_failed || source_fault),.host_fault(host_core_fault),.ingress_ready(ingress_ready),
  .active(active),.done(done),.capture_done(capture_done),.fault(controller_fault),.error_code(error_code),
  .committed(committed),.read_ordinal(read_ordinal),.unread(unread),
  .bank_release(bank_release),.bank_busy(bank_busy),.bank_begin(bank_begin),.bank_done(bank_done),
  .bank_first0(first0),.bank_first1(first1),.bank_words0(words0),.bank_words1(words1),
  .host_valid(word_valid),.host_bank(word_bank),.host_data(word_data),.host_index(word_index),
  .transport_ready(transport_ready),.transport_write_ready(transport_write_ready),.transport_done(transport_done),
  .position(position),.transport_read_valid(transport_read_valid),.transport_read_data(transport_read_data),
  .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),
  .command_count(command_count),.write_valid(write_valid),.write_stop(write_stop),.write_data(write_data));
endmodule
