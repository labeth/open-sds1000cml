// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Explicit-rate bipolar RZ receiver. Configuration is immutable while enabled.
// tick accepts one chronological ADC code and its absolute raw sample ordinal.
// Thresholds are precomputed by the host; no binary slicer may precede this core.
// Complete words use the Go decoder's nearest-cell and <=34 pulse density rules.
// Publication waits for a >2.5 bit-period pulse-start gap: START, DATA/ERROR, END.
// DATA is the entire little-endian transmission-order word at its first pulse;
// END is timestamped at the final in-window pulse, not at delayed publication.
// Partial/noisy words publish nothing. Bad parity publishes ERROR and cannot hit.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module arinc429_trigger(
 input wire clk,reset,enable,tick,
 input wire [7:0] code,threshold_hi,threshold_lo,exit_hi,exit_lo,
 input wire [23:0] bit_ticks,
 input wire [63:0] sample,
 input wire [31:0] pattern,pattern_mask,
 output reg match=0,event_valid=0,
 output reg [7:0] event_kind=0,
 output reg [31:0] event_data=0,
 output reg [63:0] event_sample=0
);
 localparam NULL_LEVEL=2'd0,HI_LEVEL=2'd1,LO_LEVEL=2'd2;
 reg [1:0] level=NULL_LEVEL,next_level;
 reg pulse,positive;
 always @* begin
  next_level=level;pulse=0;positive=0;
  case(level)
   HI_LEVEL:begin
    if(code<=threshold_lo)begin next_level=LO_LEVEL;pulse=1;end
    else if(code<exit_hi)next_level=NULL_LEVEL;
   end
   LO_LEVEL:begin
    if(code>=threshold_hi)begin next_level=HI_LEVEL;pulse=1;positive=1;end
    else if(code>exit_lo)next_level=NULL_LEVEL;
   end
   default:begin
    if(code>=threshold_hi)begin next_level=HI_LEVEL;pulse=1;positive=1;end
    else if(code<=threshold_lo)begin next_level=LO_LEVEL;pulse=1;end
   end
  endcase
 end
 wire valid_config=bit_ticks>=4 && threshold_lo<exit_lo &&
  exit_lo<=exit_hi && exit_hi<threshold_hi;
 wire [25:0] gap_limit=({2'b0,bit_ticks}<<1)+({2'b0,bit_ticks}>>1);
 reg active=0;
 reg [25:0] gap=0;
 reg [23:0] phase=0;
 reg [5:0] slot=0,hits=0;
 reg [31:0] seen=0,word=0;
 reg [63:0] first_sample=0,last_sample=0;
 wire rollover=phase==bit_ticks-1'b1;
 wire [5:0] current_slot=(rollover && slot<32)?slot+1'b1:slot;
 wire close_word=active && gap>=gap_limit;
 reg [1:0] publish=0;
 reg [31:0] held_word=0;
 reg [63:0] held_first=0,held_last=0;
 reg held_good=0,held_hit=0;
 always @(posedge clk)begin
  event_valid<=0;match<=0;
  if(reset || !enable || !valid_config)begin
   level<=NULL_LEVEL;active<=0;gap<=0;phase<=0;slot<=0;hits<=0;
   seen<=0;word<=0;first_sample<=0;last_sample<=0;
   publish<=0;held_word<=0;held_first<=0;held_last<=0;held_good<=0;held_hit<=0;
  end else begin
   // Drain independently of accepted sample ticks. At least 32 cells separate
   // complete words, so this three-clock publication cannot overlap another.
   if(publish==2)begin
    event_valid<=1;event_kind<=held_good?8'd2:8'd4;
    event_data<=held_word;event_sample<=held_first;publish<=1;
   end else if(publish==1)begin
    event_valid<=1;event_kind<=3;event_data<=held_good?0:1;
    event_sample<=held_last;match<=held_good && held_hit;publish<=0;
   end
   if(tick)begin
    level<=next_level;
    if(active)begin
     if(gap<26'h3ffffff)gap<=gap+1'b1;
     if(rollover)begin phase<=0;if(slot<32)slot<=slot+1'b1;end
     else phase<=phase+1'b1;
    end
    if(close_word)begin
     active<=0;
     if(&seen && hits<=34)begin
      held_word<=word;held_first<=first_sample;held_last<=last_sample;
      held_good<=^word;held_hit<=((word & pattern_mask)==(pattern & pattern_mask));
      event_valid<=1;event_kind<=1;event_data<=0;event_sample<=first_sample;
      publish<=2;
     end
    end
    if(pulse)begin
     gap<=0;
     if(!active || close_word)begin
      active<=1;phase<=bit_ticks>>1;slot<=0;hits<=1;
      seen<=1;word<={31'd0,positive};first_sample<=sample;last_sample<=sample;
     end else if(current_slot<32)begin
      seen[current_slot]<=1;word[current_slot]<=positive;last_sample<=sample;
      if(hits<35)hits<=hits+1'b1;
     end
    end
   end
  end
 end
endmodule
