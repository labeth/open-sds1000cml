// ADC -> precision -> finite/continuous SRAM capture -> shared host recall.
// All control inputs are core_clk synchronous. Host RAM ports use host_clk.
// operation: 0 finite capture, 1 continuous stream, 2 frozen finite recall.
// trigger_mode: 0 automatic, 1 edge, 2 external match on source_word/valid.
// This is the acquisition core; board PLL/pins and GPMC ABI remain external.
module sram_acquisition_path #(parameter AW=19,READ_DELAY=0,CONTINUE_READS=1)(
 input wire reset,locked,core_clk,sample_clk,ram_clk,host_clk,clk100,
 input wire start,halt,input wire [1:0] operation,
 input wire [AW:0] pre_count,post_count,offset,length,
 input wire [AW-1:0] read_bias,
 input wire [4:0] decim_log,input wire [9:0] encode_enable,
 input wire [1:0] trigger_mode,input wire trigger_channel,trigger_falling,
 input wire [15:0] trigger_level,input wire force_trigger,match_trigger,
 input wire [79:0] lane,input wire snapshot_request,
 output wire snapshot_ack,output wire [79:0] snapshot,
 output wire [4:0] enc_p,enc_n,output wire adc_locked,
 output wire [31:0] source_word,output wire source_valid,precision_mode,
 output wire start_ready,active,done,fault,record_frozen,triggered,
 output reg request_error=0,output reg trigger_second=0,
 output wire [AW-1:0] record_start,output wire [AW:0] record_words,trigger_index,
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
 wire ca,cf,ce,cr,cfr,cerr,cstart_ready;
 wire ba,bf,bd,bc,berr,brejected,bready,ben,bsready;
 wire writer_request,writer_command,writer_valid,writer_stop;
 wire writer_ready,writer_write_ready,writer_done,writer_granted;
 wire [31:0] writer_data;wire [AW-1:0] position;
 wire source_fault;wire [3:0] backend_error;wire selected_finite;
 reg [1:0] selected_operation=0,trigger_mode_l=0;
 reg trigger_channel_l=0,trigger_falling_l=0,finite_record=0;
 reg [15:0] trigger_level_l=0;
 reg [4:0] decim_l=0;reg [9:0] encode_l=10'h3ff;
 wire [AW+1:0] requested={1'b0,pre_count}+{1'b0,post_count};
 wire [AW+1:0] read_end={1'b0,offset}+{1'b0,length};
 wire decim_legal=decim_log==0 || (decim_log>=4 && decim_log<=20);
 wire legal=operation==0 ? (decim_legal && trigger_mode!=3 && post_count!=0 && requested<=(1<<AW)) :
            operation==1 ? (decim_log>=8 && decim_log<=20) :
            operation==2 && record_frozen && read_end<={1'b0,record_words};
 assign active=ca || ba || writer_granted;
 assign fault=cf || bf;
 assign start_ready=!reset && !fault && !active && bank_busy==0 && bready && cstart_ready;
 wire accept=start && start_ready && legal;
 wire capture_start=accept && operation==0;
 wire backend_start=accept && operation!=0;
 assign record_frozen=finite_record && cfr && !fault;
 assign done=selected_operation==0 ? (record_frozen && !active) : bd;
 always @(posedge core_clk)begin
  request_error<=start && (!start_ready || !legal);
  if(reset)begin finite_record<=0;request_error<=0;selected_operation<=0;trigger_second<=0;end
  else if(accept)begin
   selected_operation<=operation;
   if(operation!=2)begin
    finite_record<=operation==0;decim_l<=decim_log;encode_l<=encode_enable;
    trigger_mode_l<=trigger_mode;trigger_channel_l<=trigger_channel;
    trigger_falling_l<=trigger_falling;trigger_level_l<=trigger_level;trigger_second<=0;
   end
  end else if(ce && cr && source_valid && trigger_event && !triggered)
   trigger_second<=trigger_mode_l==1 && !force_trigger && !first_edge && second_edge;
 end
 // Keep finite frontend alive through priming and physical drain. Streaming
 // ends on the controller's source boundary, not on individual SRAM excursions.
 wire frontend_enable=!reset && (ca || ben);
 adc_acquisition_source source(
  .core(core_clk),.packclk(ram_clk),.clk100(clk100),.enable(frontend_enable),
  .lane(lane),.decim_log(decim_l),.encode_enable(encode_l),
  .snapshot_request(snapshot_request),.snapshot_ack(snapshot_ack),.snapshot(snapshot),
  .enc_p(enc_p),.enc_n(enc_n),.adc_locked(adc_locked),
  .data(source_word),.valid(source_valid),.fault(source_fault),.precision_mode(precision_mode));
 wire [15:0] first_value=precision_mode ? (trigger_channel_l ? source_word[31:16] : source_word[15:0]) :
                         {trigger_channel_l ? source_word[15:8] : source_word[7:0],8'b0};
 wire [15:0] second_value={trigger_channel_l ? source_word[31:24] : source_word[23:16],8'b0};
 reg [15:0] previous=0;reg previous_valid=0;
 wire first_edge=previous_valid && (trigger_falling_l ?
                   (previous>trigger_level_l && first_value<=trigger_level_l) :
                   (previous<trigger_level_l && first_value>=trigger_level_l));
 wire second_edge=!precision_mode && (trigger_falling_l ?
                   (first_value>trigger_level_l && second_value<=trigger_level_l) :
                   (first_value<trigger_level_l && second_value>=trigger_level_l));
 wire trigger_event=force_trigger || trigger_mode_l==0 ||
                    (trigger_mode_l==1 && (first_edge || second_edge)) ||
                    (trigger_mode_l==2 && match_trigger);
 always @(posedge core_clk)begin
  if(!frontend_enable)previous_valid<=0;
  else if(source_valid)begin previous<=precision_mode ? first_value : second_value;previous_valid<=1;end
 end
 sram_finite_writer #(.AW(AW)) writer(
  .clk(core_clk),.reset(reset),.start(capture_start),
  .capture_allowed(bready && !ba && bank_busy==0 && !writer_granted && !bf),.halt(halt),
  .pre_count(pre_count),.post_count(post_count),.source_valid(source_valid),
  .source_fault(source_fault),.trigger(trigger_event),.source_data(source_word),
  .start_ready(cstart_ready),.active(ca),.source_enable(ce),.source_ready(cr),
  .frozen(cfr),.fault(cf),.request_error(cerr),.triggered(triggered),
  .record_start(record_start),.record_words(record_words),.trigger_index(trigger_index),
  .writer_request(writer_request),.writer_command(writer_command),.writer_valid(writer_valid),
  .writer_stop(writer_stop),.writer_data(writer_data),.writer_ready(writer_ready),
  .writer_write_ready(writer_write_ready),.position(position));
 sram_board_capture_path #(.AW(AW),.READ_DELAY(READ_DELAY),.CONTINUE_READS(CONTINUE_READS)) backend(
  .reset(reset),.locked(locked),.core_clk(core_clk),.sample_clk(sample_clk),.ram_clk(ram_clk),.host_clk(host_clk),
  .start(backend_start),.finite_mode(operation==2),.stop(halt),.source_finished(!ben && !source_valid),
  .frozen(record_frozen),.record_start(record_start),.read_bias(read_bias),
  .record_words(record_words),.offset(offset),.length(length),
  .source_valid(source_valid && ben),.source_fault(source_fault && selected_operation==1),.source_data({4'b0,source_word}),
  .source_ready(bsready),.start_ready(bready),.source_enable(ben),.active(ba),.done(bd),.capture_done(bc),
  .fault(bf),.request_error(berr),.start_rejected(brejected),.selected_finite(selected_finite),.error_code(backend_error),
  .committed(committed),.read_ordinal(read_ordinal),.unread(unread),.bank_busy(bank_busy),
  .host_release(host_release),.release_bank(release_bank),.release_token(release_token),
  .host_fault(host_fault),.host_ready(host_ready),.host_token(host_token),
  .host_first0(host_first0),.host_first1(host_first1),.host_words0(host_words0),.host_words1(host_words1),
  .read_enable(read_enable),.read_halfword(read_halfword),.read_valid(read_valid),.read_error(read_error),.read_data(read_data),
  .writer_request(writer_request),.writer_command(writer_command),.writer_valid(writer_valid),
  .writer_stop(writer_stop),.writer_data(writer_data),.writer_ready(writer_ready),
  .writer_write_ready(writer_write_ready),.writer_done(writer_done),.writer_granted(writer_granted),.position(position),
  .dq(dq),.k1(k1),.k2(k2),.g1(g1));
endmodule
