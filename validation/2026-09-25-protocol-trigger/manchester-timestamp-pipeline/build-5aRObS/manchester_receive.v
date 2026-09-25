// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two frame-local phase hypotheses. The caller identifies the first edge and
// the >2.5-bit inter-frame gap. Outputs are candidates, NEVER qualified events
// or triggers: a frame owner must buffer them and choose the final score.
// Configuration stays fixed from start through finish. bit_ticks >= 8.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module manchester_receive #(parameter START_DELAY=0)(
 input wire clk,reset,start,finish,tick,line,previous_level,ieee,msb,
 input wire [23:0] bit_ticks,input wire [4:0] word_bits,
 output wire [1:0] event_valid,event_error,done,overflow,
 output wire [31:0] event_data,event_cell,
 output wire signed [19:0] score_a,score_b,
 output wire [16:0] good_a,good_b
);
 reg active=0,phase=0,first_a=0,first_b=0;
 reg [25:0] remaining=0;
 wire sample=active && tick && remaining<=4 && !start && !finish;
 // Quarter-cell positions use a Q2 countdown. Fractional remainders survive
 // reload, so odd bit periods do not accumulate a half-cell timing error.
 always @(posedge clk)begin
  if(reset || finish)active<=0;
  else if(start)begin
   active<=bit_ticks>=8;phase<=0;
   remaining<={2'b0,bit_ticks}-((START_DELAY && tick) ? 26'd4 : 26'd0);
   first_a<=0;first_b<=previous_level;
  end else if(active && tick)begin
   if(sample)begin
    remaining<=remaining+{1'b0,bit_ticks,1'b0}-26'd4;
    phase<=!phase;
    if(!phase)first_a<=line;else first_b<=line;
   end else remaining<=remaining-26'd4;
  end
 end
 manchester_hypothesis a(.clk(clk),.reset(reset),.start(start),.finish(finish),
  .cell_valid(sample && phase),.first_level(first_a),.second_level(line),
  .ieee(ieee),.msb(msb),.word_bits(word_bits),.event_valid(event_valid[0]),
  .event_error(event_error[0]),.event_data(event_data[15:0]),.event_cell(event_cell[15:0]),
  .score(score_a),.good(good_a),.done(done[0]),.overflow(overflow[0]));
 manchester_hypothesis b(.clk(clk),.reset(reset),.start(start),.finish(finish),
  .cell_valid(sample && !phase),.first_level(first_b),.second_level(line),
  .ieee(ieee),.msb(msb),.word_bits(word_bits),.event_valid(event_valid[1]),
  .event_error(event_error[1]),.event_data(event_data[31:16]),.event_cell(event_cell[31:16]),
  .score(score_b),.good(good_b),.done(done[1]),.overflow(overflow[1]));
endmodule

// recoverManchester's scoring and word packing for one phase. One missing
// mid-cell transition is a coding error; two consecutive ones terminate the
// hypothesis and discard the provisional idle error. Dangling words still
// produce an error, as in the repository decoder. At most 65536 cells/frame.
// A caller must reject a frame on overflow, including any earlier candidates.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module manchester_hypothesis(
 input wire clk,reset,start,finish,cell_valid,first_level,second_level,ieee,msb,
 input wire [4:0] word_bits,
 output reg event_valid=0,event_error=0,
 output reg [15:0] event_data=0,event_cell=0,
 output reg signed [19:0] score=0,output reg [16:0] good=0,
 output reg done=1,overflow=0
);
 reg [15:0] index=0,word_value=0,last_good=0,invalid_index=0,saved_index=0;
 reg [4:0] used=0;
 reg pending_invalid=0,pending_bit=0,saved_bit=0,exhausted=0,ending=0;
 wire bit_value=second_level==ieee;
 wire [15:0] next_word=msb ? {word_value[14:0],bit_value} :
   word_value | ({15'd0,bit_value}<<used);
 wire [15:0] saved_word={15'd0,saved_bit};
 always @(posedge clk)begin
  event_valid<=0;event_error<=0;
  if(reset || start)begin
   index<=0;word_value<=0;last_good<=0;invalid_index<=0;saved_index<=0;
   used<=0;pending_invalid<=0;pending_bit<=0;saved_bit<=0;exhausted<=0;ending<=0;
   score<=0;good<=0;overflow<=0;done<=reset || word_bits<1 || word_bits>16;
  end else if(!done)begin
   if(finish)ending<=1;
   if(pending_bit)begin
    // An isolated coding error and the following bit require two output
    // cycles for one-bit words. Minimum cell spacing leaves room for both.
    pending_bit<=0;word_value<=saved_word;last_good<=saved_index;
    if(word_bits==1)begin
     used<=0;word_value<=0;event_valid<=1;event_data<=saved_word;event_cell<=saved_index;
    end else used<=1;
    if(cell_valid)begin overflow<=1;done<=1;end
   end else if(finish || ending)begin
    done<=1;
    if(used!=0)begin event_valid<=1;event_error<=1;event_data<=0;event_cell<=last_good;end
   end else if(cell_valid)begin
    if(exhausted)begin overflow<=1;done<=1;end
    else begin
     if(index==16'hffff)exhausted<=1;else index<=index+1'b1;
     if(first_level==second_level)begin
      if(pending_invalid)begin
       done<=1;pending_invalid<=0;
       if(used!=0)begin event_valid<=1;event_error<=1;event_data<=0;event_cell<=last_good;end
      end else begin pending_invalid<=1;invalid_index<=index;end
     end else begin
      good<=good+1'b1;last_good<=index;
      if(pending_invalid)begin
       score<=score-20'sd3;pending_invalid<=0;used<=0;word_value<=0;
       event_valid<=1;event_error<=1;event_data<=0;event_cell<=invalid_index;
       pending_bit<=1;saved_bit<=bit_value;saved_index<=index;
      end else begin
       score<=score+20'sd1;word_value<=next_word;
       if(used+1'b1==word_bits)begin
        used<=0;word_value<=0;event_valid<=1;event_data<=next_word;event_cell<=index;
       end else used<=used+1'b1;
      end
     end
    end
   end
  end
 end
endmodule
