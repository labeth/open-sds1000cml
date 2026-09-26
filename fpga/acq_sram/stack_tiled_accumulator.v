// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Host-restored tile -> exclusive accepted-hit processing -> host readback.
// Each accepted start leases the tile and latches geometry in the datapath on
// the same edge. Completion waits for ownership release after write ack.
// A processing fault poisons the tile: no more host or hit commands until shared
// reset clears it. Normalized-sample storage must share reset and remain stable.
// No GPMC mapping, clock crossing, drift fitting or candidate acceptance here.
// TRLC-LINKS: REQ-SDS-141
module stack_tiled_accumulator #(parameter BINS=32)(
 input wire clk,reset,start,output wire start_ready,output wire busy,output reg done=0,invalid=0,
 input wire [31:0] hit_position,bin_count,factor,record_samples,first_bin,
 input wire signed [24:0] delta,input wire [1:0] channel_mask,input wire odd,
 output wire sample_request_valid,input wire sample_request_ready,output wire [31:0] sample_index,
 input wire sample_response_valid,sample_response_error,output wire sample_response_ready,
 input wire [36:0] ch0_left,ch0_right,ch1_left,ch1_right,
 input wire command_valid,output wire command_ready,input wire command_write,
 input wire [31:0] command_bin,input wire command_channel,
 input wire upload_valid,output wire upload_ready,input wire [15:0] upload_data,
 output wire download_valid,input wire download_ready,output wire [15:0] download_data,
 output wire completion_valid,input wire completion_ready,output wire completion_error,output wire host_busy,
 output wire initialized
);
 localparam IDLE=0,RUN=1,RELEASE=2;
 reg [1:0] state=IDLE;
 wire acquire_ready,owned,host_ready,core_busy,core_done,core_invalid;
 wire req_valid,req_ready,req_write,req_channel,resp_valid,resp_ready,resp_error;
 wire [31:0] req_bin;
 wire host_allowed=state==IDLE && !invalid && !reset;
 wire legal_bins=bin_count!=0 && bin_count<=BINS;
 assign start_ready=host_allowed && acquire_ready && initialized;
 wire launch=start && start_ready && legal_bins;
 assign busy=state!=IDLE && !reset;
 assign command_ready=host_ready && host_allowed;
 wire [68:0] write_sum,read_sum;
 wire [68:0] write_sum_a,read_sum_a;
 wire [105:0] write_sum2,read_sum2;
 wire [31:0] write_count,read_count;
 wire [31:0] write_count_a,read_count_a;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;invalid<=0;end
  else case(state)
   IDLE:if(start && start_ready)begin
    if(legal_bins)state<=RUN;
    else begin invalid<=1;done<=1;end
   end
   RUN:if(core_done)begin invalid<=core_invalid;state<=RELEASE;end
   RELEASE:if(!owned)begin state<=IDLE;done<=1;end
   default:begin state<=IDLE;invalid<=1;done<=1;end
  endcase
 end
 stack_resample_store core(.clk(clk),.reset(reset),.start(launch),.hit_position(hit_position),
 .bin_count(bin_count),.factor(factor),.record_samples(record_samples),.first_bin(first_bin),.delta(delta),.channel_mask(channel_mask),.odd(odd),
 .sample_request_valid(sample_request_valid),.sample_request_ready(sample_request_ready),.sample_index(sample_index),
 .sample_response_valid(sample_response_valid),.sample_response_error(sample_response_error),.sample_response_ready(sample_response_ready),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
 .state_request_valid(req_valid),.state_request_ready(req_ready),.state_write(req_write),.state_bin(req_bin),.state_channel(req_channel),
 .state_response_valid(resp_valid),.state_response_ready(resp_ready),.state_response_error(resp_error),
 .write_sum(write_sum),.read_sum(read_sum),
 .write_sum_a(write_sum_a),.read_sum_a(read_sum_a),
 .write_sum2(write_sum2),.read_sum2(read_sum2),
 .write_count(write_count),.read_count(read_count),
 .write_count_a(write_count_a),.read_count_a(read_count_a),
 .busy(core_busy),.done(core_done),.invalid(core_invalid));
 stack_tile_access #(.BINS(BINS)) access(.clk(clk),.reset(reset),
 .command_valid(command_valid && host_allowed),.command_ready(host_ready),.command_write(command_write),.command_bin(command_bin),.command_channel(command_channel),
 .upload_valid(upload_valid),.upload_ready(upload_ready),.upload_data(upload_data),
 .download_valid(download_valid),.download_ready(download_ready),.download_data(download_data),
 .completion_valid(completion_valid),.completion_ready(completion_ready),.completion_error(completion_error),.busy(host_busy),.initialized(initialized),
 .engine_acquire(launch),.engine_acquire_ready(acquire_ready),.engine_release(state==RELEASE),.engine_owned(owned),
 .engine_request_valid(req_valid),.engine_request_ready(req_ready),.engine_request_write(req_write),.engine_request_bin(req_bin),.engine_request_channel(req_channel),
 .engine_response_valid(resp_valid),.engine_response_ready(resp_ready),.engine_response_error(resp_error),
 .engine_write_sum(write_sum),.engine_read_sum(read_sum),
 .engine_write_sum_a(write_sum_a),.engine_read_sum_a(read_sum_a),
 .engine_write_sum2(write_sum2),.engine_read_sum2(read_sum2),
 .engine_write_count(write_count),.engine_read_count(read_count),
 .engine_write_count_a(write_count_a),.engine_read_count_a(read_count_a)
 );
endmodule
