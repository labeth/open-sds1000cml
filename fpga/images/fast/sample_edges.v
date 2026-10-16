// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Sample-domain edge extractor: finds one line's crossings in the converters'
// own codes, four samples a channel per 125 MHz receiver clock (two words of
// the interleaver), and times each by linear interpolation between the two
// samples around the level, in sixteenths of a nanosecond. A crossing is the
// first sample at or past the level after a sample beyond the re-arm band
// (rising: at or below low; falling: at or above high), so converter noise
// near the level makes one edge, not several. At most one edge a clock:
// lines up to 125 MHz. With each edge it takes the other line's level (its
// code at or above the level) at the sample nearest the crossing plus delay,
// for a receiver that samples that line at the edge (spi_sample.v). The
// app's Go model (internal/decode spi_sample.go) is this pipeline clock for
// clock.
// TRLC-LINKS: REQ-SDS-229
module sample_edges(
 input clk,reset,valid,
 input [63:0] word,             // {C2,C1} code pairs, the earliest pair lowest
 input ch,                      // the line whose edges are found: 0 C1, 1 C2
 input rising,                  // rising crossings, else falling
 input [7:0] level,low,high,
 input signed [7:0] delay,      // the other line's sample after the crossing, 1/16 ns on this line's sample grid
 output reg live=0,             // the outputs come from words (not from the reset's fill)
 output reg found=0,            // an edge in clock k's samples, seven clocks on
 output reg signed [7:0] when=0,// its crossing after clock k's first sample of the line, 1/16 ns, -32..96
 output reg other=0             // the other line's level at the crossing plus delay
);
 // Local copies of the held configuration beside the comparators (timing).
 (* preserve, dont_merge *) reg [7:0] lv=128,lo=124,hi=132;
 reg up=1,sel=0;
 reg signed [7:0] dl=0;
 always @(posedge clk)begin lv<=level;lo<=low;hi<=high;up<=rising;sel<=ch;dl<=delay;end
 // 0: the line's samples and the other line's.
 reg [7:0] s0[0:3],d0[0:3];reg v0=0;
 // 1: comparisons; the previous clock's last sample.
 reg [7:0] s1[0:3],last1=0;reg [3:0] hit1=0,far1=0,dl1=0;reg v1=0;
 integer j;
 initial for(j=0;j<4;j=j+1)begin s0[j]=0;d0[j]=0;s1[j]=0;end
 always @(posedge clk)begin
  v0<=valid && !reset;
  for(j=0;j<4;j=j+1)begin
   s0[j]<=sel ? word[16*j+8+:8] : word[16*j+:8];
   d0[j]<=sel ? word[16*j+:8] : word[16*j+8+:8];
  end
  v1<=v0 && !reset;last1<=s1[3];
  for(j=0;j<4;j=j+1)begin
   s1[j]<=s0[j];
   hit1[j]<=up ? s0[j]>=lv : s0[j]<lv;
   far1[j]<=up ? s0[j]<=lo : s0[j]>=hi;
   dl1[j]<=d0[j]>=lv;
  end
 end
 // 2: the re-arm chain across the clock's four samples; the first edge and
 // the two samples around it.
 reg armed=0;
 wire a0=armed;
 wire a1=(a0 && !hit1[0]) || far1[0];
 wire a2=(a1 && !hit1[1]) || far1[1];
 wire a3=(a2 && !hit1[2]) || far1[2];
 wire a4=(a3 && !hit1[3]) || far1[3];
 wire [3:0] fire={a3 && hit1[3],a2 && hit1[2],a1 && hit1[1],a0 && hit1[0]};
 reg e2=0;reg [1:0] p2=0;reg [7:0] sa2=0,sb2=0;
 always @(posedge clk)begin
  armed<=v1 && !reset && a4;
  e2<=v1 && !reset && fire!=0;
  if(fire[0])begin p2<=0;sa2<=last1;sb2<=s1[0];end
  else if(fire[1])begin p2<=1;sa2<=s1[0];sb2<=s1[1];end
  else if(fire[2])begin p2<=2;sa2<=s1[1];sb2<=s1[2];end
  else begin p2<=3;sa2<=s1[2];sb2<=s1[3];end
 end
 // 3: the interpolation's numerator and denominator (num <= den, den >= 1);
 // the other line's sample from the edge's position: 32*(p-1)+delay+16.
 reg e3=0;reg [1:0] p3=0;reg [7:0] num3=0,den3=1;reg signed [9:0] pd3=0;
 always @(posedge clk)begin
  e3<=e2 && !reset;p3<=p2;
  num3<=up ? lv-sa2 : sa2-lv;
  den3<=up ? sb2-sa2 : sa2-sb2;
  pd3<=$signed({1'b0,p2,5'd0})-10'sd16+dl;
 end
 // 4-6: q = floor(32*num/den), two quotient bits a clock (restoring).
 // TRLC-LINKS: REQ-SDS-229
 function [9:0] step(input [8:0] r,input [7:0] d); // {bit, remainder}: r < d, so 2r < 2d
  reg [9:0] t;begin t={r,1'b0}-{2'b0,d};step=({r,1'b0}>={2'b0,d}) ? {1'b1,t[8:0]} : {1'b0,r[7:0],1'b0};end
 endfunction
 reg v2=0,v3=0,v4=0,v5=0,v6=0;
 reg e4=0,e5=0,e6=0;reg [1:0] p4=0,p5=0;reg [7:0] den4=1,den5=1;reg [8:0] r4=0,r5=0;reg [1:0] q4=0;reg [3:0] q5=0;reg [5:0] q6=0;
 reg signed [9:0] pd4=0,pd5=0,pd6=0;reg signed [7:0] w6=0;
 wire q_top=num3==den3;
 wire [9:0] s4=step(q_top ? 9'd0 : {1'b0,num3},den3);
 wire [9:0] s5a=step(r4,den4);wire [9:0] s5b=step(s5a[8:0],den4);
 wire [9:0] s6a=step(r5,den5);wire [9:0] s6b=step(s6a[8:0],den5);
 wire [5:0] q={q5,s6a[9],s6b[9]};
 // the other line's levels, clock c's at hist[4*i+:4] i clocks of dl1 ago;
 // at the output its clocks k-2..k+1 around the edge's clock k
 reg [27:0] hist=0;
 wire [15:0] window={hist[15:12],hist[19:16],hist[23:20],hist[27:24]}; // bit 4*(c+2)+j
 wire signed [9:0] at=pd6+$signed({4'b0,q6});
 wire [3:0] index=at[8:5]+4'd8;                  // the nearest sample, -5..7 from the clock's first
 always @(posedge clk)begin
  e4<=e3 && !reset;p4<=p3;den4<=den3;q4<={q_top,s4[9]};r4<=s4[8:0];pd4<=pd3;
  e5<=e4 && !reset;p5<=p4;den5<=den4;q5<={q4,s5a[9],s5b[9]};r5<=s5b[8:0];pd5<=pd4;
  e6<=e5 && !reset;q6<=q;pd6<=pd5;w6<=$signed({1'b0,p5,5'd0})-8'sd32+$signed({2'b0,q});
  hist<={hist[23:0],dl1};
  v2<=v1 && !reset;v3<=v2 && !reset;v4<=v3 && !reset;v5<=v4 && !reset;v6<=v5 && !reset;live<=v6 && !reset;
  found<=e6 && !reset;when<=w6;other<=window[index];
 end
endmodule
