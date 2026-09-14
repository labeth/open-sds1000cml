// ADC -> precision -> finite/continuous SRAM capture -> shared host recall.
// All control inputs are core_clk synchronous. Host RAM ports use host_clk.
// operation: 0 finite capture, 1 continuous stream, 2 frozen finite recall.
// trigger_mode: 0 automatic, 1 edge, 2 external match on source_word/valid.
// This is the acquisition core; board PLL/pins and GPMC ABI remain external.
module sram_acquisition_path #(parameter ENABLE_STREAM=1,AW=19,READ_DELAY=0,CONTINUE_READS=1)(
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
 wire capture_legal=decim_legal && trigger_mode!=3 && post_count!=0 && requested<=(1<<AW);
 wire stream_legal=ENABLE_STREAM && decim_log>=8 && decim_log<=20;
 wire [AW:0] record_words_sampled;
 wire recall_legal=record_frozen && read_end<={1'b0,record_words_sampled};
 assign active=ca || ba || writer_granted;
 assign fault=cf || bf;
 wire idle_ready_now=!reset && !fault && !active && bank_busy==0 && bready && cstart_ready;
 // Resolve nested client/transport idle checks before a request arrives.
 // Every request consumes the cached readiness, including a rejected request.
 // Faults, lock loss and ownership invalidate it immediately, not a clock later.
 (* preserve *) reg idle_ready_q=0;
 always @(posedge core_clk)begin
  if(reset)idle_ready_q<=0;
  else idle_ready_q<=idle_ready_now && !start;
 end
 assign start_ready=idle_ready_q && !reset && locked && !fault && !source_fault && !active && bank_busy==0;
 // synthesis translate_off
 always @(posedge core_clk)if(start_ready && !idle_ready_now)
  $fatal(1,"cached acquisition readiness outlived client readiness");
 always @(posedge core_clk)if(start_ready && record_frozen && record_words_sampled!==record_words)
  $fatal(1,"recall length pipeline not settled before readiness");
 // synthesis translate_on
 // Keep mode-specific acceptance separate. Recall geometry must not feed
 // the wide configuration enables for a new ADC capture or stream.
 (* keep *) wire capture_start=start && start_ready && operation==0 && capture_legal;
 (* keep *) wire stream_start=start && start_ready && operation==1 && stream_legal;
 (* keep *) wire recall_start=start && start_ready && operation==2 && recall_legal;
 wire accept=capture_start || stream_start || recall_start;
 wire backend_start=stream_start || recall_start;
 assign record_frozen=finite_record && cfr && !fault;
 assign done=selected_operation==0 ? (record_frozen && !active) : bd;
 // Capture data settings speculatively while no producer/transport owner is
 // active. The accepted-start edge captures its inputs; active then holds
 // them. Idle updates cannot change the independently frozen record metadata.
 always @(posedge core_clk)if(!active)begin
  decim_l<=decim_log;encode_l<=encode_enable;
  trigger_mode_l<=trigger_mode;trigger_channel_l<=trigger_channel;
  trigger_falling_l<=trigger_falling;trigger_level_l<=trigger_level;
 end
 always @(posedge core_clk)begin
  request_error<=start && !accept;
  if(reset)begin finite_record<=0;request_error<=0;selected_operation<=0;trigger_second<=0;end
  else begin
   if(capture_start)selected_operation<=0;
   else if(stream_start)selected_operation<=1;
   else if(recall_start)selected_operation<=2;
   if(capture_start || stream_start)begin
    finite_record<=capture_start;trigger_second<=0;
   end else if(ce && cr && capture_valid && trigger_event && !triggered)
    trigger_second<=capture_second;
  end
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
 // Two stages separate channel/format selection from threshold comparison.
 // Only the finite writer is delayed; continuous streaming remains unchanged.
 wire [31:0] capture_word;wire capture_valid,trigger_event,capture_second;
 acquisition_trigger_pipeline trigger_pipe(
  .clk(core_clk),.enable(frontend_enable),.valid(source_valid),.data(source_word),
  .precision_mode(precision_mode),.channel(trigger_channel_l),.falling(trigger_falling_l),
  .level(trigger_level_l),.mode(trigger_mode_l),.force_trigger(force_trigger),.match_trigger(match_trigger),
  .out_data(capture_word),.out_valid(capture_valid),.event_trigger(trigger_event),.second(capture_second));
 sram_finite_writer #(.AW(AW)) writer(
  .clk(core_clk),.reset(reset),.start(capture_start),
  .capture_allowed(bready && !ba && bank_busy==0 && !writer_granted && !bf),.halt(halt),
  .pre_count(pre_count),.post_count(post_count),.source_valid(capture_valid),
  .source_fault(source_fault),.trigger(trigger_event),.source_data(capture_word),
  .start_ready(cstart_ready),.active(ca),.source_enable(ce),.source_ready(cr),
  .frozen(cfr),.fault(cf),.request_error(cerr),.triggered(triggered),
  .record_start(record_start),.record_words(record_words),.trigger_index(trigger_index),
  .writer_request(writer_request),.writer_command(writer_command),.writer_valid(writer_valid),
  .writer_stop(writer_stop),.writer_data(writer_data),.writer_ready(writer_ready),
  .writer_write_ready(writer_write_ready),.position(position));
 sram_board_capture_path #(.ENABLE_STREAM(ENABLE_STREAM),.AW(AW),.READ_DELAY(READ_DELAY),.CONTINUE_READS(CONTINUE_READS)) backend(
  .reset(reset),.locked(locked),.core_clk(core_clk),.sample_clk(sample_clk),.ram_clk(ram_clk),.host_clk(host_clk),
  .start(backend_start),.finite_mode(!ENABLE_STREAM || operation==2),.stop(halt),.source_finished(!ben && !source_valid),
  .frozen(record_frozen),.record_start(record_start),.read_bias(read_bias),
  .record_words(record_words),.record_words_sampled(record_words_sampled),.offset(offset),.length(length),
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

