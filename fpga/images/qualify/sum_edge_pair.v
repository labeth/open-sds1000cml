// ENGMODEL-OWNER-UNIT: FU-RTL-QUAL-TRIGGER
`timescale 1ns/1ps
// The slope levels' crossings (ADR-HARDWARE-QUALIFIERS): sram_edge_pair's
// two-samples-a-clock edges with hysteresis, on wider values - the sums of
// five consecutive samples, one from each interleaved converter, so the
// converters' offsets cancel. A rise is a value at or above level after one
// at or below low_level; a fall a value at or below level after one at or
// above high_level. One clock of comparisons, one of edges.
// TRLC-LINKS: REQ-SDS-011
module sum_edge_pair #(parameter W=11)(
 input clk,enable,valid,
 input [W-1:0] first,second,          // first the earlier
 input [W-1:0] level,low_level,high_level,
 output reg rise=0,fall=0
);
 reg v=0,f_ge=0,f_le=0,f_lo=0,f_hi=0,s_ge=0,s_le=0,s_lo=0,s_hi=0;
 always @(posedge clk)begin
  v<=enable && valid;
  f_ge<=first>=level;f_le<=first<=level;f_lo<=first<=low_level;f_hi<=first>=high_level;
  s_ge<=second>=level;s_le<=second<=level;s_lo<=second<=low_level;s_hi<=second>=high_level;
 end
 reg rising_armed=0,falling_armed=0;
 // still armed, below (above) the level after the first value
 wire rise_after_first=(rising_armed || f_lo) && !f_ge;
 wire fall_after_first=(falling_armed || f_hi) && !f_le;
 always @(posedge clk)begin
  rise<=0;fall<=0;
  if(v)begin
   rise<=(rising_armed && f_ge) || (rise_after_first && s_ge);
   fall<=(falling_armed && f_le) || (fall_after_first && s_le);
   rising_armed<=(rise_after_first || s_lo) && !s_ge;
   falling_armed<=(fall_after_first || s_hi) && !s_le;
  end
  if(!enable)begin rising_armed<=0;falling_armed<=0;rise<=0;fall<=0;end
 end
endmodule

// five_sum: per clock the sums of the five samples ending at its first and at
// its second sample (first the earlier), over valid words only, three
// registered stages later.
// TRLC-LINKS: REQ-SDS-011
module five_sum(
 input clk,valid,
 input [7:0] first,second,
 output reg [10:0] sum_first=0,sum_second=0,
 output reg out_valid=0
);
 reg [7:0] h1=0,h2=0,h3=0,h4=0;                   // the last four samples, h1 the newest
 reg [8:0] a=0,b=0;reg [7:0] old1=0,new1=0,old2=0,new2=0;reg [9:0] c=0;reg v1=0,v2=0;
 always @(posedge clk)begin
  if(valid)begin h1<=second;h2<=first;h3<=h1;h4<=h2;end
  a<=h3+h2;b<=h1+first;old1<=h4;new1<=second;v1<=valid;
  c<=a+b;old2<=old1;new2<=new1;v2<=v1;
  sum_first<=c+old2;sum_second<=c+new2;out_valid<=v2;
 end
endmodule

