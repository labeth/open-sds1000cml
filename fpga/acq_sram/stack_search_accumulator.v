// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Retained-record search -> external segment verdict -> host-backed tile updates.
// A successful segment verdict is acknowledged to the peak selector only after
// all writes complete, so separation and odd/even hit numbering follow committed
// hits. Host commands are excluded for the entire search, including verdict gaps.
// Faults poison the logical stack until shared reset; an in-flight accumulation
// finishes releasing its tile before done. Both memory adapters MUST share reset.
// Segment checks, gain/offset normalization, physical memory access, capture/tile
// identity and the live host driver remain external; this is not a deployed image.
// TRLC-LINKS: REQ-SDS-141
module stack_search_accumulator #(parameter BINS=32,parameter [31:0] MAX_SAMPLES=32'd1048576)(
 input wire clk,reset,start,output wire start_ready,busy,output reg done=0,invalid=0,
 input wire [31:0] first_position,window_count,gate_length,record_samples,reference_samples,min_separation,
 input wire signed [49:0] threshold,
 input wire [31:0] bin_count,first_bin,factor,initial_hits,output reg [31:0] accepted_hits=0,
 output wire request_valid,input wire request_ready,output wire [31:0] reference_address,candidate_address,
 input wire response_valid,response_error,input wire [7:0] reference_data,candidate_data,output wire response_ready,
 output wire candidate_valid,input wire verdict_valid,verdict_accept,output wire verdict_ready,
 input wire [1:0] verdict_channel_mask,
 output wire [31:0] candidate_position,output wire signed [49:0] candidate_score,left_score,right_score,
 output wire signed [24:0] candidate_delta,output wire left_present,right_present,
 output wire sample_request_valid,input wire sample_request_ready,output wire [31:0] sample_index,
 input wire sample_response_valid,sample_response_error,output wire sample_response_ready,
 input wire [36:0] ch0_left,ch0_right,ch1_left,ch1_right,
 input wire command_valid,output wire command_ready,input wire command_write,
 input wire [31:0] command_bin,input wire command_channel,
 input wire upload_valid,output wire upload_ready,input wire [15:0] upload_data,
 output wire download_valid,input wire download_ready,output wire [15:0] download_data,
 output wire completion_valid,input wire completion_ready,output wire completion_error,host_busy,initialized
);
 localparam TILE_COUNT_BITS=BINS<2 ? 1:$clog2(BINS+1);
 localparam IDLE=0,SEARCH=1,UPDATE=2,LAUNCH=3,COMMIT=4;
 reg [2:0] state=IDLE;
 reg [1:0] mask_l=0;
 // Full-width bad_tile validation precedes every use of this bounded count.
 reg [TILE_COUNT_BITS-1:0] tile_bins=0;
 reg [31:0] origin=0,interpolation=0,record_length=0;
 wire store_ready,store_done,store_invalid,store_busy,host_ready;
 wire search_candidate,search_done,search_invalid,search_busy,read_request;
 wire search_fault=search_done && search_invalid;
 wire host_allowed=state==IDLE && !invalid && !reset;
 wire [32:0] tile_end={1'b0,first_bin}+{1'b0,bin_count};
 wire bad_tile=bin_count==0 || bin_count>BINS || factor==0 || tile_end>33'h100000000;
 // IDLE is entered only after the store has released ownership. Host busy
 // includes held completions; a simultaneous host command retains priority.
 assign start_ready=host_allowed && initialized && !host_busy && !command_valid;
 wire launch=start && start_ready && !bad_tile;
 assign busy=state!=IDLE && !reset;
 assign command_ready=host_allowed && host_ready;
 wire permit_reads=busy && !invalid && !search_fault;
 assign request_valid=read_request && permit_reads;
 assign candidate_valid=state==SEARCH && search_candidate && !invalid && !search_fault && !reset;
 // SEARCH has no store operation in flight. LAUNCH independently waits for
 // store readiness, avoiding a store-to-search combinational control chain.
 assign verdict_ready=candidate_valid;
 wire verdict=verdict_valid && verdict_ready;
 wire take_hit=verdict && verdict_accept;
 // A registered launch keeps count checking and verdict arbitration out of
 // the accumulator's broad start/reset fanout. The candidate stays held.
 wire store_start=state==LAUNCH && !invalid && !search_fault && !reset;
 wire committed=state==COMMIT && !invalid && !search_fault;
 wire peak_verdict=(verdict && !verdict_accept) || committed;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;invalid<=0;accepted_hits<=0;end
  else case(state)
   IDLE:if(start && start_ready)begin
    accepted_hits<=initial_hits;tile_bins<=bin_count[TILE_COUNT_BITS-1:0];origin<=first_bin;
    interpolation<=factor;record_length<=record_samples;
    if(bad_tile)begin invalid<=1;done<=1;end
    else state<=SEARCH;
   end
   SEARCH:begin
    if(search_done)begin state<=IDLE;done<=1;invalid<=search_invalid;end
    else if(verdict && verdict_accept && accepted_hits==32'hffffffff)begin
     state<=IDLE;done<=1;invalid<=1;
    end else if(take_hit)begin mask_l<=verdict_channel_mask;state<=LAUNCH;end
   end
   LAUNCH:begin
    if(search_fault)begin state<=IDLE;done<=1;invalid<=1;end
    else if(store_ready)state<=UPDATE;
   end
   UPDATE:begin
    if(search_fault)invalid<=1;
    if(store_done)begin
     if(invalid || search_fault || store_invalid)begin state<=IDLE;done<=1;invalid<=1;end
     else begin accepted_hits<=accepted_hits+1'b1;state<=COMMIT;end
    end
   end
   COMMIT:begin
    if(search_fault)begin state<=IDLE;done<=1;invalid<=1;end
    else state<=SEARCH;
   end
   default:begin state<=IDLE;done<=1;invalid<=1;end
  endcase
 end
 stack_memory_search #(.MAX_SAMPLES(MAX_SAMPLES)) search(.clk(clk),.reset(reset),.start(launch),
 .first_position(first_position),.window_count(window_count),.gate_length(gate_length),.record_samples(record_samples),
 .reference_samples(reference_samples),.min_separation(min_separation),.threshold(threshold),
 .request_valid(read_request),.request_ready(request_ready && permit_reads),.reference_address(reference_address),.candidate_address(candidate_address),
 .response_valid(response_valid),.response_error(response_error),.reference_data(reference_data),.candidate_data(candidate_data),.response_ready(response_ready),
 .candidate_valid(search_candidate),.verdict_valid(peak_verdict),.verdict_accept(committed),
 .candidate_position(candidate_position),.candidate_score(candidate_score),.left_score(left_score),.right_score(right_score),
 .candidate_delta(candidate_delta),.left_present(left_present),.right_present(right_present),
 .busy(search_busy),.done(search_done),.invalid(search_invalid));
 stack_tiled_accumulator #(.BINS(BINS)) store(.clk(clk),.reset(reset),.start(store_start),.start_ready(store_ready),
 .busy(store_busy),.done(store_done),.invalid(store_invalid),.hit_position(candidate_position),.delta(candidate_delta),
 .bin_count({{(32-TILE_COUNT_BITS){1'b0}},tile_bins}),.first_bin(origin),.factor(interpolation),.record_samples(record_length),
 .channel_mask(mask_l),.odd(!accepted_hits[0]),
 .sample_request_valid(sample_request_valid),.sample_request_ready(sample_request_ready),.sample_index(sample_index),
 .sample_response_valid(sample_response_valid),.sample_response_error(sample_response_error),.sample_response_ready(sample_response_ready),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
 .command_valid(command_valid && host_allowed),.command_ready(host_ready),.command_write(command_write),.command_bin(command_bin),.command_channel(command_channel),
 .upload_valid(upload_valid),.upload_ready(upload_ready),.upload_data(upload_data),
 .download_valid(download_valid),.download_ready(download_ready),.download_data(download_data),
 .completion_valid(completion_valid),.completion_ready(completion_ready),.completion_error(completion_error),.host_busy(host_busy),.initialized(initialized));
endmodule
