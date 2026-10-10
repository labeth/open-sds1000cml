// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_peak against a reference reduction, for bucket sizes
// 2, 4 and 8 words, with gaps in the input and one-sample spikes.
// TRLC-LINKS: REQ-SDS-035
module tb_stream_peak;
 reg clk=0;always #2 clk=!clk;
 reg enable=0,in_valid=0;reg [4:0] log_words=0;reg [31:0] in_word=0;
 wire out_valid;wire [31:0] out_word;
 stream_peak dut(.clk(clk),.enable(enable),.log_words(log_words),.in_valid(in_valid),.in_word(in_word),.out_valid(out_valid),.out_word(out_word));
 integer errors=0,outs=0,expect_n=0;
 reg [31:0] expect_q[0:4095];
 always @(posedge clk)if(out_valid)begin
  if(outs>=expect_n)begin $display("FAIL: extra output %h",out_word);errors=errors+1;end
  else if(out_word!==expect_q[outs])begin $display("FAIL: bucket %0d got %h want %h",outs,out_word,expect_q[outs]);errors=errors+1;end
  outs=outs+1;
 end
 task run(input integer lw,input integer buckets);
  integer b,k,size;reg [7:0] mn1,mx1,mn2,mx2,s;reg [31:0] w;
  begin
   size=1<<lw;
   @(posedge clk);enable<=0;log_words<=lw;repeat(4)@(posedge clk);enable<=1;repeat(2)@(posedge clk);
   for(b=0;b<buckets;b=b+1)begin
    mn1=255;mx1=0;mn2=255;mx2=0;
    for(k=0;k<size;k=k+1)begin
     w=$urandom;
     if(($urandom%17)==0)w[7:0]=8'd255;      // one-sample spike on CH1
     if(($urandom%19)==0)w[31:24]=8'd0;      // one-sample dip on CH2
     s=w[7:0];if(s<mn1)mn1=s;if(s>mx1)mx1=s;s=w[23:16];if(s<mn1)mn1=s;if(s>mx1)mx1=s;
     s=w[15:8];if(s<mn2)mn2=s;if(s>mx2)mx2=s;s=w[31:24];if(s<mn2)mn2=s;if(s>mx2)mx2=s;
     // Back to back as the ADC delivers, with occasional idle clocks.
     @(posedge clk);in_valid<=1;in_word<=w;
     if(($urandom%4)==0)begin @(posedge clk);in_valid<=0;end
    end
    expect_q[expect_n]={mx2,mx1,mn2,mn1};expect_n=expect_n+1;
   end
   @(posedge clk);in_valid<=0;
   repeat(8)@(posedge clk);
  end
 endtask
 initial begin
  run(1,40);run(2,40);run(3,60);
  if(outs!=expect_n)begin $display("FAIL: %0d outputs, want %0d",outs,expect_n);errors=errors+1;end
  if(errors==0)$display("PASS tb_stream_peak (%0d buckets)",outs);else $display("FAIL tb_stream_peak: %0d errors",errors);
  $finish;
 end
endmodule
