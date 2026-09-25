// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Consecutive reference/candidate windows -> scored peak candidates. Each window
// supplies exactly gate_length accepted pairs in address order; the external
// memory scheduler supplies the same reference and the next shifted candidate.
// One result may wait behind a segment verdict; no score is dropped on stalls.
// This is a search datapath, not reference storage or a complete stack engine.
// TRLC-LINKS: REQ-SDS-141
module stack_search(
 input wire clk,reset,start,
 input wire [31:0] first_position,window_count,gate_length,min_separation,
 input wire signed [49:0] threshold,
 input wire pair_valid,input wire [7:0] reference_sample,candidate_sample,
 output wire pair_ready,candidate_valid,
 input wire verdict_valid,verdict_accept,
 output wire [31:0] candidate_position,
 output wire signed [49:0] candidate_score,left_score,right_score,
 output wire left_present,right_present,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,LAUNCH=1,FEED=2,WAIT_SCORE=3,PUBLISH=4,FINISH=5;
 reg [2:0] state=IDLE;
 reg [31:0] length=0,pairs_left=0,windows_left=0;
 wire [32:0] end_exclusive={1'b0,first_position}+{1'b0,window_count};
 wire bad_config=window_count==0 || gate_length<4 ||
  end_exclusive>33'h100000000 ||
  threshold>50'sh1000000000000 || threshold< -50'sh1000000000000;
 wire score_ready,score_defined,score_invalid,score_overflow,score_busy,score_done;
 wire signed [49:0] score;
 wire peaks_ready,peaks_candidate,peaks_busy,peaks_done,peaks_invalid;
 wire pair_last=pairs_left==1;
 assign pair_ready=busy && state==FEED && score_ready && !start && !reset;
 assign candidate_valid=busy && peaks_candidate && !start && !reset;
 stack_match_score scoring(.clk(clk),.reset(reset || start || peaks_done),
  .start(state==LAUNCH && !reset && !start),.valid(pair_valid && pair_ready),.last(pair_last),
  .x(reference_sample),.y(candidate_sample),.ready(score_ready),.defined(score_defined),
  .invalid(score_invalid),.overflow(score_overflow),.busy(score_busy),.done(score_done),.score(score));
 stack_peaks peaks(.clk(clk),.reset(reset || (start && bad_config)),.start(start && !bad_config),
  .first_position(first_position),.min_separation(min_separation),.threshold(threshold),
  .valid(state==PUBLISH),.last(windows_left==1),.score_defined(score_defined),
  .score_invalid(score_invalid),.score(score),.ready(peaks_ready),.candidate_valid(peaks_candidate),
  .verdict_valid(verdict_valid),.verdict_accept(verdict_accept),.candidate_position(candidate_position),
  .candidate_score(candidate_score),.left_score(left_score),.right_score(right_score),
  .left_present(left_present),.right_present(right_present),
  .busy(peaks_busy),.done(peaks_done),.invalid(peaks_invalid));
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;end
  else if(start)begin
   length<=gate_length;windows_left<=window_count;
   invalid<=bad_config;busy<=!bad_config;done<=bad_config;
   state<=bad_config ? IDLE:LAUNCH;
  end else if(peaks_done)begin
   state<=IDLE;busy<=0;done<=1;invalid<=peaks_invalid;
  end else case(state)
   LAUNCH:begin pairs_left<=length;state<=FEED;end
   FEED:if(pair_valid && pair_ready)begin
    pairs_left<=pairs_left-1'b1;
    if(pair_last)state<=WAIT_SCORE;
   end
   WAIT_SCORE:if(score_done)state<=PUBLISH;
   PUBLISH:if(peaks_ready)begin
    if(windows_left==1)state<=FINISH;
    else begin windows_left<=windows_left-1'b1;state<=LAUNCH;end
   end
  endcase
 end
endmodule