// Control settings remain fixed throughout enable. Force and external match
// accompany the input word, including across bubbles. Two cycles of latency,
// one word per cycle. Remember only threshold relations for the previous sample.
module acquisition_trigger_pipeline(
 input wire clk,enable,valid,input wire [31:0] data,
 input wire precision_mode,channel,falling,input wire [15:0] level,
 input wire [1:0] mode,input wire force_trigger,match_trigger,
 output reg [31:0] out_data=0,output reg out_valid=0,
 output wire event_trigger,second
);
 reg [31:0] data_s=0;
 reg [15:0] first_s=0;reg [7:0] second_s=0;
 reg valid_s=0,precision_s=0,force_s=0,match_s=0;
 reg first_above=0,first_below=0,second_above=0,second_below=0;
 reg precision_q=0,force_q=0,match_q=0;
 reg previous_above=0,previous_below=0,previous_valid=0;
 wire first_edge=previous_valid && (falling ? previous_above && !first_above : previous_below && !first_below);
 wire second_edge=!precision_q && (falling ? first_above && !second_above : first_below && !second_below);
 assign event_trigger=force_q || mode==0 || (mode==1 && (first_edge || second_edge)) || (mode==2 && match_q);
 assign second=mode==1 && !force_q && !first_edge && second_edge;
 always @(posedge clk)begin
  if(!enable)begin valid_s<=0;out_valid<=0;previous_valid<=0;end
  else begin
   valid_s<=valid;out_valid<=valid_s;
   begin // Payload may advance through bubbles; validity carries ownership.
    data_s<=data;precision_s<=precision_mode;force_s<=force_trigger;match_s<=match_trigger;
    first_s<=precision_mode ? (channel ? data[31:16] : data[15:0]) : {channel ? data[15:8] : data[7:0],8'b0};
    second_s<=channel ? data[31:24] : data[23:16];
   end
   begin
    out_data<=data_s;precision_q<=precision_s;force_q<=force_s;match_q<=match_s;
    first_above<=first_s>level;first_below<=first_s<level;
    second_above<={second_s,8'b0}>level;second_below<={second_s,8'b0}<level;
   end
   if(out_valid)begin
    previous_valid<=1;
    previous_above<=precision_q ? first_above : second_above;
    previous_below<=precision_q ? first_below : second_below;
   end
  end
 end
endmodule
