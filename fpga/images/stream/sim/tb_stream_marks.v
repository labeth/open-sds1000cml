// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_marks counts the stream's words across the 16-bit
// wrap, latches the count on each crossing asked for (the ordinal of the
// word it falls in: the words before it), steps each direction's count, and
// starts again from zero with a new stream.
// TRLC-LINKS: REQ-SDS-272
module tb_stream_marks;
 reg clk=0;always #2 clk=!clk;
 reg active=0,word=0,rise=0,fall=0;
 wire [31:0] rw,fw;wire [15:0] rc,fc;
 stream_marks dut(.clk(clk),.active(active),.word(word),.rise(rise),.fall(fall),.rise_word(rw),.fall_word(fw),.rise_count(rc),.fall_count(fc));
 integer errors=0,k,words=0,marks=0;
 // one word, then four idle clocks (a stream's words are clocks apart)
 task one_word;begin @(posedge clk);word<=1;@(posedge clk);word<=0;repeat(3)@(posedge clk);words=words+1;end endtask
 // a crossing between words: the latched ordinal is the words so far
 task crossing(input up);begin
  @(posedge clk);if(up)rise<=1;else fall<=1;@(posedge clk);rise<=0;fall<=0;@(posedge clk);@(posedge clk);
  marks=marks+1;
  if(up && (rw!==words || rc!==marks[15:0]))begin $display("FAIL: rise at %0d: word %0d count %0d (want %0d)",words,rw,rc,marks);errors=errors+1;end
  if(!up && fw!==words)begin $display("FAIL: fall at %0d: word %0d",words,fw);errors=errors+1;end
 end endtask
 initial begin
  repeat(4)@(posedge clk);active<=1;
  for(k=0;k<70000;k=k+1)begin
   one_word;
   if(k==0 || k==500 || k==65533 || k==65534 || k==65535 || k==65536 || k==69999)crossing(1);
   if(k==3 || k==65535)begin marks=marks-1;crossing(0);end
  end
  // a mark in the clock after a word, before its count commits, reads the words before it
  @(posedge clk);word<=1;rise<=1;@(posedge clk);word<=0;rise<=0;repeat(3)@(posedge clk);
  if(rw!==words)begin $display("FAIL: mark beside a word read %0d, want %0d",rw,words);errors=errors+1;end
  words=words+1;
  // a new stream starts at zero
  @(posedge clk);active<=0;@(posedge clk);@(posedge clk);active<=1;words=0;marks=0;
  repeat(2)one_word;
  crossing(1);
  if(errors==0)$display("PASS: tb_stream_marks");else $display("FAIL: %0d errors",errors);
  $finish;
 end
endmodule
