// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-SOUND: edge marks in a continuous stream. The trigger's edge
// comparator keeps running on the decimated words while the drum streams;
// this counts the stream's words from the stream's start and, on each
// crossing it is asked for, latches the count (the ordinal of the word the
// crossing falls in, within a word of it) and steps that direction's count.
// The host reads the newest mark of each direction and its count, so the
// FPGA decides every crossing the cycle view and a gate-armed envelope use;
// software only places the mark on the samples.
//
// Timing at 250 MHz: words are 2^8 clocks or more apart, so the 32-bit
// count steps in two stages - the low half's sum and carry, then both
// halves together - and a mark never sees the halves of different counts.
// The stream's start reaches the counters through a local register, off the
// drum's own fanout.
// TRLC-LINKS: REQ-SDS-272
module stream_marks(
 input clk,active,word,rise,fall,
 output reg [31:0] rise_word=0,fall_word=0,
 output reg [15:0] rise_count=0,fall_count=0
);
 (* preserve, dont_merge *) reg run=0;
 reg [15:0] lo=0,hi=0;
 reg [16:0] lo_next=0;
 reg step=0;
 always @(posedge clk)begin
  run<=active;
  step<=run && word;
  if(word)lo_next<={1'b0,lo}+1'b1;
  if(!run)begin
   lo<=0;hi<=0;rise_count<=0;fall_count<=0;step<=0;
  end else begin
   if(step)begin lo<=lo_next[15:0];hi<=hi+lo_next[16];end
   if(rise)begin rise_word<={hi,lo};rise_count<=rise_count+1'b1;end
   if(fall)begin fall_word<={hi,lo};fall_count<=fall_count+1'b1;end
  end
 end
endmodule
