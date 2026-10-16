// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Two-wire SPI receiver on the sample-domain edges of sample_edges.v: each
// sampling edge of the clock line takes the data line's sample nearest to
// the edge's interpolated crossing plus a delay, so it decodes to the edge
// rate sample_edges.v resolves (a clock up to 125 MHz) and every lower rate
// alike. No chip-select channel is available: an idle gap of
// gap_ticks receiver clocks without a sampling edge frames a transaction.
// Events and the pattern match are spi_trigger.v's, so the decoded-event
// transport and the sequence trigger take them unchanged. Configuration is
// held while enabled.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
module spi_sample(
 input wire clk,reset,enable,
 input wire live,found,other,       // sample_edges.v: the clock line's sampling edge and the data line at it
 input wire inverted,msb,
 input wire [23:0] gap_ticks,
 input wire [31:0] pattern,input wire [2:0] pattern_len,
 output reg match=0,event_valid=0,
 output reg [7:0] event_kind=0,event_data=0
);
 reg primed=0,active=0,framed=0;
 reg valid_config=0;
 always @(posedge clk)valid_config<=gap_ticks>=2 && pattern_len<=4;
 reg [23:0] idle=0;
 reg [2:0] bits=0,count=0;
 reg [7:0] shift=0;
 reg [31:0] history=0;
 wire sample_edge=primed && found;
 wire data=other^inverted;
 wire [7:0] received=msb ? {shift[6:0],data} : {data,shift[7:1]};
 wire [31:0] next_history={history[23:0],received};
 wire hit=pattern_len==0 ||
  (pattern_len==1 && next_history[7:0]==pattern[7:0]) ||
  (pattern_len==2 && count>=1 && next_history[15:0]==pattern[15:0]) ||
  (pattern_len==3 && count>=2 && next_history[23:0]==pattern[23:0]) ||
  (pattern_len==4 && count>=3 && next_history==pattern);
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  // Until words have passed the pipeline (after a reset, or a gap in the
  // words) no idle gap is counted: the line's own gap frames a transaction.
  if(reset || !enable || !valid_config || !live)begin
   primed<=0;active<=0;framed<=0;idle<=0;bits<=0;count<=0;history<=0;shift<=0;
  end else begin
   primed<=1;
   // With no CS input, an epoch beginning mid-byte has unknown alignment.
   // Observe an idle gap before admitting the first frame or trigger suffix.
   if(primed && idle<gap_ticks)idle<=idle+1'b1;
   if(primed && idle>=gap_ticks)framed<=1;
   if(sample_edge)idle<=0;
   if(active && idle>=gap_ticks)begin
    active<=0;bits<=0;count<=0;history<=0;shift<=0;
    event_valid<=1;event_kind<=bits==0 ? 8'd3 : 8'd4;event_data<=0;
   end
   if(sample_edge && (framed || idle>=gap_ticks))begin
    idle<=0;active<=1;
    if(!active || idle>=gap_ticks)begin
     // First sampled bit of a frame. A stale partial byte and suffix must
     // never contribute to the new transaction's trigger predicate.
     shift<=msb ? {7'd0,data} : {data,7'd0};bits<=1;count<=0;history<=0;
     event_valid<=1;event_kind<=1;event_data<=0;
    end else begin
     shift<=received;bits<=bits+1'b1;
     if(bits==7)begin
      event_valid<=1;event_kind<=2;event_data<=received;
      history<=next_history;if(count<4)count<=count+1'b1;
      match<=hit;
     end
    end
   end
  end
 end
endmodule

// The fast image's SPI trigger: the acquisition words (two samples a
// channel on the 250 MHz clock) paired for the 125 MHz receiver clock, the
// edge extractor on the clock line and the receiver.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
module spi_sample_trigger(
 input wire core,clk,reset,enable,
 input wire [31:0] word,input wire valid,  // core clock: CH1, CH2, CH1, CH2 bytes, the earliest lowest
 input wire clock_ch,inverted,cpol,cpha,msb,
 input wire [7:0] level,low,high,delay,    // delay: spi_sample.v's, held (host clock)
 input wire [23:0] gap_ticks,
 input wire [31:0] pattern,input wire [2:0] pattern_len,
 output wire match,event_valid,
 output wire [7:0] event_kind,event_data
);
 // The receiver clock (in phase, half the rate) takes the two words since
 // its last edge.
 reg [31:0] w0=0,w1=0;reg v0=0,v1=0;
 always @(posedge core)begin w0<=word;w1<=w0;v0<=valid;v1<=v0;end
 reg [7:0] delay_h=0;
 always @(posedge clk)delay_h<=delay;
 wire live,found,other;wire signed [7:0] when;
 sample_edges edges(.clk(clk),.reset(reset || !enable),.valid(v0 && v1),.word({w0,w1}),.ch(clock_ch),.rising((cpol==cpha)!=inverted),
  .level(level),.low(low),.high(high),.delay(delay_h),.live(live),.found(found),.when(when),.other(other));
 spi_sample receiver(.clk(clk),.reset(reset),.enable(enable),.live(live),.found(found),.other(other),
  .inverted(inverted),.msb(msb),.gap_ticks(gap_ticks),.pattern(pattern),.pattern_len(pattern_len),
  .match(match),.event_valid(event_valid),.event_kind(event_kind),.event_data(event_data));
endmodule
