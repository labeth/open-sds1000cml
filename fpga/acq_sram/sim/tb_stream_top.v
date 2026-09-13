`timescale 1ns/1ps
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign snapshot_ack=snapshot_request;assign word_data=32'h65646464;assign valid=enable;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
module adc_precision(input core,packclk,slowclk,enable,input [4:0] log2decim,input [31:0] data,input valid,
 output [31:0] result,output out_valid,output fault);
 reg [6:0] phase=0;
 always @(posedge core)if(!enable)phase<=0;else phase<=phase+1'b1;
 assign result=32'h64006400;assign out_valid=enable && phase==0;assign fault=0;
endmodule
module tb;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),
  .gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=#4.5 s1; /* Modeled board read-response latency exceeds one 4 ns period. */end
  address<=address+1'b1;
 end
 integer i;
 task command(input integer code);
 begin
  @(negedge clk);dut.opcode=code;dut.request=~dut.request;
  wait(dut.ack==dut.request);#100;
  if(dut.command_error)$fatal(1,"command rejected %0d",code);
 end endtask

 integer expected=0,b,n,j,words,verified;
 integer cc,cm,ff,edges,cv,rr,fl,want;reg tok;
 task drain;
 begin
  wait(dut.stream_available!=0);#30;
  b=dut.stream_available[0] ? 0 : 1;
  if(dut.stream_available==3)b=dut.stream_first0<dut.stream_first1 ? 0 : 1;
  if((b ? dut.stream_first1 : dut.stream_first0)!=expected)$fatal(1,"stream base %d",expected);
  n=b ? dut.stream_count1 : dut.stream_count0;tok=dut.stream_token[b];
  words=2*n-dut.stream_single[b];
  for(j=0;j<words;j=j+1)begin
   if((j%2 ? dut.buffer_mem64[b*(dut.BUF_WORDS/4)+j/2][63:32] : dut.buffer_mem64[b*(dut.BUF_WORDS/4)+j/2][31:0])!==32'(expected))$fatal(1,"top word %d",expected);
   expected=expected+1;
  end
  @(negedge clk);force dut.wc=1;force dut.ws=20;force dut.wd={14'b0,tok,1'(b)};
  @(negedge clk);release dut.wc;release dut.ws;release dut.wd;#100;
 end endtask
 initial begin
  #100;
  for(cc=0;cc<16;cc=cc+1)for(cm=0;cm<2;cm=cm+1)for(ff=0;ff<2;ff=ff+1)for(edges=0;edges<16;edges=edges+1)begin
   cv=(cc&1)|((cc&2)<<3)|((cc&4)<<3)|((cc&8)<<3);
   dut.config_word=cv;dut.stream_cfg=cm ? 3 : 0;rr=edges&3;fl=(edges>>2)&3;
   force dut.rise_ch=rr;force dut.fall_ch=fl;force dut.force_trigger=ff;
   #100;
   want=cm ? ff : (!(cv&16) || ff || ((cv&1) && (((cv&32) ? fl : rr) & ((cv&64) ? 2 : 1))));
   if(dut.il_fire_stage!==1'(want))$fatal(1,"trigger decode cfg=%d mode=%d force=%d edges=%d",cv,cm,ff,edges);
  end
  release dut.rise_ch;release dut.fall_ch;release dut.force_trigger;
  dut.config_word=0;#100;dut.decim_log=8;dut.pre_cfg=16;dut.post_cfg=8;dut.stream_cfg=3;
  #100;command(1);
  repeat(4)drain();
  if(!dut.running || dut.record_done || dut.il_failed)$fatal(1,"continuous mode stopped early");
  wait(dut.ramp==(2*dut.BUF_WORDS+1));command(5);wait(dut.ready && dut.stream_finished_cpu[2]);#100;
  while(dut.stream_available!=0)drain();
  if(expected!=dut.record_length || expected!=(2*dut.BUF_WORDS+1))$fatal(1,"tail loss %d length %d",expected,dut.record_length);
  for(i=0;i<expected;i=i+1)if(mem[(dut.origin-1+i)%524288]!==i)$fatal(1,"SRAM history changed %d",i);
  verified=expected;
  // A frozen SRAM read may not overwrite banks while stream mode owns RAM.
  dut.count_cfg=64;
  @(negedge clk);dut.opcode=2;dut.request=~dut.request;
  wait(dut.ack==dut.request);#100;
  if(!dut.command_error)$fatal(1,"read accepted while stream owns RAM");
  dut.stream_cfg=0;#200;command(2);wait(dut.ready);#100;
  if(dut.received!=64)$fatal(1,"recall did not resume");
  // The old capture mode still freezes with the requested record length.
  dut.pre_cfg=239;dut.post_cfg=17;#100;command(1);wait(dut.record_done && dut.ready);#100;
  if(dut.record_length!=256 || dut.il_failed)$fatal(1,"legacy capture regressed");
  dut.stream_cfg=3;dut.pre_cfg=16;dut.post_cfg=8;#100;command(1);
  wait(dut.il_failed && dut.ready);#200;
  if(!dut.host_bank_overrun || dut.stream_available!=3)$fatal(1,"overrun did not preserve banks and fault");
  expected=0;command(1);drain();
  if(dut.il_failed || !dut.running)$fatal(1,"stale fault poisoned rearm");
  command(5);wait(dut.ready);
  $display("PASS integrated stream tap: %d exact words, continuous SRAM history, stop, ownership exclusion, legacy capture, overrun/rearm, exhaustive trigger decode",verified);$finish;
 end
 initial begin #25000000;$fatal(1,"timeout");end
endmodule
