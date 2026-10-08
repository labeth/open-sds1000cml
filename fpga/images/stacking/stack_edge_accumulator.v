// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Edge-locked hits -> host-backed tile updates over one retained-record port.
// The scanner and the accumulator never read concurrently: the scanner holds a
// candidate without outstanding reads until its update commits. Raw eight-bit
// codes enter the accumulator as Q24 values without gain/offset correction.
// Odd/even numbering follows committed hits from initial_hits, so repeated tile
// passes over one record reproduce the same split. Host tile commands are only
// accepted while idle. Faults poison the stack until shared reset.
// POW2_FACTOR=1 accepts only power-of-two factors up to 2^24 (stack_positions).
// With qualify_enable, a candidate is accepted only when the sum of absolute
// differences between the align channel at crossing - qualify_pre + k*stride,
// k < qualify_count, and the host template is at most qualify_threshold. A
// window leaving the record rejects. Rejected candidates are counted and do
// not restart the separation hold-off (ADR-STACKING-TEMPLATE-QUALIFIER).
// The template RAM belongs to the caller: template_data follows
// template_address one clock later.
// ADR-STACKING-IMAGE-SPLIT.
// TRLC-LINKS: REQ-SDS-141
module stack_edge_accumulator #(parameter BINS=32,POW2_FACTOR=0)(
 input wire clk,reset,start,peek,output wire start_ready,busy,output reg done=0,invalid=0,
 input wire [31:0] first_index,record_samples,pre_samples,min_separation,
 input wire channel,falling,input wire [7:0] level,hysteresis,
 input wire [31:0] bin_count,first_bin,factor,initial_hits,input wire [1:0] channel_mask,
 output reg [31:0] accepted_hits=0,output wire [31:0] crossings,peek_data,
 input wire qualify_enable,input wire [10:0] qualify_count,input wire [7:0] qualify_stride,
 input wire [31:0] qualify_pre,qualify_threshold,
 output wire [9:0] template_address,input wire [7:0] template_data,output reg [31:0] rejected=0,
 output wire request_valid,input wire request_ready,output wire [31:0] request_index,
 input wire response_valid,response_error,output wire response_ready,
 input wire [7:0] ch0_left,ch0_right,ch1_left,ch1_right,
 input wire command_valid,output wire command_ready,input wire command_write,
 input wire [31:0] command_bin,input wire command_channel,
 input wire upload_valid,output wire upload_ready,input wire [15:0] upload_data,
 output wire download_valid,input wire download_ready,output wire [15:0] download_data,
 output wire completion_valid,input wire completion_ready,output wire completion_error,host_busy,initialized
);
 localparam TILE_COUNT_BITS=BINS<2 ? 1:$clog2(BINS+1);
 localparam IDLE=0,SEARCH=1,LAUNCH=2,UPDATE=3,COMMIT=4,PEEK=5,
  Q_START=6,Q_CHECK=7,Q_REQUEST=8,Q_WAIT=9,Q_ADD=10,Q_TEST=11,Q_REJECT=12;
 reg [3:0] state=IDLE;
 // Qualifier configuration, latched at start with the rest of the geometry.
 // Positions fit 21 bits (records hold at most 2^20 samples) and the SAD 19
 // bits (1024 x 255); wider host values saturate, preserving every comparison.
 reg q_enable=0,q_channel=0;reg [10:0] q_count=0,q_left=0;reg [7:0] q_stride=0;
 reg [20:0] q_pre=0,q_last=0,q_index=0;reg [18:0] q_threshold=0;
 reg [9:0] q_address=0;reg [18:0] sad=0;reg [7:0] q_diff=0;
 wire [31:0] crossing;
 wire [7:0] q_sample=q_channel ? ch1_left : ch0_left;
 assign template_address=q_address;
 reg [1:0] mask_l=0;
 reg [TILE_COUNT_BITS-1:0] tile_bins=0;
 reg [31:0] origin=0,interpolation=0,record_length=0;
 // The candidate is latched on launch so the store's start logic sees registers.
 reg [31:0] hit_position_l=0;reg signed [24:0] hit_delta_l=0;
 wire store_ready,store_done,store_invalid,store_busy,host_ready;
 wire scan_done,scan_invalid,scan_busy,scan_candidate,scan_request;
 wire [31:0] scan_index,hit_position,store_index;wire signed [24:0] hit_delta;
 wire scan_response_ready,store_request,store_response_ready;
 wire host_allowed=state==IDLE && !invalid && !reset;
 wire [32:0] tile_end={1'b0,first_bin}+{1'b0,bin_count};
 wire bad_tile=bin_count==0 || bin_count>BINS || factor==0 || tile_end>33'h100000000 || channel_mask==0;
 assign start_ready=host_allowed && initialized && !host_busy && !command_valid;
 assign busy=state!=IDLE && !reset;
 assign command_ready=host_allowed && host_ready;
 wire launch=start && start_ready && !bad_tile;
 wire store_phase=state==LAUNCH || state==UPDATE;
 wire qualify_phase=state==Q_REQUEST || state==Q_WAIT;
 wire store_start=state==LAUNCH && !invalid && !reset;
 wire committed=state==COMMIT && !invalid;
 assign request_valid=store_phase ? store_request : qualify_phase ? state==Q_REQUEST : scan_request;
 assign request_index=store_phase ? store_index : qualify_phase ? {11'd0,q_index} : scan_index;
 assign response_ready=store_phase ? store_response_ready : qualify_phase ? state==Q_WAIT : scan_response_ready;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;invalid<=0;accepted_hits<=0;end
  else case(state)
   IDLE:if(peek && start_ready)state<=PEEK;
   else if(start && start_ready)begin
    accepted_hits<=initial_hits;tile_bins<=bin_count[TILE_COUNT_BITS-1:0];origin<=first_bin;
    interpolation<=factor;record_length<=record_samples;mask_l<=channel_mask;
    q_enable<=qualify_enable;q_channel<=channel;q_count<=qualify_count;q_stride<=qualify_stride;
    q_pre<=|qualify_pre[31:21] ? {21{1'b1}} : qualify_pre[20:0];
    q_threshold<=|qualify_threshold[31:19] ? {19{1'b1}} : qualify_threshold[18:0];
    q_last<=record_samples[20:0]-2'd2;rejected<=0;
    if(bad_tile)begin invalid<=1;done<=1;end
    else state<=SEARCH;
   end
   PEEK:if(scan_done)begin state<=IDLE;done<=1;invalid<=scan_invalid;end
   SEARCH:begin
    if(scan_done)begin state<=IDLE;done<=1;invalid<=scan_invalid;end
    else if(scan_candidate)begin
     if(accepted_hits==32'hffffffff)begin state<=IDLE;done<=1;invalid<=1;end
     else begin hit_position_l<=hit_position;hit_delta_l<=hit_delta;state<=q_enable ? Q_START : LAUNCH;end
    end
   end
   Q_START:begin
    q_index<=crossing[20:0]-q_pre;q_address<=0;q_left<=q_count;sad<=0;
    state<=crossing[20:0]<q_pre || q_count==0 || q_stride==0 ? Q_REJECT : Q_CHECK;
   end
   Q_CHECK:state<=q_index>q_last ? Q_REJECT : Q_REQUEST;
   Q_REQUEST:if(request_ready)state<=Q_WAIT;
   Q_WAIT:if(response_valid)begin
    if(response_error)begin state<=IDLE;done<=1;invalid<=1;end
    else begin q_diff<=q_sample>template_data ? q_sample-template_data : template_data-q_sample;state<=Q_ADD;end
   end
   Q_ADD:begin
    sad<=sad+q_diff;q_left<=q_left-1'b1;q_address<=q_address+1'b1;q_index<=q_index+q_stride;state<=Q_TEST;
   end
   Q_TEST:state<=sad>q_threshold ? Q_REJECT : q_left==0 ? LAUNCH : Q_CHECK;
   Q_REJECT:begin rejected<=rejected+1'b1;state<=SEARCH;end
   LAUNCH:if(store_ready)state<=UPDATE;
   UPDATE:if(store_done)begin
    if(store_invalid)begin state<=IDLE;done<=1;invalid<=1;end
    else begin accepted_hits<=accepted_hits+1'b1;state<=COMMIT;end
   end
   COMMIT:state<=SEARCH;
   default:begin state<=IDLE;done<=1;invalid<=1;end
  endcase
 end
 // A poisoned stack keeps the scanner in reset so it cannot issue reads.
 stack_edge_hits scan(.clk(clk),.reset(reset || invalid),.start(launch),.peek(peek && start_ready && state==IDLE),
 .first_index(first_index),.record_samples(record_samples),.pre_samples(pre_samples),.min_separation(min_separation),
 .channel(channel),.falling(falling),.level(level),.hysteresis(hysteresis),
 .request_valid(scan_request),.request_ready(request_ready && !store_phase && !qualify_phase),.request_index(scan_index),
 .response_valid(response_valid && !store_phase && !qualify_phase),.response_error(response_error),.response_ready(scan_response_ready),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
 .candidate_valid(scan_candidate),.candidate_ready(committed),.candidate_reject(state==Q_REJECT),.candidate_crossing(crossing),
 .candidate_position(hit_position),.candidate_delta(hit_delta),.crossings(crossings),.peek_data(peek_data),
 .busy(scan_busy),.done(scan_done),.invalid(scan_invalid));
 stack_tiled_accumulator #(.BINS(BINS),.POW2_FACTOR(POW2_FACTOR)) store(.clk(clk),.reset(reset),.start(store_start),.start_ready(store_ready),
 .busy(store_busy),.done(store_done),.invalid(store_invalid),.hit_position(hit_position_l),.delta(hit_delta_l),
 .bin_count({{(32-TILE_COUNT_BITS){1'b0}},tile_bins}),.first_bin(origin),.factor(interpolation),.record_samples(record_length),
 .channel_mask(mask_l),.odd(!accepted_hits[0]),
 .sample_request_valid(store_request),.sample_request_ready(request_ready && store_phase),.sample_index(store_index),
 .sample_response_valid(response_valid && store_phase),.sample_response_error(response_error),.sample_response_ready(store_response_ready),
 .ch0_left({5'd0,ch0_left,24'd0}),.ch0_right({5'd0,ch0_right,24'd0}),.ch1_left({5'd0,ch1_left,24'd0}),.ch1_right({5'd0,ch1_right,24'd0}),
 .command_valid(command_valid && host_allowed),.command_ready(host_ready),.command_write(command_write),.command_bin(command_bin),.command_channel(command_channel),
 .upload_valid(upload_valid),.upload_ready(upload_ready),.upload_data(upload_data),
 .download_valid(download_valid),.download_ready(download_ready),.download_data(download_data),
 .completion_valid(completion_valid),.completion_ready(completion_ready),.completion_error(completion_error),.host_busy(host_busy),.initialized(initialized));
endmodule
