// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Replays receiver words (hex, one 64-bit word a line) through the two edge
// extractors (sample_edges.v) and the UART (uart_sample.v) or I2C
// (i2c_sample.v) receiver and prints every edge, event and match with the
// index of the word presented on that clock; the app's Go tests
// (internal/decode, TestSampleUARTRTL, TestSampleI2CRTL) write the words
// from their model's fixtures and require the same, clock for clock.
// TRLC-LINKS: REQ-SDS-229
module tb_sample_rx;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,valid=1;reg [63:0] word=0;
 reg [63:0] mem[0:(1<<21)-1];
 integer n,i,proto,ch,level,low,high,inverted,delay,period,bits,parity,resync,pattern,len,address,direction;
 wire live_a,found_a,rising_a,other_a,live_b,found_b,rising_b,other_b;
 wire signed [7:0] when_a,when_b;wire [3:0] levels_a,levels_b;
 wire i2c=proto==2;
 sample_edges a(.clk(clk),.reset(reset),.valid(valid),.word(word),.ch(ch[0]),.rise_en(i2c ? !inverted[0] : 1'b1),.fall_en(i2c ? inverted[0] : 1'b1),
  .level(level[7:0]),.low(low[7:0]),.high(high[7:0]),.delay(delay[7:0]),.live(live_a),.found(found_a),.when(when_a),.rising(rising_a),.other(other_a),.levels(levels_a));
 sample_edges b(.clk(clk),.reset(reset),.valid(valid),.word(word),.ch(!ch[0]),.rise_en(1'b1),.fall_en(1'b1),
  .level(level[7:0]),.low(low[7:0]),.high(high[7:0]),.delay(-delay[7:0]),.live(live_b),.found(found_b),.when(when_b),.rising(rising_b),.other(other_b),.levels(levels_b));
 wire u_match,u_ev;wire [7:0] u_kind;wire [8:0] u_data;
 uart_sample uart(.clk(clk),.reset(reset),.enable(proto==1),.live(live_a),.found(found_a),.rising(rising_a),.when(when_a),.levels(levels_a),
  .inverted(inverted[0]),.resync(resync[0]),.period(period[23:0]),.bits(bits[3:0]),.parity(parity[1:0]),.pattern(pattern),.pattern_len(len[2:0]),
  .match(u_match),.event_valid(u_ev),.event_kind(u_kind),.event_data(u_data));
 wire i_match,i_ev,i_sel;wire [7:0] i_kind,i_data;wire [1:0] i_meta;
 i2c_sample i2cr(.clk(clk),.reset(reset),.enable(proto==2),.live(live_a && live_b),.scl_found(found_a),.scl_other(other_a),.scl_when(when_a),
  .sda_found(found_b),.sda_rising(rising_b),.sda_other(other_b),.sda_when(when_b),.delay(delay[7:0]),.inverted(inverted[0]),
  .address(address[6:0]),.any_address(address<0),.direction(direction[1:0]),.pattern(pattern),.pattern_len(len[2:0]),
  .match(i_match),.event_valid(i_ev),.event_kind(i_kind),.event_data(i_data),.event_meta(i_meta),.selected(i_sel));
 integer at=-1;
 reg [1023:0] input_path;
 initial begin
  if(!$value$plusargs("input=%s",input_path) || !$value$plusargs("n=%d",n) || !$value$plusargs("proto=%d",proto))$fatal(1,"usage: +input= +n= +proto=");
  if(!$value$plusargs("ch=%d",ch))ch=0;
  if(!$value$plusargs("level=%d",level))level=128;
  if(!$value$plusargs("low=%d",low))low=116;
  if(!$value$plusargs("high=%d",high))high=140;
  if(!$value$plusargs("inverted=%d",inverted))inverted=0;
  if(!$value$plusargs("delay=%d",delay))delay=0;
  if(!$value$plusargs("period=%d",period))period=320;
  if(!$value$plusargs("bits=%d",bits))bits=8;
  if(!$value$plusargs("parity=%d",parity))parity=0;
  if(!$value$plusargs("resync=%d",resync))resync=1;
  if(!$value$plusargs("pattern=%d",pattern))pattern=0;
  if(!$value$plusargs("len=%d",len))len=0;
  if(!$value$plusargs("address=%d",address))address=-1;
  if(!$value$plusargs("direction=%d",direction))direction=2;
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
 // Outputs after the clock that took word at-1 (the receivers take a word
 // at the clock after it is presented).
 always @(negedge clk)if(!reset)begin
  if(found_a)$display("EA %0d %0d %0d %0d %0d",at-1,when_a,rising_a,other_a,levels_a);
  if(found_b && i2c)$display("EB %0d %0d %0d %0d %0d",at-1,when_b,rising_b,other_b,levels_b);
  if(u_ev)$display("EV %0d %0d %0d 0 %0d",at-1,u_kind,u_data,u_match);
  if(i_ev)$display("EV %0d %0d %0d %0d 0",at-1,i_kind,i_data,i_meta);
  if(i_match)$display("M %0d",at-1);
 end
endmodule
