// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-PRECISION
`timescale 1ns/1ps
// Box decimation for the images without room for the precision filter
// (packet, frame, line, fast): each channel's 2^decim_log samples (two a raw
// word) averaged into one, written as the precision filter's words are
// ({C2, C1}, unsigned Q8.8), so the record spans a slow timebase's screen
// and the protocol receivers, which read the raw samples, trigger at any
// timebase. Register 30 tells the host the box (0x4258): no CIC
// compensation applies. decim_log is the core-domain copy, static while
// running (4..20).
//
// At 250 MHz the per-word loop is an adder whose operands are gated (in the
// adder's own logic cells): each channel's sum, parts of 10, 9 and 9 bits
// whose carries trail by a clock, starts over with a window's first word. A count of
// the words (from 1, so the first window is whole) marks a window's last
// word where its bit decim_log-1 turns (every 2^(decim_log-1) words: a
// one-hot mask in two halves, a stage each, the samples a stage behind to
// meet it). The sum is copied as it
// starts over; over the following clocks its parts are joined, shifted
// left 4 and then right decim_log-4 bits, one a clock: Q8.8 = sum << 8 >>
// decim_log, written at most 22 clocks after the copy, within the
// 2^(decim_log-1) words to the next (at least 8 at /16, with no shifts).
// Every step is decided a clock ahead into a flag (timing).
// TRLC-LINKS: REQ-SDS-040
module box_decim(input core,packclk,clk100,enable,input [4:0] decim_log,input [31:0] raw,input raw_valid,
 output [31:0] data,output reg valid=0,output fault);
 assign fault=1'b0;
 (* preserve, dont_merge *) reg run=0;
 reg [19:0] mask=0;reg [4:0] rights=0;
 integer b;
 always @(posedge core)begin
  run<=enable;rights<=decim_log-5'd4;
  for(b=0;b<20;b=b+1)mask[b]<=decim_log==b+1;
 end
 // stage 1: each channel's two samples of the word
 reg [8:0] p1=0,p2=0;reg v1=0;
 always @(posedge core)begin p1<=raw[7:0]+raw[23:16];p2<=raw[15:8]+raw[31:24];v1<=run && raw_valid;end
 // stage 2: the samples a stage on (with a valid per channel: fanout); the
 // word's count bit, a half at a time
 // The count is two 10-bit halves: the high one steps with the word that
 // wraps the low one, known a clock ahead (lo_top: the low half is at its
 // top) - no 20-bit carry at 250 MHz.
 reg [9:0] count_lo=1,count_hi=0;reg lo_top=0;reg m_lo=0,m_hi=0;reg v2=0;reg [8:0] q1=0,q2=0;
 (* preserve, dont_merge *) reg v21=0,v22=0;
 always @(posedge core)begin
  v2<=v1;v21<=v1;v22<=v1;q1<=p1;q2<=p2;
  m_lo<=|(count_lo & mask[9:0]);m_hi<=|(count_hi & mask[19:10]);
  if(!run)begin count_lo<=1;count_hi<=0;lo_top<=0;end
  else if(v1)begin
   count_lo<=count_lo+1'b1;lo_top<=count_lo==10'h3fe;
   if(lo_top)count_hi<=count_hi+1'b1;
  end
 end
 // stage 3: the sums take the word (starting over after a window's last);
 // whether the word is a window's last (its count bit turned). A sum is
 // three parts, 10, 9 and 9 bits, each carry a clock behind into the next.
 reg prev=0,mark=0;
 // whether the sums go on (from 0, and over after a window's last): decided
 // with mark into a register per channel (fanout, timing)
 (* preserve, dont_merge *) reg keep1=0,keep2=0;
 reg [10:0] a1=0,a2=0;reg [9:0] b1=0,b2=0;reg [8:0] c1=0,c2=0;
 wire last=v2 && (m_lo|m_hi)!=prev;
 always @(posedge core)begin
  if(!run)begin prev<=0;mark<=0;keep1<=0;keep2<=0;end
  else begin mark<=last;keep1<=!last;keep2<=!last;if(v2)prev<=m_lo|m_hi;end
  a1<=({1'b0,a1[9:0]} & {11{keep1}})+(q1 & {9{v21}});a2<=({1'b0,a2[9:0]} & {11{keep2}})+(q2 & {9{v22}});
  b1<=({1'b0,b1[8:0]} & {10{keep1}})+(a1[10] & keep1);b2<=({1'b0,b2[8:0]} & {10{keep2}})+(a2[10] & keep2);
  c1<=(c1 & {9{keep1}})+(b1[9] & keep1);c2<=(c2 & {9{keep2}})+(b2[9] & keep2);
 end
 // the copy as a sum starts over (its pending carries join next, over two
 // clocks), then the shifter: load, shift and done each a flag decided ahead
 reg [10:0] ca1=0,ca2=0;reg [9:0] cb1=0,cb2=0;reg [8:0] cc1=0,cc2=0;
 reg [9:0] jb1=0,jb2=0;
 reg [31:0] t1=0,t2=0;reg [4:0] left=0;reg j=0,ld=0,sh=0,done=0;
 always @(posedge core)begin
  if(mark)begin ca1<=a1;cb1<=b1;cc1<=c1;ca2<=a2;cb2<=b2;cc2<=c2;end
  j<=run && mark;ld<=run && j;
  if(j)begin jb1<={1'b0,cb1[8:0]}+ca1[10];jb2<={1'b0,cb2[8:0]}+ca2[10];end
  if(ld)begin
   t1<={cc1+cb1[9]+jb1[9],jb1[8:0],ca1[9:0],4'b0};t2<={cc2+cb2[9]+jb2[9],jb2[8:0],ca2[9:0],4'b0};
  end else if(sh)begin t1<={1'b0,t1[31:1]};t2<={1'b0,t2[31:1]};end
  if(ld)left<=rights;else if(sh)left<=left-1'b1;
  sh<=run && (ld ? rights!=5'd0 : sh && left!=5'd1);
  done<=run && (ld ? rights==5'd0 : sh && left==5'd1);
  valid<=done;
 end
 // written as the shifter holds it (taken on valid)
 assign data={t2[15:0],t1[15:0]};
endmodule
