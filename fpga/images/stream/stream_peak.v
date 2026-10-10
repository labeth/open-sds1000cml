// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-IMAGE: peak-detect stream. Reduces the raw interleaved ADC words
// (CH1[n],CH2[n],CH1[n+1],CH2[n+1]; one per 250 MHz clock) to one word per
// bucket of 2^log_words raw words, {max2,max1,min2,min1} -- the envelope
// recall's raw format, so the host de-biases and orders it the same way. A
// stream of extremes keeps narrow pulses and fast edges that the precision
// decimator's averaging removes, as a scope's peak-detect roll does.
//
// Timing at 250 MHz: words are first reduced in pairs, so the bucket
// accumulators (and the bucket counter) update at most every other clock from
// pair registers that hold for two clocks; build.ts gives those paths two
// clocks (multicycle). Buckets are whole pairs: log_words >= 1 (a stream's
// decimation is at least 2^8, so log_words >= 7).
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-010
module stream_peak(
 input clk,enable,input [4:0] log_words, // raw words per bucket = 2^log_words, 1..19
 input in_valid,input [31:0] in_word,
 output reg out_valid=0,output reg [31:0] out_word=0
);
 function [7:0] lo8(input [7:0] a,b);lo8=a<b?a:b;endfunction
 function [7:0] hi8(input [7:0] a,b);hi8=a>b?a:b;endfunction
 // Stage 1: the word.
 reg v1=0;reg [31:0] w1=0;
 // Stage 2: per channel extremes of its two samples.
 reg v2=0;reg [7:0] cmin1=0,cmax1=0,cmin2=0,cmax2=0;
 // Stage 3: extremes of two consecutive words (a pair), at most every 2
 // clocks: 3a registers the compares with both operands, 3b selects.
 reg half=0;reg [7:0] hmin1=0,hmax1=0,hmin2=0,hmax2=0;
 reg av=0;reg [7:0] amin1=0,amax1=0,amin2=0,amax2=0,bmin1=0,bmax1=0,bmin2=0,bmax2=0;
 reg lt1=0,gt1=0,lt2=0,gt2=0;
 reg pv=0,pv_d=0;reg [7:0] pmin1=0,pmax1=0,pmin2=0,pmax2=0;
 // Stage 4: the bucket, counted in pairs. A down-counter preloaded with
 // pairs-2 ends it on its sign bit; the output is a register copy one clock on.
 reg [7:0] min1=0,max1=0,min2=0,max2=0;
 reg [19:0] size_r=0,init=0,left=0;reg first=1,emit=0;
 wire [19:0] dec=left-1'b1;
 always @(posedge clk)begin
  // Bucket size changes only while acquisition is idle: pipeline it.
  size_r<=20'd1<<(log_words-1'b1);init<=size_r-20'd2;
  v1<=enable && in_valid;w1<=in_word;
  v2<=v1;
  cmin1<=lo8(w1[7:0],w1[23:16]);cmax1<=hi8(w1[7:0],w1[23:16]);
  cmin2<=lo8(w1[15:8],w1[31:24]);cmax2<=hi8(w1[15:8],w1[31:24]);
  av<=0;pv<=av;pv_d<=pv;
  if(!enable)half<=0;
  else if(v2)begin
   if(!half)begin hmin1<=cmin1;hmax1<=cmax1;hmin2<=cmin2;hmax2<=cmax2;end
   else begin
    amin1<=cmin1;amax1<=cmax1;amin2<=cmin2;amax2<=cmax2;bmin1<=hmin1;bmax1<=hmax1;bmin2<=hmin2;bmax2<=hmax2;
    lt1<=cmin1<hmin1;gt1<=cmax1>hmax1;lt2<=cmin2<hmin2;gt2<=cmax2>hmax2;av<=1;
   end
   half<=!half;
  end
  if(av)begin
   pmin1<=lt1 ? amin1 : bmin1;pmax1<=gt1 ? amax1 : bmax1;pmin2<=lt2 ? amin2 : bmin2;pmax2<=gt2 ? amax2 : bmax2;
  end
  out_valid<=emit;
  if(emit)out_word<={max2,max1,min2,min1};
  emit<=0;
  if(!enable)begin first<=1;left<=0;end
  else if(pv_d)begin
   if(first)begin min1<=pmin1;max1<=pmax1;min2<=pmin2;max2<=pmax2;left<=init;emit<=init[19];first<=init[19];end
   else begin min1<=lo8(min1,pmin1);max1<=hi8(max1,pmax1);min2<=lo8(min2,pmin2);max2<=hi8(max2,pmax2);left<=dec;emit<=dec[19];first<=dec[19];end
  end
 end
endmodule
