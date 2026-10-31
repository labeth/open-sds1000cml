// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-IMAGE: digital (thresholded) stream. Each channel's raw samples
// pass a Schmitt comparator (high at or above high, low at or below low,
// held between); one bit per period of 2^log_words raw words, sixteen periods
// per word: {CH2 bits 15..0, CH1 bits 15..0}, bit 0 the earliest period. At
// the peak stream's word rate this is sixteen times its time resolution, for
// a decoder that needs only a line's logic level.
//
// A period's bit is the last bit XOR "some sample in the period differed from
// the last bit": a real edge reads as the new level from the period it falls
// in, and a pulse shorter than a period still reads as one period of the
// other level, so a narrow glitch or clock pulse is never lost between
// samples. One bit per period instead of two (any-high, any-low) keeps twice
// the periods per word; an edge's place is within one period either way.
//
// Timing at 250 MHz: word, compares, the comparator state and the period's
// difference each take a registered stage; the period counter ends on its
// sign bit. A period is at least 8 raw words (log_words >= 3), so packing
// runs a clock behind the period's bit.
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-010
module stream_bits(
 input clk,enable,input [4:0] log_words, // raw words per period = 2^log_words, 3..15
 input [7:0] low1,high1,low2,high2,
 input in_valid,input [31:0] in_word,
 output reg out_valid=0,output reg [31:0] out_word=0
);
 // Local copies keep configuration fanout off the sample compares.
 (* preserve, dont_merge *) reg [7:0] l1=124,h1=132,l2=124,h2=132;
 // Stage 1: the word and its period's end (the counter preloaded with
 // period-2 reaches -1 on a period's last word).
 reg v1=0,e1=0;reg [31:0] w1=0;
 reg [15:0] size_r=0,init=0,left=0;
 // Stage 2: each sample at or past each band edge.
 reg v2=0,e2=0;reg hi10=0,lo10=0,hi11=0,lo11=0,hi20=0,lo20=0,hi21=0,lo21=0;
 // Stage 3: the comparator after each sample.
 reg v3=0,e3=0;reg s1=0,m1=0,s2=0,m2=0;
 // Stage 4: the period's difference from the last bit, and the bit.
 reg any1=0,any2=0,rep1=0,rep2=0,bit_v=0;
 // Packing.
 reg [14:0] sr1=0,sr2=0;reg [3:0] slot=0;
 wire st1=hi10 || (!lo10 && s1),st2=hi20 || (!lo20 && s2);
 wire d1=any1 || (m1^rep1) || (s1^rep1),d2=any2 || (m2^rep2) || (s2^rep2);
 always @(posedge clk)begin
  l1<=low1;h1<=high1;l2<=low2;h2<=high2;
  // Period size changes only while acquisition is idle: pipeline it.
  size_r<=16'd1<<log_words;init<=size_r-16'd2;
  v1<=enable && in_valid;w1<=in_word;e1<=enable && in_valid && left[15];
  if(!enable)left<=init;
  else if(in_valid)left<=left[15] ? init : left-1'b1;
  v2<=v1;e2<=e1;
  hi10<=w1[7:0]>=h1;lo10<=w1[7:0]<=l1;hi11<=w1[23:16]>=h1;lo11<=w1[23:16]<=l1;
  hi20<=w1[15:8]>=h2;lo20<=w1[15:8]<=l2;hi21<=w1[31:24]>=h2;lo21<=w1[31:24]<=l2;
  v3<=v2 && enable;e3<=e2 && enable;
  if(v2)begin
   m1<=st1;s1<=hi11 || (!lo11 && st1);
   m2<=st2;s2<=hi21 || (!lo21 && st2);
  end
  bit_v<=0;
  if(v3)begin
   any1<=d1 && !e3;any2<=d2 && !e3;
   if(e3)begin rep1<=rep1^d1;rep2<=rep2^d2;bit_v<=1;end
  end
  out_valid<=0;
  if(bit_v)begin
   sr1<={rep1,sr1[14:1]};sr2<={rep2,sr2[14:1]};slot<=slot+1'b1;
   if(&slot)begin out_word<={rep2,sr2,rep1,sr1};out_valid<=1;end
  end
  if(!enable)begin s1<=0;s2<=0;m1<=0;m2<=0;any1<=0;any2<=0;rep1<=0;rep2<=0;slot<=0;bit_v<=0;end
 end
endmodule
