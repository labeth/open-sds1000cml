// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_bits against a reference comparator and packer, for
// periods of 8, 16 and 32 words, with gaps in the input: literal words for
// steady lines, a square wave and a one-sample glitch, then random lines
// with noise inside the band, edges on either sample and glitches.
// TRLC-LINKS: REQ-SDS-035
module tb_stream_bits;
 reg clk=0;always #2 clk=!clk;
 reg enable=0,in_valid=0;reg [4:0] log_words=0;reg [31:0] in_word=0;
 reg [7:0] low1=100,high1=140,low2=60,high2=90;
 wire out_valid;wire [31:0] out_word;
 stream_bits dut(.clk(clk),.enable(enable),.log_words(log_words),.low1(low1),.high1(high1),.low2(low2),.high2(high2),
  .in_valid(in_valid),.in_word(in_word),.out_valid(out_valid),.out_word(out_word));
 integer errors=0,outs=0,expect_n=0,gaps=1;
 reg [31:0] expect_q[0:8191];
 always @(posedge clk)if(out_valid)begin
  if(outs>=expect_n)begin $display("FAIL: extra output %h",out_word);errors=errors+1;end
  else if(out_word!==expect_q[outs])begin $display("FAIL: word %0d got %h want %h",outs,out_word,expect_q[outs]);errors=errors+1;end
  outs=outs+1;
 end
 // Reference: Schmitt state per channel, the period's difference, packing.
 reg st1,st2,rep1,rep2,dif1,dif2;reg [15:0] pk1,pk2;integer per_left,slot;
 task ref_reset;begin st1=0;st2=0;rep1=0;rep2=0;dif1=0;dif2=0;slot=0;per_left=1<<log_words;end endtask
 task ref_sample(input [7:0] a,b);begin
  if(a>=high1)st1=1;else if(a<=low1)st1=0;
  if(b>=high2)st2=1;else if(b<=low2)st2=0;
  if(st1!=rep1)dif1=1;if(st2!=rep2)dif2=1;
 end endtask
 task send(input [31:0] w);begin
  ref_sample(w[7:0],w[15:8]);ref_sample(w[23:16],w[31:24]);
  per_left=per_left-1;
  if(per_left==0)begin
   rep1=rep1^dif1;rep2=rep2^dif2;dif1=0;dif2=0;pk1[slot]=rep1;pk2[slot]=rep2;slot=slot+1;per_left=1<<log_words;
   if(slot==16)begin expect_q[expect_n]={pk2,pk1};expect_n=expect_n+1;slot=0;end
  end
  @(posedge clk);in_valid<=1;in_word<=w;
  if(gaps && ($urandom%5)==0)begin @(posedge clk);in_valid<=0;end
 end endtask
 task start(input integer lw);begin
  @(posedge clk);in_valid<=0;enable<=0;log_words<=lw;repeat(6)@(posedge clk);enable<=1;repeat(2)@(posedge clk);ref_reset;
 end endtask
 task finish;begin @(posedge clk);in_valid<=0;repeat(12)@(posedge clk);end endtask
 function [7:0] level(input lvl,input [7:0] lo,hi);level=lvl ? hi+8'd5 : lo-8'd5;endfunction
 function [31:0] steady(input a,b);steady={level(b,low2,high2),level(a,low1,high1),level(b,low2,high2),level(a,low1,high1)};endfunction
 integer i,j,n,base;reg a,b,a0,a1,b0,b1;reg [7:0] x0,x1,y0,y1;reg [31:0] t;
 initial begin
  // Literal words: CH1 high and CH2 low for 16 periods, then the reverse.
  gaps=0;start(3);
  for(i=0;i<16*8;i=i+1)send(steady(1,0));
  for(i=0;i<16*8;i=i+1)send(steady(0,1));
  // CH1 a square wave of 2 periods high, 2 low (its first period low);
  // CH2 low with a one-sample glitch high in period 5 only.
  for(i=0;i<16*8;i=i+1)begin
   t=steady((i/16)%2,0);
   if(i==5*8+3)t[31:24]=8'd200;
   send(t);
  end
  finish;
  if(expect_q[0]!==32'h0000ffff || expect_q[1]!==32'hffff0000 || expect_q[2]!==32'h0020cccc)begin
   $display("FAIL: literal words %h %h %h",expect_q[0],expect_q[1],expect_q[2]);errors=errors+1;end
  // Random lines, every period size.
  gaps=1;
  for(n=3;n<=5;n=n+1)begin
   start(n);a=0;b=0;
   for(i=0;i<(40*16)<<n;i=i+1)begin
    if(($urandom%(5<<n))==0)a=!a;if(($urandom%(3<<n))==0)b=!b;
    x0=level(a,low1,high1);x1=x0;y0=level(b,low2,high2);y1=y0;
    if(($urandom%7)==0)x1=level(!a,low1,high1);      // an edge between a word's samples
    if(($urandom%11)==0)y0=low2+1+($urandom%(high2-low2-1)); // inside the band: held
    if(($urandom%23)==0)x0=level(!a,low1,high1);     // a one-sample glitch
    if(($urandom%29)==0)y1=level(!b,low2,high2);
    send({y1,x1,y0,x0});
    if(x1!=level(a,low1,high1) && x0==level(a,low1,high1))a=!a;
   end
   finish;
  end
  if(outs!=expect_n)begin $display("FAIL: %0d outputs, want %0d",outs,expect_n);errors=errors+1;end
  if(errors==0)$display("PASS tb_stream_bits (%0d words)",outs);else $display("FAIL tb_stream_bits: %0d errors",errors);
  $finish;
 end
endmodule
