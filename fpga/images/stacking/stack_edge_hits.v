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
// A scan streams the record from the cache (stack_record_cache's stream), an
// entry of four samples a clock, and evaluates the entry's transitions in
// one clock up to the first crossing; the crossing stops the stream, and the
// scan restarts it at the next transition once the candidate is settled.
// ADR-STACKING-IMAGE-SPLIT. Memory ownership and reset are the caller's.
// TRLC-LINKS: REQ-SDS-141
module stack_edge_hits(
 input wire clk,reset,start,peek,
 input wire [31:0] first_index,record_samples,pre_samples,min_separation,
 input wire channel,falling,input wire [7:0] level,hysteresis,
 output wire request_valid,input wire request_ready,output wire [31:0] request_index,
 input wire response_valid,response_error,output wire response_ready,
 input wire [7:0] ch0_left,ch0_right,ch1_left,ch1_right,
 output reg stream_start=0,stream_stop=0,output wire [17:0] stream_entry,
 input wire entry_valid,input wire [63:0] entry_data,input wire stream_error,
 output wire candidate_valid,input wire candidate_ready,candidate_reject,
 output wire [31:0] candidate_crossing,
 output reg [31:0] candidate_position=0,output reg signed [24:0] candidate_delta=0,
 output reg [31:0] crossings=0,output reg [31:0] peek_data=0,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,REQUEST=1,WAIT=2,DIVIDE=3,PLACE=4,CHECK=5,HOLD=6,NEXT=7,VALIDATE=8,RANGE=10,SSTART=11,STREAM=12,FOUND=13,FOUND2=14;
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
 // Falling edges are rising edges of the complemented code (sample_k).
 wire [8:0] trial={remainder[7:0],1'b0};
 // The stream, in two registered stages (an entry a clock; the RAM's word
 // into a whole chain did not meet 125 MHz). Stage A takes the entry: each
 // sample of the channel scanned (complemented for falling edges) and its
 // comparisons with the band and the level, and whether each transition
 // (base+k-1, base+k) is inside the scan. Stage B runs the transitions in
 // order on those bits, from the sample before the entry (P_*), up to the
 // first crossing, which stops the stream; entries after it are dropped and
 // the scan restarts the stream at the next transition. skip drops the
 // samples before a restart's first within its entry.
 reg [1:0] skip=0;reg [W-1:0] base=0;
 reg A_v=0;reg [1:0] A_skip=0;
 // The entry a (re)start begins at: index holds still while the stream runs.
 assign stream_entry=index[W-1:2];
 reg [7:0] A_s0=0,A_s1=0,A_s2=0,A_s3=0;
 reg [3:0] A_low=0,A_below=0,A_lim=0,A_last=0; // at or above the level: !A_below
 reg P_have=0,P_low=0,P_below=0;reg [7:0] P_s=0;
 // The entry stage B ran, kept as it was (loaded every entry, off the
 // chain's path): FOUND takes the crossing's samples from these. Its base
 // is base less one entry, or two when stage A took one as it was found.
 reg [1:0] f_k=0;reg f_two=0;reg [7:0] f_p=0,f_s0=0,f_s1=0,f_s2=0,f_s3=0;
 wire [7:0] e0=sample_k(entry_data,channel_l,falling_l,0),e1=sample_k(entry_data,channel_l,falling_l,1),
  e2=sample_k(entry_data,channel_l,falling_l,2),e3=sample_k(entry_data,channel_l,falling_l,3);
 // A transition's right sample is at most last_index+1: the entry's room
 // past base, saturated at 4 (the transitions k with k <= room are inside).
 wire [W+1:0] room_full={2'b0,last_index}+2'd1-{2'b0,base};
 wire [2:0] room=room_full[W+1] ? 3'd7 : |room_full[W:2] ? 3'd4 : {1'b0,room_full[1:0]}; // 7: past the end
 reg c_found,c_armed,c_have,c_end,c_low,c_below;reg [1:0] c_k;
 integer k;
 always @* begin
  c_found=0;c_armed=armed;c_have=P_have;c_low=P_low;c_below=P_below;c_end=0;c_k=0;
  for(k=0;k<4;k=k+1)begin
   if(A_v && !c_found && !c_end && k>=A_skip)begin
    if(c_have)begin
     if(!A_lim[k])c_end=1;
     else begin
      // the transition (base+k-1, base+k): armed below the band, then a crossing
      // of the level
      if((c_armed || c_low) && c_below && !A_below[k])begin
       c_found=1;c_k=k;c_armed=0;
      end else c_armed=c_armed || c_low;
      if(A_last[k])c_end=1;
     end
    end
    c_low=A_low[k];c_below=A_below[k];c_have=1;
   end
  end
 end
 // The entry's sample n of the channel scanned (the complement for falling
 // edges). Everything it reads is an argument: @* does not see what a
 // function reads otherwise.
 function [7:0] sample_k(input [63:0] entry,input ch,input fall,input integer n);
  reg [7:0] v;
  begin
   v=ch ? entry[16*n+8 +: 8] : entry[16*n +: 8];
   sample_k=fall ? ~v : v;
  end
 endfunction
 assign request_valid=state==REQUEST && !reset;
 assign request_index=index;
 assign response_ready=state==WAIT && !reset;
 assign candidate_valid=state==HOLD && !reset;
 assign candidate_crossing={{(32-W){1'b0}},position};
 always @(posedge clk)begin
  done<=0;stream_start<=0;stream_stop<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;crossings<=0;stream_stop<=1;end
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
    else state<=peeking ? REQUEST : SSTART;
   // (Re)start the stream at the entry holding the transition's left sample.
   SSTART:begin
    stream_start<=1;base<={index[W-1:2],2'b00};skip<=index[1:0];
    P_have<=0;A_v<=0;state<=STREAM;
   end
   STREAM:if(stream_error)begin stream_stop<=1;invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else begin
     // Stage A: the entry's samples and their comparisons.
     A_v<=entry_valid;
     if(entry_valid)begin
      A_s0<=e0;A_s1<=e1;A_s2<=e2;A_s3<=e3;A_skip<=skip;skip<=0;base<=base+3'd4;
      A_low<={e3<=low_band,e2<=low_band,e1<=low_band,e0<=low_band};
      A_below<={e3<threshold,e2<threshold,e1<threshold,e0<threshold};
      A_lim<=room==7 ? 4'b0000 : room==4 ? 4'b1111 : room==3 ? 4'b1111 : room==2 ? 4'b0111 : room==1 ? 4'b0011 : 4'b0001;
      A_last<=room==3 ? 4'b1000 : room==2 ? 4'b0100 : room==1 ? 4'b0010 : room==0 ? 4'b0001 : 4'b0000;
     end
     // Stage B: the transitions of the entry stage A holds.
     if(A_v)begin
      // The entry's last sample is the next one's previous (a found or ended
      // scan restarts without it).
      P_have<=1;P_s<=A_s3;P_low<=A_low[3];P_below<=A_below[3];
      f_k<=c_k;f_two<=entry_valid;f_p<=P_s;f_s0<=A_s0;f_s1<=A_s1;f_s2<=A_s2;f_s3<=A_s3;
      if(c_found)begin
       stream_stop<=1;armed<=0;state<=FOUND;
      end else begin
       armed<=c_armed;
       if(c_end)begin stream_stop<=1;A_v<=0;done<=1;busy<=0;state<=IDLE;end
      end
     end
    end
   // The crossing's index and division from registers (timing).
   // The crossing's samples (the one before the entry for its
   // first transition), then the Q24 fraction, PLACE and CHECK.
   FOUND:begin
    index<=base-(f_two ? 4'd9 : 4'd5)+f_k;crossings<=crossings+1'b1;A_v<=0;
    a<=f_k==0 ? f_p : f_k==1 ? f_s0 : f_k==2 ? f_s1 : f_s2;
    b<=f_k==0 ? f_s0 : f_k==1 ? f_s1 : f_k==2 ? f_s2 : f_s3;
    state<=FOUND2;
   end
   FOUND2:begin divisor<=b-a;remainder<={1'b0,threshold-a};quotient<=0;step<=0;state<=DIVIDE;end
   REQUEST:if(request_ready)state<=WAIT;
   WAIT:if(response_valid)begin
    if(response_error)begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else begin peek_data<={ch1_right,ch0_right,ch1_left,ch0_left};done<=1;busy<=0;state<=IDLE;end
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
    else begin index<=index+1'b1;state<=SSTART;end
   end
   default:begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
  endcase
 end
endmodule
