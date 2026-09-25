// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Ordered Q48 score stream -> local-maximum candidates. The downstream segment
// verdict must resolve each candidate before input resumes. Only accepted
// candidates advance minimum separation. Neighbors support fractional alignment.
// start/reset cancel pending candidates; last flushes the right boundary.
// TRLC-LINKS: REQ-SDS-141
module stack_peaks(
 input wire clk,reset,start,
 input wire [31:0] first_position,min_separation,
 input wire signed [49:0] threshold,
 input wire valid,last,score_defined,score_invalid,
 input wire signed [49:0] score,
 output wire ready,candidate_valid,
 input wire verdict_valid,verdict_accept,
 output reg [31:0] candidate_position=0,
 output reg signed [49:0] candidate_score=0,left_score=0,right_score=0,
 output reg left_present=0,right_present=0,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,FIRST=1,NEXT=2,TAIL=3,VERDICT=4;
 reg [2:0] state=IDLE,resume_state=IDLE;
 reg [31:0] position=0,separation=1;
 reg [32:0] next_eligible=0;
 reg have_accepted=0,have_left=0;
 reg signed [49:0] floor=0,center=0,previous=0;
 wire signed [49:0] incoming=score_defined ? score:50'sd0;
 wire spaced=!have_accepted || {1'b0,position}>=next_eligible;
 wire peak=center>=floor && (!have_left || previous<center) && spaced;
 wire bad_score=score_invalid || (score_defined && (score>50'sh1000000000000 || score< -50'sh1000000000000));
 assign ready=!reset && !start && (state==FIRST || state==NEXT);
 assign candidate_valid=!reset && !start && state==VERDICT;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;end
  else if(start)begin
   position<=first_position;separation<=min_separation==0 ? 32'd1:min_separation;
   floor<=threshold;have_left<=0;have_accepted<=0;busy<=1;invalid<=0;state<=FIRST;
   if(threshold>50'sh1000000000000 || threshold< -50'sh1000000000000)begin
    state<=IDLE;busy<=0;done<=1;invalid<=1;
   end
  end else case(state)
   FIRST,NEXT:if(valid)begin
    if(bad_score || (state==NEXT && position==32'hffffffff))begin
     state<=IDLE;busy<=0;done<=1;invalid<=1;
    end else begin
     center<=incoming;state<=last ? TAIL:NEXT;
     if(state==NEXT)begin
      previous<=center;have_left<=1;position<=position+1'b1;
      if(peak && center>=incoming)begin
       candidate_position<=position;candidate_score<=center;
       left_score<=previous;left_present<=have_left;
       right_score<=incoming;right_present<=1;
       resume_state<=last ? TAIL:NEXT;state<=VERDICT;
      end
     end
    end
   end
   TAIL:begin
    if(peak)begin
     candidate_position<=position;candidate_score<=center;
     left_score<=previous;left_present<=have_left;
     right_score<=0;right_present<=0;resume_state<=IDLE;state<=VERDICT;
    end else begin state<=IDLE;busy<=0;done<=1;end
   end
   VERDICT:if(verdict_valid)begin
    if(verdict_accept)begin
     next_eligible<={1'b0,candidate_position}+{1'b0,separation};have_accepted<=1;
    end
    state<=resume_state;
    if(resume_state==IDLE)begin busy<=0;done<=1;end
   end
  endcase
 end
endmodule
