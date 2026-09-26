// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// One accepted aligned hit -> normalized reads -> resampling -> stored moments.
// Starts while busy are ignored. A fault requires shared reset of both memory
// adapters and invalidation of accumulated state; an in-flight write may have
// committed. done on success waits for the final state-write acknowledgement.
// Physical memory ownership and drift normalization are adapter obligations.
// TRLC-LINKS: REQ-SDS-141
module stack_resample_store(
 input wire clk,reset,start,
 input wire [31:0] hit_position,bin_count,factor,record_samples,
 input wire signed [24:0] delta,input wire [1:0] channel_mask,input wire odd,
 output wire sample_request_valid,input wire sample_request_ready,output wire [31:0] sample_index,
 input wire sample_response_valid,sample_response_error,output wire sample_response_ready,
 input wire [36:0] ch0_left,ch0_right,ch1_left,ch1_right,
 output wire state_request_valid,input wire state_request_ready,
 output wire state_write,output wire [31:0] state_bin,output wire state_channel,
 output wire [68:0] write_sum,write_sum_a,output wire [105:0] write_sum2,
 output wire [31:0] write_count,write_count_a,
 input wire state_response_valid,state_response_error,output wire state_response_ready,
 input wire [68:0] read_sum,read_sum_a,input wire [105:0] read_sum2,
 input wire [31:0] read_count,read_count_a,
 output reg busy=0,done=0,invalid=0
);
 wire launch=start && !busy && !invalid;
 wire rs_request,rs_valid,rs_ready,rs_busy,rs_done,rs_invalid;
 wire [31:0] bin;wire [36:0] value0,value1;wire [1:0] mask;wire hit_odd;
 wire writer_ready,writer_done,writer_fault,writer_request;
 reg resampling_finished=0;
 assign sample_request_valid=rs_request && busy && !invalid;
 assign state_request_valid=writer_request && busy && !invalid;
 assign rs_ready=writer_ready && busy && !invalid;
 stack_resample resampler(.clk(clk),.reset(reset),.start(launch),.hit_position(hit_position),
  .bin_count(bin_count),.factor(factor),.record_samples(record_samples),.delta(delta),
  .channel_mask(channel_mask),.odd(odd),.request_valid(rs_request),
  .request_ready(sample_request_ready && busy && !invalid),.request_index(sample_index),
  .response_valid(sample_response_valid),.response_error(sample_response_error),.response_ready(sample_response_ready),
  .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
  .valid(rs_valid),.ready(rs_ready),.bin(bin),.ch0_value(value0),.ch1_value(value1),
  .result_mask(mask),.result_odd(hit_odd),.busy(rs_busy),.done(rs_done),.invalid(rs_invalid));
 stack_bin_writer writer(.clk(clk),.reset(reset),.valid(rs_valid && busy && !invalid),.ready(writer_ready),
  .bin(bin),.mask(mask),.odd(hit_odd),.value0(value0),.value1(value1),
  .request_valid(writer_request),.request_ready(state_request_ready && busy && !invalid),
  .request_write(state_write),.request_bin(state_bin),.request_channel(state_channel),
  .write_sum(write_sum),.write_sum2(write_sum2),.write_sum_a(write_sum_a),
  .write_count(write_count),.write_count_a(write_count_a),
  .response_valid(state_response_valid),.response_error(state_response_error),.response_ready(state_response_ready),
  .read_sum(read_sum),.read_sum2(read_sum2),.read_sum_a(read_sum_a),.read_count(read_count),.read_count_a(read_count_a),
  .done(writer_done),.fault(writer_fault));
 always @(posedge clk)begin
  done<=0;
  if(reset)begin busy<=0;invalid<=0;resampling_finished<=0;end
  else if(launch)begin busy<=1;resampling_finished<=0;end
  else if(busy)begin
   if(rs_done)resampling_finished<=1;
   if(rs_invalid || writer_fault)begin invalid<=1;busy<=0;done<=1;end
   else if(resampling_finished && writer_ready)begin busy<=0;done<=1;end
  end
 end
endmodule
