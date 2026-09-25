// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Retained-memory adapter -> window scheduler -> scores -> peak candidates.
// A paired-read adapter must preserve the reference and record for the search,
// honor backpressure, and share reset with this module. Segment verdicts are
// still external. No SRAM ownership, channel calibration or accumulation here.
// TRLC-LINKS: REQ-SDS-141
module stack_memory_search(
 input wire clk,reset,start,
 input wire [31:0] first_position,window_count,gate_length,record_samples,reference_samples,min_separation,
 input wire signed [49:0] threshold,
 output wire request_valid,input wire request_ready,
 output wire [31:0] reference_address,candidate_address,
 input wire response_valid,response_error,input wire [7:0] reference_data,candidate_data,
 output wire response_ready,candidate_valid,
 input wire verdict_valid,verdict_accept,
 output wire [31:0] candidate_position,
 output wire signed [49:0] candidate_score,left_score,right_score,
 output wire left_present,right_present,
 output reg busy=0,done=0,invalid=0
);
 wire read_request,read_pair,read_pair_ready,read_busy,read_done,read_fault;
 wire [7:0] reference_sample,candidate_sample;
 wire search_busy,search_done,search_invalid,search_candidate;
 wire permit=busy && !read_fault && !search_invalid;
 assign request_valid=read_request && permit;
 assign candidate_valid=search_candidate && permit && !start && !reset;
 stack_window_reader reader(.clk(clk),.reset(reset),.start(start),
  .first_position(first_position),.window_count(window_count),.gate_length(gate_length),
  .record_samples(record_samples),.reference_samples(reference_samples),
  .request_valid(read_request),.request_ready(request_ready && permit),
  .reference_address(reference_address),.candidate_address(candidate_address),
  .response_valid(response_valid),.response_error(response_error),.reference_data(reference_data),
  .candidate_data(candidate_data),.response_ready(response_ready),
  .pair_valid(read_pair),.pair_ready(read_pair_ready),
  .reference_sample(reference_sample),.candidate_sample(candidate_sample),
  .busy(read_busy),.done(read_done),.fault(read_fault));
 stack_search search(.clk(clk),.reset(reset || (read_fault && !start)),.start(start),
  .first_position(first_position),.window_count(window_count),.gate_length(gate_length),
  .min_separation(min_separation),.threshold(threshold),
  .pair_valid(read_pair),.reference_sample(reference_sample),.candidate_sample(candidate_sample),
  .pair_ready(read_pair_ready),.candidate_valid(search_candidate),
  .verdict_valid(verdict_valid && busy),.verdict_accept(verdict_accept),
  .candidate_position(candidate_position),.candidate_score(candidate_score),.left_score(left_score),
  .right_score(right_score),.left_present(left_present),.right_present(right_present),
  .busy(search_busy),.done(search_done),.invalid(search_invalid));
 always @(posedge clk)begin
  done<=0;
  if(reset)begin busy<=0;invalid<=0;end
  else if(start)begin busy<=1;invalid<=0;end
  else if(busy && (read_fault || search_done))begin
   busy<=0;done<=1;invalid<=read_fault || search_invalid;
  end
 end
endmodule
