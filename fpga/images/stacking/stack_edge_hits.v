// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Edge-locked hit detector over a retained record. Reads adjacent sample pairs
// in order, arms below level-hysteresis (above level+hysteresis when falling),
// and reports a crossing at the linearly interpolated position. The candidate
// position is pre_samples before the crossing; delta is signed Q24 within
// +/-0.5 sample. Crossings closer than min_separation to the previous reported
// one, or before pre_samples, are skipped. Each candidate is held until
// candidate_ready (accepted: restarts the separation hold-off) or
// candidate_reject (skipped: the hold-off keeps its previous reference).
// candidate_crossing is the rounded crossing sample the hit is locked to.
// peek reads one pair at first_index and stops.
// ADR-STACKING-IMAGE-SPLIT. Memory ownership and reset are the caller's.
// TRLC-LINKS: REQ-SDS-141
module stack_edge_hits(
 input wire clk,reset,start,peek,
 input wire [31:0] first_index,record_samples,pre_samples,min_separation,
 input wire channel,falling,input wire [7:0] level,hysteresis,
 output wire request_valid,input wire request_ready,output wire [31:0] request_index,
 input wire response_valid,response_error,output wire response_ready,
 input wire [7:0] ch0_left,ch0_right,ch1_left,ch1_right,
 output wire candidate_valid,input wire candidate_ready,candidate_reject,
 output wire [31:0] candidate_crossing,
 output reg [31:0] candidate_position=0,output reg signed [24:0] candidate_delta=0,
 output reg [31:0] crossings=0,output reg [31:0] peek_data=0,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,REQUEST=1,WAIT=2,DIVIDE=3,PLACE=4,CHECK=5,HOLD=6,NEXT=7,VALIDATE=8,EVALUATE=9,RANGE=10;
 reg [3:0] state=IDLE;
 reg [31:0] samples_l=0,first_l=0,last_l=0;reg samples_bad=0;
 // Records hold at most 2^20 samples per channel, so positions need 21 bits.
 // Wider pre/separation settings saturate, which preserves every comparison.
 localparam W=21;
 function [W-1:0] saturate(input [31:0] x);saturate=|x[31:W] ? {W{1'b1}} : x[W-1:0];endfunction
 reg [W-1:0] index=0,last_index=0,pre=0,separation=0,last=0,position=0;
 reg have_last=0,armed=0,peeking=0,channel_l=0,falling_l=0;
 reg [7:0] threshold=0,low_band=0,a=0,b=0;
 reg [8:0] remainder=0;reg [7:0] divisor=0;
 reg [24:0] quotient=0;reg [4:0] step=0;
 wire [7:0] left=channel_l ? ch1_left : ch0_left;
 wire [7:0] right=channel_l ? ch1_right : ch0_right;
 // Falling edges are rising edges of the complemented code.
 wire [7:0] left_v=falling_l ? ~left : left,right_v=falling_l ? ~right : right;
 // The response is registered into a/b first; decisions use those copies.
 wire arm_now=armed || a<=low_band;
 wire [8:0] trial={remainder[7:0],1'b0};
 assign request_valid=state==REQUEST && !reset;
 assign request_index=index;
 assign response_ready=state==WAIT && !reset;
 assign candidate_valid=state==HOLD && !reset;
 assign candidate_crossing={{(32-W){1'b0}},position};
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;crossings<=0;end
  else case(state)
   IDLE:if(start || peek)begin
    peeking<=peek;index<=first_index[W-1:0];last_index<=record_samples[W-1:0]-2'd2;
    pre<=saturate(pre_samples);separation<=saturate(min_separation);channel_l<=channel;falling_l<=falling;
    threshold<=falling ? ~level : level;
    low_band<=(falling ? ~level : level)>hysteresis ? (falling ? ~level : level)-hysteresis : 8'd0;
    armed<=0;have_last<=0;crossings<=0;invalid<=0;busy<=1;
    samples_l<=record_samples;first_l<=first_index;state<=RANGE;
   end
   // Geometry is checked from registered copies, off the start fanout.
   // Range flags and the last pair index first, then the comparison.
   RANGE:begin samples_bad<=samples_l<2 || samples_l>32'h100000;last_l<=samples_l-2'd2;state<=VALIDATE;end
   VALIDATE:if(samples_bad || first_l>last_l)begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else state<=REQUEST;
   REQUEST:if(request_ready)state<=WAIT;
   WAIT:if(response_valid)begin
    if(response_error)begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else if(peeking)begin peek_data<={ch1_right,ch0_right,ch1_left,ch0_left};done<=1;busy<=0;state<=IDLE;end
    else begin a<=left_v;b<=right_v;state<=EVALUATE;end
   end
   EVALUATE:begin
    armed<=arm_now;
    if(arm_now && a<threshold && b>=threshold)begin
     // Q24 fraction (level-a)/(b-a) in (0,1]; the first quotient bit is 2^24.
     armed<=0;divisor<=b-a;remainder<={1'b0,threshold-a};quotient<=0;step<=0;
     crossings<=crossings+1'b1;state<=DIVIDE;
    end else state<=NEXT;
   end
   DIVIDE:begin
    if(step==0)begin
     if(remainder[7:0]>=divisor)begin remainder<=remainder-divisor;quotient<=25'h1000000;end
    end else if(trial>={1'b0,divisor})begin remainder<=trial-divisor;quotient[24-step]<=1'b1;end
    else remainder<=trial;
    step<=step+1'b1;
    if(step==24)state<=PLACE;
   end
   PLACE:begin
    // Round to the nearer sample so |delta| <= 0.5.
    if(quotient>25'h0800000)begin position<=index+1'b1;candidate_delta<=$signed(quotient)-$signed(25'h1000000);end
    else begin position<=index;candidate_delta<=$signed(quotient);end
    state<=CHECK;
   end
   CHECK:begin
    if(position<pre || (have_last && position-last<separation))state<=NEXT;
    else begin candidate_position<={{(32-W){1'b0}},position-pre};state<=HOLD;end
   end
   HOLD:if(candidate_ready)begin last<=position;have_last<=1;state<=NEXT;end
    else if(candidate_reject)state<=NEXT;
   NEXT:begin
    if(index==last_index)begin done<=1;busy<=0;state<=IDLE;end
    else begin index<=index+1'b1;state<=REQUEST;end
   end
   default:begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
  endcase
 end
endmodule
