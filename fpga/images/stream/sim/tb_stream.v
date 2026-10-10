// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_packetizer + stream_banks across three unrelated
// clocks, drained by a host model that follows sramcapture.DrainStream
// (find the bank whose first ordinal is the next expected word, read count and
// last-single, check the words, release with the token read).
// TRLC-LINKS: REQ-SDS-035
module tb_stream;
 localparam AW=4,BANK=1<<AW;
 reg wclk=0,pclk=0,hclk=0;
 always #2 wclk=!wclk;     // 250 MHz acquisition
 always #4 pclk=!pclk;     // 125 MHz packet
 always #6.25 hclk=!hclk;  // 80 MHz host
 reg reset=1,word_valid=0,stop=0;reg [31:0] word_data=0;
 wire fault,finished,pvalid,psingle,pseal,seal_ok;wire [63:0] pdata;
 stream_packetizer #(.SEAL_CYCLES(300),.STOP_SETTLE(15)) pk(.reset(reset),.word_clk(wclk),.packet_clk(pclk),.word_valid(word_valid),
  .word_data(word_data),.stop(stop),.seal_ok(seal_ok),.fault(fault),.finished(finished),.packet_valid(pvalid),.packet_data(pdata),
  .packet_single(psingle),.packet_seal(pseal));
 wire mw,overrun,host_overrun;wire [AW:0] ma;wire [63:0] md;
 reg rel=0,rel_bank=0,rel_token=0;
 wire [1:0] ready,token,single;wire [63:0] first0,first1;wire [AW:0] count0,count1;
 stream_banks #(.BANK_AW(AW)) bk(.reset(reset),.producer_clk(pclk),.host_clk(hclk),.packet_valid(pvalid),.packet_data(pdata),
  .packet_single(psingle),.seal(pseal),.packet_ready(seal_ok),.mem_write(mw),.mem_address(ma),.mem_data(md),.overrun(overrun),
  .host_overrun(host_overrun),.host_release(rel),.release_bank(rel_bank),.release_token(rel_token),.host_ready(ready),
  .host_token(token),.first_word0(first0),.first_word1(first1),.packets0(count0),.packets1(count1),.last_single(single));
 reg [63:0] mem[0:2*BANK-1];
 always @(posedge pclk)if(mw)mem[ma]<=md;

 integer errors=0;
 reg [63:0] expected=0;   // next stream word ordinal the host wants
 reg draining=1;
 integer host_delay=0; // host clocks before taking a ready bank
 integer blocks=0,singles=0;
 // Host: DrainStream semantics.
 task drain_one(output reg got);
  integer b,i,n;reg [63:0] first;reg [AW:0] cnt;reg sg,tk;reg [31:0] w;
  begin
   got=0;
   for(b=0;b<2 && !got;b=b+1)if(ready[b])begin
    first=b ? first1 : first0;
    if(first==expected)begin
     repeat(host_delay)@(posedge hclk);
     cnt=b ? count1 : count0;sg=single[b];tk=token[b];
     if(cnt==0 || cnt>BANK)begin $display("FAIL: bad count %0d",cnt);errors=errors+1;end
     n=cnt*2-sg;
     for(i=0;i<n;i=i+1)begin
      w=i[0] ? mem[b*BANK+i/2][63:32] : mem[b*BANK+i/2][31:0];
      if(w!==expected[31:0]+i)begin $display("FAIL: word %0d is %0d, want %0d",expected+i,w,expected[31:0]+i);errors=errors+1;end
     end
     if(sg)singles=singles+1;
     expected=expected+n;blocks=blocks+1;
     @(posedge hclk);rel<=1;rel_bank<=b;rel_token<=tk;@(posedge hclk);rel<=0;
     repeat(4)@(posedge hclk); // let ready fall through the release
     got=1;
    end
   end
  end
 endtask
 always begin
  reg got;
  @(posedge hclk);
  if(draining && !reset)drain_one(got);
 end
 task send(input integer n,input integer gap_lo,input integer gap_hi);
  integer k;
  begin
   for(k=0;k<n;k=k+1)begin
    @(posedge wclk);word_valid<=1;@(posedge wclk);word_valid<=0;word_data<=word_data+1;
    repeat(gap_lo+($urandom%(gap_hi-gap_lo+1)))@(posedge wclk);
   end
  end
 endtask
 initial begin
  repeat(20)@(posedge hclk);reset<=0;repeat(20)@(posedge hclk);
  // 1. A steady stream across many full banks, then slow trickles that only
  //    a timeout seal publishes, an odd count, and a stop.
  send(200,40,90);
  send(3,700,900);        // slower than SEAL_CYCLES: timeout seals with odd words
  send(41,40,60);
  @(posedge wclk);stop<=1;@(posedge wclk);stop<=0;
  wait(finished);repeat(200)@(posedge hclk);
  if(expected!==64'd244)begin $display("FAIL: drained %0d words, want 244",expected);errors=errors+1;end
  if(fault || overrun || host_overrun)begin $display("FAIL: fault %b overrun %b",fault,overrun);errors=errors+1;end
  if(singles==0)begin $display("FAIL: no single-word flush was exercised");errors=errors+1;end
  $display("phase 1: %0d words in %0d blocks, %0d single flushes",expected,blocks,singles);
  // 1b. Banks fill in about the seal period and the host takes each a little
  //     late: a seal just after a full bank must wait, not overrun (bench
  //     2026-10-10: decimation 2^12, a bank fills in exactly the seal period).
  reset<=1;repeat(20)@(posedge hclk);expected=0;blocks=0;word_data<=0;reset<=0;repeat(20)@(posedge hclk);
  host_delay=60;
  send(1500,17,20);
  @(posedge wclk);stop<=1;@(posedge wclk);stop<=0;
  wait(finished);repeat(400)@(posedge hclk);
  if(expected!==64'd1500 || fault || overrun)begin $display("FAIL: seal race: drained %0d of 1500, fault %b overrun %b",expected,fault,overrun);errors=errors+1;end
  $display("phase 1b: %0d words in %0d blocks, host delay %0d",expected,blocks,host_delay);
  host_delay=0;
  // 2. The host stops draining: the producer must report the loss.
  reset<=1;repeat(20)@(posedge hclk);expected=0;word_data<=0;reset<=0;repeat(20)@(posedge hclk);
  draining=0;
  send(4*BANK*2+8,40,50);
  repeat(50)@(posedge hclk);
  if(!overrun || !host_overrun)begin $display("FAIL: undrained banks did not raise overrun");errors=errors+1;end
  // 3. Bursts: a few words on consecutive clocks (the precision tail drains
  //    its FIFO so) all arrive; a burst beyond the FIFO is a fault, not a loss.
  reset<=1;repeat(20)@(posedge hclk);reset<=0;draining=1;expected=0;word_data<=0;repeat(20)@(posedge hclk);
  begin : burst
   integer k;
   for(k=0;k<6;k=k+1)begin @(posedge wclk);word_valid<=1;word_data<=k; end
   @(posedge wclk);word_valid<=0;
  end
  @(posedge wclk);stop<=1;@(posedge wclk);stop<=0;
  wait(finished);repeat(200)@(posedge hclk);
  if(expected!==64'd6 || fault)begin $display("FAIL: 6-word burst: drained %0d, fault %b",expected,fault);errors=errors+1;end
  reset<=1;repeat(20)@(posedge hclk);reset<=0;expected=0;word_data<=0;repeat(20)@(posedge hclk);
  begin : flood
   integer k;
   for(k=0;k<40;k=k+1)begin @(posedge wclk);word_valid<=1;word_data<=k; end
   @(posedge wclk);word_valid<=0;
  end
  repeat(100)@(posedge pclk);
  if(!fault)begin $display("FAIL: a 40-word burst did not raise fault");errors=errors+1;end
  if(errors==0)$display("PASS tb_stream");else $display("FAIL tb_stream: %0d errors",errors);
  $finish;
 end
 initial begin #20000000 $display("FAIL: timeout");$finish;end
endmodule
