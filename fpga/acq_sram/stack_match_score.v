// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// One reference/candidate window from accepted sample pairs to a Q48 score.
// New start/reset aborts both stages. Results are consumed only on done.
// This serial wrapper deliberately does not accept another window while the
// normalizer is busy; a future window scheduler must honor ready.
// TRLC-LINKS: REQ-SDS-141
module stack_match_score #(parameter COUNT_BITS=32)(
 input wire clk,reset,start,valid,last,input wire [7:0] x,y,
 output wire ready,defined,invalid,overflow,
 output reg busy=0,done=0,output wire signed [49:0] score
);
 wire moment_ready,moment_busy,moment_done,score_busy,score_done,score_defined,score_invalid;
 wire [COUNT_BITS-1:0] count;wire [COUNT_BITS+7:0] sum_x,sum_y;wire [COUNT_BITS+15:0] sum_xx,sum_yy,sum_xy;
 assign ready=busy && moment_ready;
 assign defined=score_defined && !overflow;
 assign invalid=score_invalid || overflow;
 stack_moments #(.COUNT_BITS(COUNT_BITS)) moments(.clk(clk),.reset(reset),.start(start),.valid(valid && busy),.last(last),
  .x(x),.y(y),.ready(moment_ready),.busy(moment_busy),.done(moment_done),.overflow(overflow),
  .count(count),.sum_x(sum_x),.sum_y(sum_y),.sum_xx(sum_xx),.sum_yy(sum_yy),.sum_xy(sum_xy));
 stack_correlation #(.COUNT_BITS(COUNT_BITS)) correlation(.clk(clk),.reset(reset || start),.start(moment_done),
  .count({{(32-COUNT_BITS){1'b0}},count}),.sum_x({{(32-COUNT_BITS){1'b0}},sum_x}),.sum_y({{(32-COUNT_BITS){1'b0}},sum_y}),.sum_xx({{(32-COUNT_BITS){1'b0}},sum_xx}),.sum_yy({{(32-COUNT_BITS){1'b0}},sum_yy}),.sum_xy({{(32-COUNT_BITS){1'b0}},sum_xy}),
  .busy(score_busy),.done(score_done),.defined(score_defined),.invalid(score_invalid),.score(score));
 always @(posedge clk)begin
  done<=0;
  if(reset)busy<=0;
  else if(start)busy<=1;
  else if(score_done || (busy && overflow))begin busy<=0;done<=1;end
 end
endmodule
