`timescale 1ns/1ps
module tb_host_ownership;
 reg reset=1,pclk=0,hclk=0,cclk=0,pause_producer=0,pause_host=0,pause_core=0;
 always #4 if(!pause_producer)pclk=~pclk;
 always #5 if(!pause_host)hclk=~hclk;
 always #2 if(!pause_core)cclk=~cclk;
 reg [1:0] publish=0;reg [63:0] first0=0,first1=0;reg [11:0] words0=0,words1=0;
 reg release_en=0,bank=0,token=0;
 wire fault;wire [1:0] busy,ready,tokens,releases;
 wire [63:0] f0,f1;wire [11:0] w0,w1;
 sram_host_ownership dut(reset,pclk,hclk,cclk,publish,first0,first1,words0,words1,
  fault,busy,release_en,bank,token,ready,tokens,f0,f1,w0,w1,releases);
 integer release_count=0,generation,before_release;
 always @(posedge cclk)if(!reset)begin
  if(|(releases & busy))$fatal(1,"core release before RAM acknowledgment");
  release_count=release_count+releases[0]+releases[1];
 end
 task release_bank(input bit b,input bit t);
  begin @(negedge hclk);release_en=1;bank=b;token=t;
   @(negedge hclk);release_en=0;repeat(8)@(negedge hclk);
  end
 endtask
 task pub0(input integer n);
  begin @(negedge pclk);first0=64'habcdef0000000000+n;words0=n+1;publish=1;
   @(negedge pclk);publish=0;
   wait(ready[0]);#1;
   if(f0!==64'habcdef0000000000+n || w0!=n+1)$fatal(1,"torn metadata");
  end
 endtask
 initial begin
  repeat(4)@(negedge hclk);reset=0;repeat(8)@(negedge hclk);
  // Both banks may be published together and released in either order.
  @(negedge pclk);first0=100;words0=2560;first1=2660;words1=1;publish=3;
  @(negedge pclk);publish=0;wait(ready==3);#1;
  if(f0!=100 || f1!=2660 || w0!=2560 || w1!=1)$fatal(1,"simultaneous publication");
  release_bank(1,0);if(ready!=3 || release_count!=0)$fatal(1,"wrong token accepted");
  release_bank(1,1);if(ready!=1 || release_count!=1)$fatal(1,"bank1 release");
  release_bank(1,1);if(release_count!=1)$fatal(1,"duplicate release");
  release_bank(0,1);if(ready || busy || release_count!=2)$fatal(1,"bank0 release");
  for(generation=0;generation<20;generation=generation+1)begin
   pub0(generation);
   release_bank(0,!tokens[0]);if(!ready[0])$fatal(1,"stale token accepted");
   release_bank(0,tokens[0]);if(ready[0] || busy[0])$fatal(1,"reuse failed");
  end
  if(release_count!=22 || fault)$fatal(1,"release accounting");
  pub0(29);
  @(negedge pclk);pause_producer=1;before_release=release_count;
  release_bank(0,tokens[0]);
  if(release_count!=before_release || !busy[0])$fatal(1,"release bypassed stopped RAM clock");
  pause_producer=0;repeat(10)@(negedge hclk);
  if(release_count!=before_release+1 || busy[0])$fatal(1,"release lost after RAM clock resume");
  pub0(30);
  @(negedge pclk);first0=0;words0=0;publish=1;
  @(negedge pclk);publish=0;repeat(8)@(negedge hclk);
  if(!fault || f0!=64'habcdef000000001e || w0!=31)$fatal(1,"busy descriptor changed");
  #1;reset=1;#1;if(ready || releases)$fatal(1,"reset visible ownership");
  repeat(4)@(negedge hclk);reset=0;repeat(8)@(negedge hclk);
  if(fault || busy || ready)$fatal(1,"epoch reset");
  pub0(31);
  @(negedge pclk);pause_producer=1;
  @(negedge hclk);pause_host=1;
  @(negedge cclk);pause_core=1;
  #0.3;reset=1;#1;
  if(ready || releases || busy)$fatal(1,"stopped-clock reset failed to abandon ownership");
  reset=0;#20;
  if(ready || releases || busy || dut.hr!=3 || dut.pr!=3 || dut.cr!=3)
   $fatal(1,"reset released without destination clocks");
  pause_producer=0;pause_host=0;pause_core=0;
  repeat(10)@(negedge hclk);
  if(ready || releases || busy || fault)$fatal(1,"old epoch reappeared after resume");
  before_release=release_count;
  pub0(32);release_bank(0,tokens[0]);
  if(ready || busy || release_count!=before_release+1)$fatal(1,"new epoch failed after resume");
  $display("PASS ownership: metadata, dual banks, 20 reuses, stale/duplicate releases, overwrite fault, reset, short reset with all clocks stopped and new epoch");$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule
