// Shared profile composition with the actual GPMC slave and acquisition core.
// Board wrapper supplies qualified PLL clocks, reset, lane/pin assignments.
// This composition is experimental until real board fit/CDC/device checks pass.
module acq_profile_core #(parameter ENABLE_STREAM=0,BUILD_ID=32'b0)(
 input wire reset,locked,core_clk,sample_clk,ram_clk,host_clk,clk100,
 input wire nCS1,nOE,nWE,input wire [6:0] sel,
 inout wire [15:0] gpmc_d,input wire [79:0] lane,
 inout wire [31:0] dq,output wire k1,k2,g1,
 output wire [4:0] enc_p,enc_n
);
 wire host_write,host_pop;wire [7:0] write_sel,read_sel;wire [15:0] write_data;
 wire command_hit,window_hit;wire [15:0] command_data,window_data;
 reg [15:0] bus_data;
 gpmc_slave #(.QUALIFIED_READ(1)) bus_slave(
  .clk(host_clk),.nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel(sel),.gpmc_d(gpmc_d),
  .we_commit(host_write),.wr_sel(write_sel),.wr_data(write_data),.wr_aux(),
  .rd_pop(host_pop),.rd_sel(read_sel),.rdata(bus_data),.drive_active());
 wire core_start,core_halt,core_force,core_snapshot,core_error;
 wire [1:0] operation,trigger_mode;
 wire [19:0] pre_count,post_count,offset,length;
 wire [18:0] read_bias;wire [4:0] decim_log;wire [9:0] encode_enable;
 wire trigger_channel,trigger_falling;wire [15:0] trigger_level;
 wire start_ready,active,done,fault,record_frozen,triggered,trigger_second;
 wire precision_mode,adc_locked,acquisition_request_error;
 wire [18:0] record_start;wire [19:0] record_words,trigger_index;
 wire host_fault;wire [1:0] host_ready,host_token;
 wire [11:0] host_words0,host_words1;wire [63:0] host_first0,host_first1;
 wire host_release,release_bank,release_token;
 wire ram_read_enable,ram_read_valid,ram_read_error;
 wire [13:0] ram_read_halfword;wire [15:0] ram_read_data;
 (* async_reg="true" *) reg [1:0] acquisition_fault_host=0;
 always @(posedge host_clk)begin
  if(reset)acquisition_fault_host<=0;
  else acquisition_fault_host<={acquisition_fault_host[0],fault};
 end
 wire readout_fault=host_fault || acquisition_fault_host[1];
 wire [31:0] status_flags={21'b0,locked,host_fault,adc_locked,precision_mode,
  trigger_second,triggered,record_frozen,fault,done,active,start_ready};
 wire [127:0] acquisition_status={{13'b0,record_start},{12'b0,trigger_index},
  {12'b0,record_words},status_flags};
 // No raw ADC snapshot service in this base composition. Reject its opcode
 // explicitly until an adapter can return and arbitrate the 80-bit snapshot.
 acq_command_port #(.ENABLE_STREAM(ENABLE_STREAM),.ENABLE_SNAPSHOT(0),.ENABLE_MATCH(0)) commands(
  .reset(reset),.host_clk(host_clk),.core_clk(core_clk),.host_write(host_write),
  .write_sel(write_sel),.read_sel(read_sel),.write_data(write_data),
  .read_hit(command_hit),.read_data(command_data),.host_busy(),.host_rejected(),
  .core_start(core_start),.core_halt(core_halt),.core_force(core_force),
  .core_snapshot(core_snapshot),.core_error(core_error),.operation(operation),
  .pre_count(pre_count),.post_count(post_count),.offset(offset),.length(length),
  .read_bias(read_bias),.decim_log(decim_log),.encode_enable(encode_enable),
  .trigger_mode(trigger_mode),.trigger_channel(trigger_channel),.trigger_falling(trigger_falling),
  .trigger_level(trigger_level),.acquisition_request_error(acquisition_request_error),
  .acquisition_status(acquisition_status));
 acq_host_read_port readout(
  .clk(host_clk),.reset(reset),.host_write(host_write),.host_pop(host_pop),
  .write_sel(write_sel),.read_sel(read_sel),.write_data(write_data),
  .read_hit(window_hit),.read_data(window_data),.host_ready(host_ready),.host_token(host_token),
  .host_fault(readout_fault),.host_words0(host_words0),.host_words1(host_words1),
  .host_first0(host_first0),.host_first1(host_first1),.host_release(host_release),
  .release_bank(release_bank),.release_token(release_token),.ram_read_enable(ram_read_enable),
  .ram_read_halfword(ram_read_halfword),.ram_read_valid(ram_read_valid),
  .ram_read_error(ram_read_error),.ram_read_data(ram_read_data));
 sram_acquisition_path #(.ENABLE_STREAM(ENABLE_STREAM)) acquisition(
  .reset(reset),.locked(locked),.core_clk(core_clk),.sample_clk(sample_clk),
  .ram_clk(ram_clk),.host_clk(host_clk),.clk100(clk100),
  .start(core_start),.halt(core_halt),.operation(operation),
  .pre_count(pre_count),.post_count(post_count),.offset(offset),.length(length),
  .read_bias(read_bias),.decim_log(decim_log),.encode_enable(encode_enable),
  .trigger_mode(trigger_mode),.trigger_channel(trigger_channel),.trigger_falling(trigger_falling),
  .trigger_level(trigger_level),.force_trigger(core_force),.match_trigger(1'b0),
  .lane(lane),.snapshot_request(1'b0),.snapshot_ack(),.snapshot(),.enc_p(enc_p),.enc_n(enc_n),
  .adc_locked(adc_locked),.source_word(),.source_valid(),.precision_mode(precision_mode),
  .start_ready(start_ready),.active(active),.done(done),.fault(fault),.record_frozen(record_frozen),
  .triggered(triggered),.request_error(acquisition_request_error),.trigger_second(trigger_second),
  .record_start(record_start),.record_words(record_words),.trigger_index(trigger_index),
  .committed(),.read_ordinal(),.unread(),.bank_busy(),.host_release(host_release),
  .release_bank(release_bank),.release_token(release_token),.host_fault(host_fault),
  .host_ready(host_ready),.host_token(host_token),.host_first0(host_first0),.host_first1(host_first1),
  .host_words0(host_words0),.host_words1(host_words1),.read_enable(ram_read_enable),
  .read_halfword(ram_read_halfword),.read_valid(ram_read_valid),.read_error(ram_read_error),
  .read_data(ram_read_data),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 always @* begin
  bus_data=16'hffff;
  if(command_hit)bus_data=command_data;
  else if(window_hit)bus_data=window_data;
  else case(read_sel)
   8'h00:bus_data=16'hacd2;
   8'h01:bus_data=1; // profile ABI, distinct from legacy rev11
   8'h02:bus_data=ENABLE_STREAM ? 0 : 1;
   8'h03:bus_data=16'h000b | (ENABLE_STREAM ? 16'h0004 : 16'h0000);
   8'h04:bus_data=5120;8'h05:bus_data=2560;
   8'h06:bus_data=0;8'h07:bus_data=8; // SRAM depth 0x80000 words
   8'h08:bus_data=3; // raw and Q8.8 sample formats
   8'h09:bus_data=100; // ADC encode MHz
   8'h0a:bus_data=0; // hardware qualification flag remains false
   8'h0b:bus_data=BUILD_ID[15:0];8'h0c:bus_data=BUILD_ID[31:16];
  endcase
 end
endmodule
