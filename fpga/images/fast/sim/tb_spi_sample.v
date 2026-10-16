// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Replays receiver words (hex, one 64-bit word a line) through
// sample_edges.v and spi_sample.v and prints every edge and event with the
// index of the word that was presented on that clock; the app's Go test
// (internal/decode, TestSampleSPIRTL) writes the words from its model's
// fixtures and requires the same edges and events, clock for clock.
// TRLC-LINKS: REQ-SDS-229
module tb_spi_sample;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,valid=1;reg [63:0] word=0;
 reg [63:0] mem[0:(1<<21)-1];
 integer n,i,ch,rising,level,low,high,inverted,msb,delay,gap,pattern,len;
 wire live,found,other,dir;wire signed [7:0] when;wire [3:0] levels;
 wire match,ev;wire [7:0] kind,value;
 sample_edges edges(.clk(clk),.reset(reset),.valid(valid),.word(word),.ch(ch[0]),.rise_en(rising[0]),.fall_en(!rising[0]),
  .level(level[7:0]),.low(low[7:0]),.high(high[7:0]),.delay(delay[7:0]),.live(live),.found(found),.when(when),.rising(dir),.other(other),.levels(levels));
 spi_sample spi(.clk(clk),.reset(reset),.enable(1'b1),.live(live),.found(found),.other(other),
  .inverted(inverted[0]),.msb(msb[0]),.gap_ticks(gap[23:0]),
  .pattern(pattern),.pattern_len(len[2:0]),.match(match),.event_valid(ev),.event_kind(kind),.event_data(value));
 integer at=-1;
 reg [1023:0] input_path;
 initial begin
  if(!$value$plusargs("input=%s",input_path) || !$value$plusargs("n=%d",n))$fatal(1,"usage: +input= +n=");
  if(!$value$plusargs("ch=%d",ch))ch=1;
  if(!$value$plusargs("rising=%d",rising))rising=1;
  if(!$value$plusargs("level=%d",level))level=128;
  if(!$value$plusargs("low=%d",low))low=116;
  if(!$value$plusargs("high=%d",high))high=140;
  if(!$value$plusargs("inverted=%d",inverted))inverted=0;
  if(!$value$plusargs("msb=%d",msb))msb=1;
  if(!$value$plusargs("delay=%d",delay))delay=0;
  if(!$value$plusargs("gap=%d",gap))gap=4;
  if(!$value$plusargs("pattern=%d",pattern))pattern=0;
  if(!$value$plusargs("len=%d",len))len=0;
  $readmemh(input_path,mem,0,n-1);
  // Reset with zero words: the model starts from zeros.
  repeat(4)@(posedge clk);
  for(i=0;i<n;i=i+1)begin
   word<=mem[i];at<=i;
   if(i==0)reset<=0;
   @(posedge clk);
  end
  $display("DONE %0d",n);$finish;
 end
 // Outputs after the clock that took word at-1 (the receiver takes a word at
 // the clock after it is presented).
 always @(negedge clk)if(!reset)begin
  if(found)$display("EDGE %0d %0d",at-1,when);
  if(ev)$display("EV %0d %0d %0d %0d",at-1,kind,value,match);
 end
endmodule
